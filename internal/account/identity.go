package account

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/identity"
)

const identityMaxAge = 30 * 24 * time.Hour

func queueIdentityDeletion(ctx context.Context, tx pgx.Tx, subject *string) error {
	if subject == nil || *subject == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_deletions (subject) VALUES ($1) ON CONFLICT (subject) DO NOTHING`, *subject); err != nil {
		return fmt.Errorf("account: queue identity deletion: %w", err)
	}
	return nil
}

// IdentityEraser removes the login identity at the identity provider after an account is erased,
// so no profile lingers there and the old login does not silently work again.
type IdentityEraser struct {
	Pool     *pgxpool.Pool
	Provider identity.Provider
	Log      *slog.Logger
	Now      func() time.Time // nil means time.Now
}

func (e *IdentityEraser) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// EraseDue deletes the identities that are due and returns how many were removed.
func (e *IdentityEraser) EraseDue(ctx context.Context) (int, error) {
	now := e.now()
	rows, err := e.Pool.Query(ctx, `
UPDATE identity_deletions SET next_attempt_at = $2, attempts = attempts + 1
WHERE subject IN (SELECT subject FROM identity_deletions WHERE next_attempt_at <= $1 ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
RETURNING subject, attempts, created_at`, now, now.Add(emailLease), emailBatch)
	if err != nil {
		return 0, fmt.Errorf("account: claim identity deletions: %w", err)
	}
	type item struct {
		subject  string
		attempts int
		created  time.Time
	}
	var list []item
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.subject, &i.attempts, &i.created); err != nil {
			rows.Close()
			return 0, fmt.Errorf("account: scan identity deletion: %w", err)
		}
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("account: identity rows: %w", err)
	}

	done := 0
	for _, i := range list {
		err := e.Provider.DeleteUser(ctx, i.subject)
		switch {
		case err == nil:
			if _, derr := e.Pool.Exec(ctx, `DELETE FROM identity_deletions WHERE subject = $1`, i.subject); derr != nil {
				return done, derr
			}
			done++
		case now.Sub(i.created) > identityMaxAge:
			e.Log.Error("identity deletion abandoned after 30 days of failures: delete the user at the identity provider by hand", "err", err)
			if _, derr := e.Pool.Exec(ctx, `DELETE FROM identity_deletions WHERE subject = $1`, i.subject); derr != nil {
				return done, derr
			}
		default:
			backoff := emailLease << min(i.attempts, 8)
			if backoff > emailMaxBack {
				backoff = emailMaxBack
			}
			e.Log.Warn("identity deletion failed, will retry", "attempt", i.attempts, "retry_in", backoff.String(), "err", err)
			if _, derr := e.Pool.Exec(ctx, `UPDATE identity_deletions SET next_attempt_at = $2, last_error = $3 WHERE subject = $1`,
				i.subject, now.Add(backoff), strings.ToValidUTF8(truncate(err.Error(), 200), "")); derr != nil {
				return done, derr
			}
		}
	}
	return done, nil
}
