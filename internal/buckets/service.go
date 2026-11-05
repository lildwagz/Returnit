package buckets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound: no such bucket, or the person is not a member of it (the two are not told apart).
	ErrNotFound = errors.New("buckets: not found")
	// ErrNotOwner: a member asked for something only the owner may do.
	ErrNotOwner = errors.New("buckets: only the owner can do this")
	// ErrValidation: the request itself is wrong (name, a personal bucket, naming yourself).
	ErrValidation = errors.New("buckets: invalid request")
	// ErrConflict: the request is fine but the bucket's state does not allow it (full, already a member, limits).
	ErrConflict = errors.New("buckets: conflict")
	// ErrInviteGone: the invite is expired, revoked or unknown (not told apart, so a guess learns nothing).
	ErrInviteGone = errors.New("buckets: invite is no longer valid")
)

const (
	MaxMembers     = 6  // people in one bucket
	MaxMemberships = 10 // buckets one person can be in, personal included
	MaxCreated     = 5  // shared buckets one person can create
	InviteTTL      = 7 * 24 * time.Hour
	roleOwner      = "owner"
)

// ObjectPurger asks the worker to delete receipt files after their purchases were deleted with a bucket.
type ObjectPurger interface {
	RequestPurge(ctx context.Context)
}

type Service struct {
	pool   *pgxpool.Pool
	purger ObjectPurger // may be nil
}

func NewService(p *pgxpool.Pool, purger ObjectPurger) *Service {
	return &Service{pool: p, purger: purger}
}

// Bucket is one bucket as the person sees it.
type Bucket struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Personal      bool      `json:"personal"`
	Role          string    `json:"role"` // owner or member
	Notify        bool      `json:"notify"`
	MemberCount   int       `json:"memberCount"`
	PurchaseCount int       `json:"purchaseCount"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Member shows a co-member by display name only (never the e-mail address).
type Member struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
	You      bool      `json:"you"`
}

// InvitePreview is what the join screen shows before the person agrees.
type InvitePreview struct {
	BucketName  string  `json:"bucketName"`
	InvitedBy   *string `json:"invitedBy"`
	MemberCount int     `json:"memberCount"`
	Full        bool    `json:"full"`
}

type Invite struct {
	Token     string    `json:"token"` // shown once; only its hash is stored
	ExpiresAt time.Time `json:"expiresAt"`
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const bucketCols = `b.id, b.name, b.personal, bm.role, bm.notify, b.created_at,
  (SELECT count(*) FROM bucket_members x WHERE x.bucket_id = b.id),
  (SELECT count(*) FROM purchases p WHERE p.bucket_id = b.id)`

func scanBucket(row pgx.Row) (Bucket, error) {
	var b Bucket
	err := row.Scan(&b.ID, &b.Name, &b.Personal, &b.Role, &b.Notify, &b.CreatedAt, &b.MemberCount, &b.PurchaseCount)
	return b, err
}

func getFor(ctx context.Context, q queryer, userID, id uuid.UUID) (Bucket, error) {
	b, err := scanBucket(q.QueryRow(ctx, `SELECT `+bucketCols+`
FROM bucket_members bm JOIN buckets b ON b.id = bm.bucket_id WHERE bm.user_id = $1 AND b.id = $2`, userID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Bucket{}, ErrNotFound
	}
	return b, err
}

func cleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := len([]rune(s)); n < 1 || n > 60 {
		return "", fmt.Errorf("%w: name must be 1 to 60 characters", ErrValidation)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: name contains control characters", ErrValidation)
		}
	}
	return s, nil
}

// List returns the buckets the person is in, the personal one first.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Bucket, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+bucketCols+`
FROM bucket_members bm JOIN buckets b ON b.id = bm.bucket_id WHERE bm.user_id = $1
ORDER BY b.personal DESC, lower(b.name), b.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("buckets: list: %w", err)
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, fmt.Errorf("buckets: list scan: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Bucket, error) {
	b, err := getFor(ctx, s.pool, userID, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Bucket{}, fmt.Errorf("buckets: get: %w", err)
	}
	return b, err
}

// Create makes a shared bucket with the person as its owner.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (Bucket, error) {
	name, err := cleanName(name)
	if err != nil {
		return Bucket{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Bucket{}, err
	}
	defer tx.Rollback(ctx)
	// The user row is the lock for the per-person limits, so two requests cannot both pass the count.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		return Bucket{}, fmt.Errorf("buckets: lock user: %w", err)
	}
	var memberships, created int
	if err := tx.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM bucket_members WHERE user_id = $1),
  (SELECT count(*) FROM buckets WHERE created_by = $1 AND NOT personal)`, userID).Scan(&memberships, &created); err != nil {
		return Bucket{}, fmt.Errorf("buckets: count: %w", err)
	}
	if memberships >= MaxMemberships {
		return Bucket{}, fmt.Errorf("%w: you are in the maximum of %d buckets", ErrConflict, MaxMemberships)
	}
	if created >= MaxCreated {
		return Bucket{}, fmt.Errorf("%w: you can create at most %d shared buckets", ErrConflict, MaxCreated)
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO buckets (name, created_by) VALUES ($1, $2) RETURNING id`, name, userID).Scan(&id); err != nil {
		return Bucket{}, fmt.Errorf("buckets: create: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bucket_members (bucket_id, user_id, role) VALUES ($1, $2, 'owner')`, id, userID); err != nil {
		return Bucket{}, fmt.Errorf("buckets: create member: %w", err)
	}
	b, err := getFor(ctx, tx, userID, id)
	if err != nil {
		return Bucket{}, fmt.Errorf("buckets: create read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Bucket{}, fmt.Errorf("buckets: commit: %w", err)
	}
	return b, nil
}

// lock takes the bucket row (always first, so concurrent changes to one bucket cannot deadlock) and returns the
// caller's role and whether the bucket is personal. ErrNotFound when the caller is not a member.
func lock(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (role string, personal bool, name string, err error) {
	err = tx.QueryRow(ctx, `SELECT b.personal, b.name FROM buckets b WHERE b.id = $1 FOR UPDATE`, id).Scan(&personal, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, "", ErrNotFound
	}
	if err != nil {
		return "", false, "", fmt.Errorf("buckets: lock: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT role FROM bucket_members WHERE bucket_id = $1 AND user_id = $2`, id, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, "", ErrNotFound
	}
	if err != nil {
		return "", false, "", fmt.Errorf("buckets: role: %w", err)
	}
	return role, personal, name, nil
}

func (s *Service) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("buckets: begin: %w", err)
	}
	return tx, nil
}

func ownerOf(role string, personal bool) error {
	if personal {
		return fmt.Errorf("%w: this is your personal bucket", ErrValidation)
	}
	if role != roleOwner {
		return ErrNotOwner
	}
	return nil
}

func (s *Service) Rename(ctx context.Context, userID, id uuid.UUID, name string) (Bucket, error) {
	name, err := cleanName(name)
	if err != nil {
		return Bucket{}, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Bucket{}, err
	}
	defer tx.Rollback(ctx)
	role, personal, _, err := lock(ctx, tx, userID, id)
	if err != nil {
		return Bucket{}, err
	}
	if err := ownerOf(role, personal); err != nil {
		return Bucket{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE buckets SET name = $2, updated_at = now() WHERE id = $1`, id, name); err != nil {
		return Bucket{}, fmt.Errorf("buckets: rename: %w", err)
	}
	b, err := getFor(ctx, tx, userID, id)
	if err != nil {
		return Bucket{}, fmt.Errorf("buckets: rename read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Bucket{}, fmt.Errorf("buckets: commit: %w", err)
	}
	return b, nil
}

// Delete removes a shared bucket with its purchases and receipts. The caller must repeat the bucket's exact name.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID, confirmName string) error {
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, personal, name, err := lock(ctx, tx, userID, id)
	if err != nil {
		return err
	}
	if err := ownerOf(role, personal); err != nil {
		return err
	}
	if confirmName != name {
		return fmt.Errorf("%w: type the exact name of the bucket to delete it", ErrValidation)
	}
	// Purchases and their receipts go with it (the receipts trigger queues the files for deletion).
	if _, err := tx.Exec(ctx, `DELETE FROM buckets WHERE id = $1`, id); err != nil {
		return fmt.Errorf("buckets: delete: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("buckets: commit: %w", err)
	}
	if s.purger != nil {
		s.purger.RequestPurge(ctx)
	}
	return nil
}

func (s *Service) Members(ctx context.Context, userID, id uuid.UUID) ([]Member, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bucket_members WHERE bucket_id = $1 AND user_id = $2)`, id, userID).Scan(&ok); err != nil {
		return nil, fmt.Errorf("buckets: members: %w", err)
	}
	if !ok {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
SELECT u.id, COALESCE(NULLIF(trim(u.display_name), ''), split_part(u.email, '@', 1)), bm.role, bm.joined_at, u.id = $2
FROM bucket_members bm JOIN users u ON u.id = bm.user_id
WHERE bm.bucket_id = $1
ORDER BY (bm.role = 'owner') DESC, bm.joined_at, u.id`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("buckets: members: %w", err)
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.Role, &m.JoinedAt, &m.You); err != nil {
			return nil, fmt.Errorf("buckets: members scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RemoveMember removes `target` from the bucket. A person removes themselves to leave; only the owner removes others.
// The owner cannot leave: they transfer ownership first, or delete the bucket.
func (s *Service) RemoveMember(ctx context.Context, actor, id, target uuid.UUID) error {
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, personal, _, err := lock(ctx, tx, actor, id)
	if err != nil {
		return err
	}
	if personal {
		return fmt.Errorf("%w: you cannot leave your personal bucket", ErrValidation)
	}
	if target == actor {
		if role == roleOwner {
			return fmt.Errorf("%w: transfer ownership before leaving, or delete the bucket", ErrConflict)
		}
	} else if role != roleOwner {
		return ErrNotOwner
	}
	tag, err := tx.Exec(ctx, `DELETE FROM bucket_members WHERE bucket_id = $1 AND user_id = $2`, id, target)
	if err != nil {
		return fmt.Errorf("buckets: remove: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("buckets: commit: %w", err)
	}
	return nil
}

// TransferOwner makes another member the owner; the caller becomes a member.
func (s *Service) TransferOwner(ctx context.Context, actor, id, newOwner uuid.UUID) error {
	if newOwner == actor {
		return fmt.Errorf("%w: you are already the owner", ErrValidation)
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, personal, _, err := lock(ctx, tx, actor, id)
	if err != nil {
		return err
	}
	if err := ownerOf(role, personal); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bucket_members SET role = 'owner' WHERE bucket_id = $1 AND user_id = $2`, id, newOwner)
	if err != nil {
		return fmt.Errorf("buckets: transfer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: that person is not a member", ErrValidation)
	}
	if _, err := tx.Exec(ctx, `UPDATE bucket_members SET role = 'member' WHERE bucket_id = $1 AND user_id = $2`, id, actor); err != nil {
		return fmt.Errorf("buckets: transfer: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("buckets: commit: %w", err)
	}
	return nil
}

// SetNotify mutes or unmutes the bucket's reminders for this person only.
func (s *Service) SetNotify(ctx context.Context, userID, id uuid.UUID, notify bool) (Bucket, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE bucket_members SET notify = $3 WHERE bucket_id = $1 AND user_id = $2`, id, userID, notify)
	if err != nil {
		return Bucket{}, fmt.Errorf("buckets: notify: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Bucket{}, ErrNotFound
	}
	return s.Get(ctx, userID, id)
}

// normalizeToken accepts a pasted code: case, spaces and hyphens do not matter.
func normalizeToken(t string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, t)
}

func hashToken(t string) []byte {
	sum := sha256.Sum256([]byte(normalizeToken(t)))
	return sum[:]
}

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// CreateInvite makes a new invite for a shared bucket and revokes the previous active one. The token is returned
// once; only its hash is stored.
func (s *Service) CreateInvite(ctx context.Context, userID, id uuid.UUID) (Invite, error) {
	var raw [16]byte // 128 random bits
	if _, err := rand.Read(raw[:]); err != nil {
		return Invite{}, fmt.Errorf("buckets: token: %w", err)
	}
	token := tokenEncoding.EncodeToString(raw[:])
	tx, err := s.begin(ctx)
	if err != nil {
		return Invite{}, err
	}
	defer tx.Rollback(ctx)
	role, personal, _, err := lock(ctx, tx, userID, id)
	if err != nil {
		return Invite{}, err
	}
	if err := ownerOf(role, personal); err != nil {
		return Invite{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bucket_invites SET revoked_at = now() WHERE bucket_id = $1 AND revoked_at IS NULL`, id); err != nil {
		return Invite{}, fmt.Errorf("buckets: revoke: %w", err)
	}
	var exp time.Time
	if err := tx.QueryRow(ctx, `
INSERT INTO bucket_invites (bucket_id, token_hash, created_by, expires_at)
VALUES ($1, $2, $3, now() + make_interval(secs => $4)) RETURNING expires_at`,
		id, hashToken(token), userID, InviteTTL.Seconds()).Scan(&exp); err != nil {
		return Invite{}, fmt.Errorf("buckets: invite: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invite{}, fmt.Errorf("buckets: commit: %w", err)
	}
	return Invite{Token: token, ExpiresAt: exp}, nil
}

// RevokeInvite stops the bucket's active invite, if any.
func (s *Service) RevokeInvite(ctx context.Context, userID, id uuid.UUID) error {
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, personal, _, err := lock(ctx, tx, userID, id)
	if err != nil {
		return err
	}
	if err := ownerOf(role, personal); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE bucket_invites SET revoked_at = now() WHERE bucket_id = $1 AND revoked_at IS NULL`, id); err != nil {
		return fmt.Errorf("buckets: revoke: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("buckets: commit: %w", err)
	}
	return nil
}

// Preview describes the bucket behind an invite so the person can decide before joining.
func (s *Service) Preview(ctx context.Context, token string) (InvitePreview, error) {
	var p InvitePreview
	err := s.pool.QueryRow(ctx, `
SELECT b.name, NULLIF(COALESCE(NULLIF(trim(u.display_name), ''), split_part(u.email, '@', 1)), ''),
       (SELECT count(*) FROM bucket_members x WHERE x.bucket_id = b.id)
FROM bucket_invites i JOIN buckets b ON b.id = i.bucket_id LEFT JOIN users u ON u.id = i.created_by
WHERE i.token_hash = $1 AND i.revoked_at IS NULL AND i.expires_at > now()`, hashToken(token)).
		Scan(&p.BucketName, &p.InvitedBy, &p.MemberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvitePreview{}, ErrInviteGone
	}
	if err != nil {
		return InvitePreview{}, fmt.Errorf("buckets: preview: %w", err)
	}
	p.Full = p.MemberCount >= MaxMembers
	return p, nil
}

// Join adds the person to the bucket behind a valid invite.
func (s *Service) Join(ctx context.Context, userID uuid.UUID, token string) (Bucket, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Bucket{}, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
SELECT i.bucket_id FROM bucket_invites i
WHERE i.token_hash = $1 AND i.revoked_at IS NULL AND i.expires_at > now()`, hashToken(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Bucket{}, ErrInviteGone
	}
	if err != nil {
		return Bucket{}, fmt.Errorf("buckets: join: %w", err)
	}
	// Lock order: the person first (their limit), then the bucket (its size).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		return Bucket{}, fmt.Errorf("buckets: join lock user: %w", err)
	}
	var personal bool
	if err := tx.QueryRow(ctx, `SELECT personal FROM buckets WHERE id = $1 FOR UPDATE`, id).Scan(&personal); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Bucket{}, ErrInviteGone
		}
		return Bucket{}, fmt.Errorf("buckets: join lock bucket: %w", err)
	}
	var members, memberships int
	var already bool
	if err := tx.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM bucket_members WHERE bucket_id = $1),
  (SELECT count(*) FROM bucket_members WHERE user_id = $2),
  EXISTS (SELECT 1 FROM bucket_members WHERE bucket_id = $1 AND user_id = $2)`, id, userID).Scan(&members, &memberships, &already); err != nil {
		return Bucket{}, fmt.Errorf("buckets: join count: %w", err)
	}
	switch {
	case personal:
		return Bucket{}, ErrInviteGone
	case already:
		return Bucket{}, fmt.Errorf("%w: you are already in this bucket", ErrConflict)
	case members >= MaxMembers:
		return Bucket{}, fmt.Errorf("%w: this bucket is full (%d people)", ErrConflict, MaxMembers)
	case memberships >= MaxMemberships:
		return Bucket{}, fmt.Errorf("%w: you are in the maximum of %d buckets", ErrConflict, MaxMemberships)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bucket_members (bucket_id, user_id, role) VALUES ($1, $2, 'member')`, id, userID); err != nil {
		return Bucket{}, fmt.Errorf("buckets: join insert: %w", err)
	}
	b, err := getFor(ctx, tx, userID, id)
	if err != nil {
		return Bucket{}, fmt.Errorf("buckets: join read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Bucket{}, fmt.Errorf("buckets: commit: %w", err)
	}
	return b, nil
}
