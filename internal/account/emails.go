package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/mailer"
)

// E-mail kinds sent around account deletion.
const (
	KindRequested = "requested" // right after the request
	KindReminder  = "reminder"  // 24 hours before erasure
	KindErased    = "erased"    // after erasure
)

const (
	emailBatch   = 20
	emailMaxAge  = 7 * 24 * time.Hour // give up (and drop the address) after this long
	emailLease   = 5 * time.Minute    // a claimed row is not retried by others for this long
	emailMaxBack = 6 * time.Hour
)

func queueEmail(ctx context.Context, tx pgx.Tx, user uuid.UUID, kind, to, tz string, erasureAt, sendAt time.Time) error {
	if !mailer.ValidAddress(to) {
		return nil // nothing sensible to send to; deletion itself must not fail because of this
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO account_emails (user_id, kind, to_address, timezone, erasure_at, next_attempt_at)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (user_id, kind, erasure_at) DO NOTHING`,
		user, kind, to, tz, erasureAt, sendAt); err != nil {
		return fmt.Errorf("account: queue %s e-mail: %w", kind, err)
	}
	return nil
}

// EmailSender delivers the queued deletion notices. Run it periodically; it is safe to run in
// several workers at once.
type EmailSender struct {
	Pool   *pgxpool.Pool
	Mailer mailer.Mailer
	Log    *slog.Logger
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (e *EmailSender) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

type queued struct {
	id        uuid.UUID
	user      uuid.UUID
	kind, to  string
	tz        string
	erasureAt time.Time
	attempts  int
	created   time.Time
}

// SendDue sends every notice that is due and returns how many were sent. A failing message is
// retried with exponential backoff; one that is still failing after a week is dropped (together
// with the address) and reported at ERROR level.
func (e *EmailSender) SendDue(ctx context.Context) (int, error) {
	now := e.now()
	// Claim by leasing: other workers skip these rows until the lease expires, and no database
	// transaction stays open while talking to the mail server.
	rows, err := e.Pool.Query(ctx, `
UPDATE account_emails SET next_attempt_at = $2, attempts = attempts + 1
WHERE id IN (SELECT id FROM account_emails WHERE next_attempt_at <= $1 ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
RETURNING id, user_id, kind, to_address, timezone, erasure_at, attempts, created_at`,
		now, now.Add(emailLease), emailBatch)
	if err != nil {
		return 0, fmt.Errorf("account: claim e-mails: %w", err)
	}
	var list []queued
	for rows.Next() {
		var q queued
		if err := rows.Scan(&q.id, &q.user, &q.kind, &q.to, &q.tz, &q.erasureAt, &q.attempts, &q.created); err != nil {
			rows.Close()
			return 0, fmt.Errorf("account: scan e-mail: %w", err)
		}
		list = append(list, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("account: e-mail rows: %w", err)
	}

	sent := 0
	for _, q := range list {
		ok, err := e.deliver(ctx, q, now)
		if ok {
			sent++
		}
		if err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// deliver handles one claimed message. Only database errors are returned; send failures are
// recorded on the row.
func (e *EmailSender) deliver(ctx context.Context, q queued, now time.Time) (bool, error) {
	drop := func(reason string) (bool, error) {
		e.Log.Info("account e-mail dropped", "email_id", q.id, "kind", q.kind, "reason", reason)
		_, err := e.Pool.Exec(ctx, `DELETE FROM account_emails WHERE id = $1`, q.id)
		return false, err
	}
	if q.kind != KindErased {
		// The notice is only true while this exact erasure is still scheduled.
		var still bool
		err := e.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deletion_scheduled_at = $2)`,
			q.user, q.erasureAt).Scan(&still)
		if err != nil {
			return false, fmt.Errorf("account: recheck e-mail: %w", err)
		}
		if !still {
			return drop("deletion no longer scheduled")
		}
		if q.kind == KindReminder && !q.erasureAt.After(now) {
			return drop("erasure date passed")
		}
	}
	subject, body := Compose(q.kind, q.erasureAt, q.tz)
	err := e.Mailer.Send(ctx, mailer.Message{To: q.to, Subject: subject, Body: body})
	if err == nil {
		e.Log.Info("account e-mail sent", "email_id", q.id, "kind", q.kind)
		_, derr := e.Pool.Exec(ctx, `DELETE FROM account_emails WHERE id = $1`, q.id)
		return true, derr
	}
	if errors.Is(err, mailer.ErrPermanent) {
		e.Log.Error("account e-mail rejected", "email_id", q.id, "kind", q.kind, "err", err)
		return drop("rejected by server")
	}
	if now.Sub(q.created) > emailMaxAge {
		e.Log.Error("account e-mail abandoned after a week of failures", "email_id", q.id, "kind", q.kind, "err", err)
		return drop("too old")
	}
	backoff := emailLease << min(q.attempts, 8)
	if backoff > emailMaxBack {
		backoff = emailMaxBack
	}
	e.Log.Warn("account e-mail failed, will retry", "email_id", q.id, "kind", q.kind, "attempt", q.attempts, "retry_in", backoff.String(), "err", err)
	_, derr := e.Pool.Exec(ctx, `UPDATE account_emails SET next_attempt_at = $2, last_error = $3 WHERE id = $1`,
		q.id, now.Add(backoff), strings.ToValidUTF8(truncate(err.Error(), 200), ""))
	return false, derr
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Compose renders a notice. Dates are shown in the user's own time zone.
func Compose(kind string, erasureAt time.Time, tz string) (subject, body string) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation("Asia/Jakarta")
	}
	when := erasureAt.In(loc).Format("2 January 2006, 15:04 MST")
	const sig = "\nThis is an automatic message from ReturnIt. Please do not reply."
	switch kind {
	case KindRequested:
		return "Your ReturnIt account will be deleted",
			"We received a request to delete your ReturnIt account.\n\n" +
				"Your account and everything in it (purchases, receipts, reminders and notifications) will be permanently erased on " + when + ".\n\n" +
				"Changed your mind? Open the app and cancel the deletion before that time. After it, nothing can be restored.\n\n" +
				"If you did not ask for this, cancel the deletion in the app right away and change your password at your login provider.\n" + sig
	case KindReminder:
		return "Your ReturnIt account will be erased tomorrow",
			"Your ReturnIt account is scheduled to be permanently erased on " + when + ".\n\n" +
				"To keep it, open the app and cancel the deletion before then. After that nothing can be restored.\n" + sig
	default:
		return "Your ReturnIt account has been erased",
			"As requested, your ReturnIt account and all its data (purchases, receipts, reminders and notifications) have been permanently erased.\n\n" +
				"Backups are overwritten within the retention period described in our data retention policy. Nothing more is needed from you.\n" + sig
	}
}
