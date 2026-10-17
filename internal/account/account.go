// Package account implements the account profile and the deletion lifecycle:
// request (scheduled after a grace period) -> optional cancel -> hard delete by the worker.
package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("account: not found")
	// ErrTooLate means the grace period has ended and erasure is imminent or in progress.
	ErrTooLate = errors.New("account: deletion can no longer be cancelled")
)

type Account struct {
	ID                   uuid.UUID  `json:"id"`
	Email                string     `json:"email"`
	DisplayName          *string    `json:"displayName"`
	CreatedAt            time.Time  `json:"createdAt"`
	DeletionScheduledFor *time.Time `json:"deletionScheduledFor"`
}

type Service struct {
	pool  *pgxpool.Pool
	grace time.Duration
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func NewService(pool *pgxpool.Pool, grace time.Duration) *Service {
	return &Service{pool: pool, grace: grace}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `
SELECT id, email, display_name, created_at, deletion_scheduled_at FROM users WHERE id = $1`, userID).
		Scan(&a.ID, &a.Email, &a.DisplayName, &a.CreatedAt, &a.DeletionScheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: get: %w", err)
	}
	return a, nil
}

// reminderLead is how long before erasure the "erasure tomorrow" e-mail goes out. It is only
// sent when the grace period is comfortably longer, so it never duplicates the first e-mail.
const reminderLead = 24 * time.Hour

// RequestDeletion schedules erasure grace after now. It is idempotent: asking again keeps the
// original schedule, so repeated requests can never push the date out (or pull it in), and the
// notice e-mails are queued only by the call that actually creates the schedule.
func (s *Service) RequestDeletion(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("account: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		email    string
		existing *time.Time
		tz       string
	)
	err = tx.QueryRow(ctx, `
SELECT u.email, u.deletion_scheduled_at, COALESCE(p.timezone, 'Asia/Jakarta')
FROM users u LEFT JOIN reminder_preferences p ON p.user_id = u.id
WHERE u.id = $1 FOR UPDATE OF u`, userID).Scan(&email, &existing, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("account: request deletion: %w", err)
	}
	if existing != nil {
		return *existing, nil
	}
	at := now.Add(s.grace)
	if _, err := tx.Exec(ctx, `
UPDATE users SET deletion_requested_at = $2, deletion_scheduled_at = $3, updated_at = now() WHERE id = $1`,
		userID, now, at); err != nil {
		return time.Time{}, fmt.Errorf("account: request deletion: %w", err)
	}
	if err := queueEmail(ctx, tx, userID, KindRequested, email, tz, at, now); err != nil {
		return time.Time{}, err
	}
	if s.grace > reminderLead+time.Hour {
		if err := queueEmail(ctx, tx, userID, KindReminder, email, tz, at, at.Add(-reminderLead)); err != nil {
			return time.Time{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, fmt.Errorf("account: commit: %w", err)
	}
	return at, nil
}

// CancelDeletion clears a pending deletion. It is idempotent when nothing is pending, and
// returns ErrTooLate once the scheduled time has passed. Notice e-mails not yet sent are dropped.
func (s *Service) CancelDeletion(ctx context.Context, userID uuid.UUID) error {
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("account: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
UPDATE users SET deletion_requested_at = NULL, deletion_scheduled_at = NULL, updated_at = now()
WHERE id = $1 AND deletion_scheduled_at > $2`, userID, now)
	if err != nil {
		return fmt.Errorf("account: cancel deletion: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `DELETE FROM account_emails WHERE user_id = $1 AND kind IN ('requested','reminder')`, userID); err != nil {
			return fmt.Errorf("account: cancel emails: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("account: commit: %w", err)
		}
		return nil
	}
	var scheduled *time.Time
	err = tx.QueryRow(ctx, `SELECT deletion_scheduled_at FROM users WHERE id = $1`, userID).Scan(&scheduled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("account: cancel deletion: %w", err)
	case scheduled == nil:
		return nil // nothing pending
	default:
		return ErrTooLate
	}
}

const purgeBatch = 50

// PurgeDue hard-deletes accounts whose grace period has ended and returns how many were erased.
// Rows are locked with SKIP LOCKED so several workers can run it. Everything the user owns goes
// with the row (ON DELETE CASCADE); receipt files are queued for deletion by a database trigger
// in the same transaction, so a crash cannot strand them. Only the internal UUID and timestamps
// are kept, in account_deletion_log.
func (s *Service) PurgeDue(ctx context.Context) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("account: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT u.id, u.deletion_requested_at, u.deletion_scheduled_at, u.email, COALESCE(p.timezone, 'Asia/Jakarta'), u.identity_subject
FROM users u LEFT JOIN reminder_preferences p ON p.user_id = u.id
WHERE u.deletion_scheduled_at <= $1
ORDER BY u.deletion_scheduled_at LIMIT $2 FOR UPDATE OF u SKIP LOCKED`, s.now(), purgeBatch)
	if err != nil {
		return 0, fmt.Errorf("account: select due: %w", err)
	}
	type due struct {
		id               uuid.UUID
		requested, sched time.Time
		email, tz        string
		subject          *string
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.requested, &d.sched, &d.email, &d.tz, &d.subject); err != nil {
			rows.Close()
			return 0, fmt.Errorf("account: scan: %w", err)
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("account: rows: %w", err)
	}

	for _, d := range list {
		if _, err := tx.Exec(ctx, `
INSERT INTO account_deletion_log (user_id, requested_at, deleted_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO NOTHING`, d.id, d.requested, s.now()); err != nil {
			return 0, fmt.Errorf("account: log deletion: %w", err)
		}
		// Queued before the user row disappears; this is the only copy of the address left.
		if err := queueEmail(ctx, tx, d.id, KindErased, d.email, d.tz, d.sched, s.now()); err != nil {
			return 0, err
		}
		if err := queueIdentityDeletion(ctx, tx, d.subject); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, d.id); err != nil {
			return 0, fmt.Errorf("account: delete user: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("account: commit: %w", err)
	}
	return len(list), nil
}
