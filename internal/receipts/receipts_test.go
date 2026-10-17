package receipts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/malware"
)

var (
	pdfBytes = append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 100)...)
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 100)...)
)

type fakeStore struct {
	objects map[string][]byte
	deleted []string
	putErr  error
}

func (f *fakeStore) Put(_ context.Context, key string, r io.Reader, size int64, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, _ := io.ReadAll(r)
	if int64(len(b)) != size {
		return errors.New("size mismatch")
	}
	f.objects[key] = b
	return nil
}
func (f *fakeStore) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeStore) List(context.Context, string, func(string, time.Time) error) error { return nil }
func (f *fakeStore) SignedGetURL(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://signed.example/" + key, nil
}

type fakeRepo struct {
	owner     uuid.UUID
	purchase  uuid.UUID
	rec       *Record
	upsertErr error
}

func (f *fakeRepo) Owns(_ context.Context, u, p uuid.UUID) (bool, error) {
	return u == f.owner && p == f.purchase, nil
}
func (f *fakeRepo) Upsert(_ context.Context, _, _ uuid.UUID, rec Record) (*string, error) {
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	var old *string
	if f.rec != nil {
		k := f.rec.StorageKey
		old = &k
	}
	f.rec = &rec
	return old, nil
}
func (f *fakeRepo) Get(_ context.Context, u, p uuid.UUID) (Record, error) {
	if f.rec == nil || u != f.owner || p != f.purchase {
		return Record{}, ErrNotFound
	}
	return *f.rec, nil
}
func (f *fakeRepo) Delete(_ context.Context, u, p uuid.UUID) (string, error) {
	if f.rec == nil || u != f.owner || p != f.purchase {
		return "", ErrNotFound
	}
	k := f.rec.StorageKey
	f.rec = nil
	return k, nil
}

type fakePurger struct {
	calls int
	err   error
}

func (f *fakePurger) RequestPurge(context.Context) error { f.calls++; return f.err }

func setup(max int64) (*Service, *fakeRepo, *fakeStore, uuid.UUID, uuid.UUID) {
	u, p := uuid.New(), uuid.New()
	repo := &fakeRepo{owner: u, purchase: p}
	st := &fakeStore{objects: map[string][]byte{}}
	return NewService(repo, st, malware.Disabled{}, &fakePurger{}, max, slog.New(slog.NewTextHandler(io.Discard, nil))), repo, st, u, p
}

func TestUploadHappyPathUsesSniffedTypeAndRandomKey(t *testing.T) {
	svc, repo, st, u, p := setup(1 << 20)
	// Filename lies about the type; the sniffed type must win.
	r, err := svc.Upload(context.Background(), u, p, "../../evil.exe", int64(len(pngBytes)), bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentType != "image/png" || r.SizeBytes != int64(len(pngBytes)) || r.FileName != "evil.exe" {
		t.Fatalf("unexpected receipt: %+v", r)
	}
	if !strings.HasPrefix(repo.rec.StorageKey, "receipts/"+u.String()+"/") || !strings.HasSuffix(repo.rec.StorageKey, ".png") {
		t.Fatalf("key: %s", repo.rec.StorageKey)
	}
	if strings.Contains(repo.rec.StorageKey, "evil") {
		t.Fatal("storage key must not contain user-supplied name")
	}
	if len(st.objects) != 1 {
		t.Fatal("object not stored")
	}
}

func TestUploadRejections(t *testing.T) {
	svc, _, st, u, p := setup(200)
	ctx := context.Background()
	if _, err := svc.Upload(ctx, u, p, "a.png", 5, strings.NewReader("<html>not an image</html>")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("html as png: %v", err)
	}
	if _, err := svc.Upload(ctx, u, p, "a.pdf", 0, strings.NewReader("")); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	big := append([]byte("%PDF-"), bytes.Repeat([]byte("x"), 300)...)
	// Declared size lies (small) but the real body exceeds the cap.
	if _, err := svc.Upload(ctx, u, p, "a.pdf", 10, bytes.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("real size over cap: %v", err)
	}
	if _, err := svc.Upload(ctx, u, p, "a.pdf", 999, bytes.NewReader(pdfBytes)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("declared size over cap: %v", err)
	}
	if len(st.objects) != 0 {
		t.Errorf("nothing should have been stored, got %d objects", len(st.objects))
	}
}

func TestUploadNonOwnerGetsNotFoundAndStoresNothing(t *testing.T) {
	svc, _, st, _, p := setup(1 << 20)
	_, err := svc.Upload(context.Background(), uuid.New(), p, "a.pdf", 10, bytes.NewReader(pdfBytes))
	if !errors.Is(err, ErrNotFound) || len(st.objects) != 0 {
		t.Fatalf("err=%v objects=%d", err, len(st.objects))
	}
}

// The web process never deletes a replaced file itself: the DB trigger queued it and the worker
// removes it. The service only nudges the worker.
func TestReplaceNudgesWorkerInsteadOfDeleting(t *testing.T) {
	svc, repo, st, u, p := setup(1 << 20)
	purger := svc.purger.(*fakePurger)
	ctx := context.Background()
	svc.Upload(ctx, u, p, "a.pdf", 10, bytes.NewReader(pdfBytes))
	first := repo.rec.StorageKey
	if purger.calls != 0 {
		t.Fatal("first upload replaces nothing; no purge needed")
	}
	svc.Upload(ctx, u, p, "b.pdf", 10, bytes.NewReader(pdfBytes))
	if purger.calls != 1 {
		t.Fatalf("purge requests = %d, want 1", purger.calls)
	}
	if _, still := st.objects[first]; !still || len(st.objects) != 2 {
		t.Fatal("web must leave the old object for the worker to delete")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("web deleted objects directly: %v", st.deleted)
	}
}

func TestQueueOutageNeverFailsRequests(t *testing.T) {
	svc, _, _, u, p := setup(1 << 20)
	svc.purger.(*fakePurger).err = errors.New("redis down")
	ctx := context.Background()
	if _, err := svc.Upload(ctx, u, p, "a.pdf", 10, bytes.NewReader(pdfBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(ctx, u, p, "b.pdf", 10, bytes.NewReader(pdfBytes)); err != nil {
		t.Fatalf("replace failed because Redis is down: %v", err)
	}
	if err := svc.Delete(ctx, u, p); err != nil {
		t.Fatalf("delete failed because Redis is down: %v", err)
	}
}

func TestDBFailureRollsBackObject(t *testing.T) {
	svc, repo, st, u, p := setup(1 << 20)
	repo.upsertErr = errors.New("db down")
	if _, err := svc.Upload(context.Background(), u, p, "a.pdf", 10, bytes.NewReader(pdfBytes)); err == nil {
		t.Fatal("expected error")
	}
	if len(st.objects) != 0 {
		t.Fatal("orphan object left in storage")
	}
}

func TestSignedURLAndDeleteAreOwnerScoped(t *testing.T) {
	svc, _, st, u, p := setup(1 << 20)
	ctx := context.Background()
	svc.Upload(ctx, u, p, "a.pdf", 10, bytes.NewReader(pdfBytes))
	if _, _, _, err := svc.SignedURL(ctx, uuid.New(), p); !errors.Is(err, ErrNotFound) {
		t.Errorf("other user signed url: %v", err)
	}
	if err := svc.Delete(ctx, uuid.New(), p); !errors.Is(err, ErrNotFound) || len(st.objects) != 1 {
		t.Errorf("other user delete: %v", err)
	}
	if url, exp, _, err := svc.SignedURL(ctx, u, p); err != nil || url == "" || time.Until(exp) > SignedURLTTL {
		t.Errorf("owner signed url: %v %v", url, err)
	}
	purger := svc.purger.(*fakePurger)
	if err := svc.Delete(ctx, u, p); err != nil || purger.calls != 1 {
		t.Errorf("owner delete: err=%v purge requests=%d", err, purger.calls)
	}
	if len(st.objects) != 1 {
		t.Errorf("web must not delete the file itself; the worker does via the outbox")
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"receipt.pdf":            "receipt.pdf",
		`C:\Users\x\scan.jpg`:    "scan.jpg",
		"../../etc/passwd":       "passwd",
		".hidden":                "hidden",
		"":                       "receipt.pdf",
		"..":                     "receipt.pdf",
		"a\x00b\nc.png":          "abc.png",
		`na"me.pdf`:              "name.pdf",
		strings.Repeat("é", 300): strings.Repeat("é", 127),
	}
	for in, want := range cases {
		if got := SanitizeFilename(in, "pdf"); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

type stubScanner struct{ err error }

func (s stubScanner) Scan(context.Context, []byte) error { return s.err }

func TestMalwareScanFailsClosed(t *testing.T) {
	for name, scanErr := range map[string]error{
		"infected":    malware.ErrInfected,
		"unavailable": malware.ErrUnavailable,
	} {
		svc, repo, st, u, p := setup(1 << 20)
		svc.scanner = stubScanner{scanErr}
		_, err := svc.Upload(context.Background(), u, p, "a.pdf", 10, bytes.NewReader(pdfBytes))
		if !errors.Is(err, scanErr) {
			t.Errorf("%s: err=%v", name, err)
		}
		if len(st.objects) != 0 || repo.rec != nil {
			t.Errorf("%s: file or metadata stored despite scan failure", name)
		}
	}
}
