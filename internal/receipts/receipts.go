// Package receipts handles receipt file validation, storage and metadata.
package receipts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/storage"
)

var (
	ErrNotFound    = errors.New("receipts: not found")
	ErrTooLarge    = errors.New("receipts: file too large")
	ErrUnsupported = errors.New("receipts: unsupported file type")
	ErrEmpty       = errors.New("receipts: empty file")
	ErrLimit       = errors.New("receipts: too many receipts for this purchase")
)

const SignedURLTTL = 5 * time.Minute

// MaxPerPurchase is how many receipt files one purchase can hold.
const MaxPerPurchase = 5

var allowedTypes = map[string]string{
	"application/pdf": "pdf",
	"image/jpeg":      "jpg",
	"image/png":       "png",
}

type Receipt struct {
	ID          uuid.UUID `json:"id"`
	FileName    string    `json:"fileName"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	UploadedAt  time.Time `json:"uploadedAt"`
}

type Record struct {
	Receipt
	StorageKey string
}

type Repository interface {
	// Owns reports whether the purchase exists and belongs to the user.
	Owns(ctx context.Context, userID, purchaseID uuid.UUID) (bool, error)
	// Upsert is the original single-receipt upload: it replaces the oldest receipt (or adds the first) and returns the
	// replaced storage key, if any, and the stored receipt's id.
	Upsert(ctx context.Context, userID, purchaseID uuid.UUID, rec Record) (oldKey *string, id uuid.UUID, err error)
	// Add stores one more receipt; ErrLimit when the purchase already has `limit`.
	Add(ctx context.Context, userID, purchaseID uuid.UUID, rec Record, limit int) (id uuid.UUID, err error)
	// Get and Delete without an id mean the oldest receipt of the purchase.
	Get(ctx context.Context, userID, purchaseID uuid.UUID) (Record, error)
	GetByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (Record, error)
	// Delete removes metadata and returns the storage key to purge.
	Delete(ctx context.Context, userID, purchaseID uuid.UUID) (key string, err error)
	DeleteByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (key string, err error)
}

// Purger asks the worker process to delete queued receipt files now. Postgres already holds the
// deletion (object_deletions outbox), so a failed request only delays cleanup until the worker's
// next periodic run. *queue.Client implements it.
type Purger interface {
	RequestPurge(ctx context.Context) error
}

type Service struct {
	repo     Repository
	store    storage.Store
	scanner  malware.Scanner
	purger   Purger
	maxBytes int64
	log      *slog.Logger
}

func NewService(r Repository, s storage.Store, sc malware.Scanner, p Purger, maxBytes int64, log *slog.Logger) *Service {
	return &Service{repo: r, store: s, scanner: sc, purger: p, maxBytes: maxBytes, log: log}
}

// MaxBytes is the configured upload cap.
func (s *Service) MaxBytes() int64 { return s.maxBytes }

// stage validates a file and puts it in the store for a purchase owned by userID. The caller must either record
// it in the database or call deleteUnreferenced.
func (s *Service) stage(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (Record, error) {
	if declaredSize > s.maxBytes {
		return Record{}, ErrTooLarge
	}
	ok, err := s.repo.Owns(ctx, userID, purchaseID)
	if err != nil {
		return Record{}, err
	}
	if !ok {
		return Record{}, ErrNotFound
	}

	// Never trust the declared MIME type: sniff the real content.
	head := make([]byte, 512)
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Record{}, fmt.Errorf("receipts: read: %w", err)
	}
	if n == 0 {
		return Record{}, ErrEmpty
	}
	head = head[:n]
	ctype := SniffType(head)
	ext, allowed := allowedTypes[ctype]
	if !allowed {
		return Record{}, ErrUnsupported
	}

	// Enforce the cap on the actual bytes read, not just the declared size.
	// The cap is small (MB), so buffer the capped body to give the store an exact size.
	data, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(head), body), s.maxBytes+1))
	if err != nil {
		return Record{}, fmt.Errorf("receipts: read: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return Record{}, ErrTooLarge
	}
	// Fail closed: a file that was not verified clean is never stored.
	if err := s.scanner.Scan(ctx, data); err != nil {
		if errors.Is(err, malware.ErrInfected) {
			s.log.Warn("receipt upload rejected: malware detected", "detail", err.Error())
		} else {
			s.log.Error("receipt upload rejected: scan failed", "err", err)
		}
		return Record{}, err
	}
	key := fmt.Sprintf("receipts/%s/%s.%s", userID, uuid.NewString(), ext) // random, opaque key
	if err := s.store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ctype); err != nil {
		return Record{}, err
	}
	return Record{
		Receipt:    Receipt{FileName: SanitizeFilename(filename, ext), ContentType: ctype, SizeBytes: int64(len(data)), UploadedAt: time.Now().UTC()},
		StorageKey: key,
	}, nil
}

// Upload is the original single-receipt upload: it validates and stores a receipt for a purchase owned by userID,
// replacing the oldest existing one (or adding the first).
func (s *Service) Upload(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (Receipt, error) {
	rec, err := s.stage(ctx, userID, purchaseID, filename, declaredSize, body)
	if err != nil {
		return Receipt{}, err
	}
	oldKey, id, err := s.repo.Upsert(ctx, userID, purchaseID, rec)
	if err != nil {
		s.deleteUnreferenced(rec.StorageKey)
		return Receipt{}, err
	}
	if oldKey != nil {
		s.RequestPurge(ctx) // the trigger queued the replaced file
	}
	rec.ID = id
	return rec.Receipt, nil
}

// Add stores one more receipt for the purchase (at most MaxPerPurchase; ErrLimit beyond that).
func (s *Service) Add(ctx context.Context, userID, purchaseID uuid.UUID, filename string, declaredSize int64, body io.Reader) (Receipt, error) {
	rec, err := s.stage(ctx, userID, purchaseID, filename, declaredSize, body)
	if err != nil {
		return Receipt{}, err
	}
	id, err := s.repo.Add(ctx, userID, purchaseID, rec, MaxPerPurchase)
	if err != nil {
		s.deleteUnreferenced(rec.StorageKey)
		return Receipt{}, err
	}
	rec.ID = id
	return rec.Receipt, nil
}

// SignedURL returns a short-lived download URL for the oldest receipt of the purchase (the original API).
func (s *Service) SignedURL(ctx context.Context, userID, purchaseID uuid.UUID) (string, time.Time, Receipt, error) {
	rec, err := s.repo.Get(ctx, userID, purchaseID)
	if err != nil {
		return "", time.Time{}, Receipt{}, err
	}
	return s.sign(ctx, rec)
}

// SignedURLByID is SignedURL for one named receipt, after an ownership-scoped lookup.
func (s *Service) SignedURLByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (string, time.Time, Receipt, error) {
	rec, err := s.repo.GetByID(ctx, userID, purchaseID, id)
	if err != nil {
		return "", time.Time{}, Receipt{}, err
	}
	return s.sign(ctx, rec)
}

func (s *Service) sign(ctx context.Context, rec Record) (string, time.Time, Receipt, error) {
	u, err := s.store.SignedGetURL(ctx, rec.StorageKey, rec.FileName, SignedURLTTL)
	if err != nil {
		return "", time.Time{}, Receipt{}, err
	}
	return u, time.Now().UTC().Add(SignedURLTTL), rec.Receipt, nil
}

// Delete removes the oldest receipt of the purchase (the original API).
func (s *Service) Delete(ctx context.Context, userID, purchaseID uuid.UUID) error {
	if _, err := s.repo.Delete(ctx, userID, purchaseID); err != nil {
		return err
	}
	s.RequestPurge(ctx) // the trigger queued the file for deletion
	return nil
}

// DeleteByID removes one named receipt.
func (s *Service) DeleteByID(ctx context.Context, userID, purchaseID, id uuid.UUID) error {
	if _, err := s.repo.DeleteByID(ctx, userID, purchaseID, id); err != nil {
		return err
	}
	s.RequestPurge(ctx)
	return nil
}

// RequestPurge nudges the worker to process the deletion outbox immediately. Best effort: it
// never fails the caller, because the database already records what must be deleted.
func (s *Service) RequestPurge(ctx context.Context) {
	if s.purger == nil {
		return
	}
	if err := s.purger.RequestPurge(ctx); err != nil {
		s.log.Warn("could not request immediate purge; the worker will purge on its next run", "err", err)
	}
}

// deleteUnreferenced removes an object this request just uploaded but never committed. It has no
// database row (so no outbox entry); if this fails, the worker's orphan sweep removes it later.
func (s *Service) deleteUnreferenced(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.Delete(ctx, key); err != nil {
		s.log.Error("could not remove uncommitted receipt object; orphan sweep will", "err", err)
	}
}

// SniffType detects the real content type from magic bytes.
func SniffType(head []byte) string {
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return "application/pdf"
	}
	t := http.DetectContentType(head)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}

// SanitizeFilename keeps a safe display name: base name only, no control characters, max 255 bytes.
func SanitizeFilename(name, ext string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.TrimLeft(name, "."))
	if name == "" || name == "." {
		return "receipt." + ext
	}
	for len(name) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}
