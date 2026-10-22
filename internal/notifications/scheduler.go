package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PushEnqueuer hands external (push/email) delivery of an already-delivered in-app notification
// to the job queue, which owns retries. *queue.Client implements it.
type PushEnqueuer interface {
	EnqueuePush(ctx context.Context, notificationID uuid.UUID) error
}

// Scheduler plans and dispatches reminders. It is driven by the worker's periodic jobs; both
// methods are safe to run concurrently in several processes.
type Scheduler struct {
	Pool   *pgxpool.Pool
	Pushes PushEnqueuer
	Log    *slog.Logger
	Now    func() time.Time // injectable clock; defaults to time.Now
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const planBatch = 500

// Plan creates notification rows for upcoming reminders. Running it repeatedly (or from several
// processes) never creates duplicates: rows are keyed by purchase + reminder date + offset (not the
// channel) with ON CONFLICT DO NOTHING.
func (s *Scheduler) Plan(ctx context.Context) (int, error) {
	now := s.now()
	created := 0
	last := uuid.Nil
	for {
		rows, err := s.Pool.Query(ctx, `
SELECT p.id, p.user_id, p.product_name, p.warranty_end_date,
       COALESCE(rp.enabled, true),
       COALESCE(rp.days_before, '[30,14,7,3,1]'::jsonb),
       COALESCE(rp.channel, 'push'),
       to_char(rp.quiet_hours_start, 'HH24:MI'), to_char(rp.quiet_hours_end, 'HH24:MI'),
       COALESCE(rp.timezone, 'Asia/Jakarta')
FROM purchases p
JOIN users u ON u.id = p.user_id AND u.deletion_scheduled_at IS NULL
LEFT JOIN reminder_preferences rp ON rp.user_id = p.user_id
WHERE p.warranty_end_date IS NOT NULL
  AND p.warranty_end_date BETWEEN ($1::date - 3) AND ($1::date + 35)
  AND p.id > $2
ORDER BY p.id LIMIT $3`, now, last, planBatch)
		if err != nil {
			return created, fmt.Errorf("notifications: plan query: %w", err)
		}
		var batch []plannedRow
		for rows.Next() {
			var c Candidate
			var pid, uid uuid.UUID
			var raw []byte
			if err := rows.Scan(&pid, &uid, &c.ProductName, &c.End, &c.Prefs.Enabled, &raw,
				&c.Prefs.Channel, &c.Prefs.QuietStart, &c.Prefs.QuietEnd, &c.Prefs.Timezone); err != nil {
				rows.Close()
				return created, fmt.Errorf("notifications: plan scan: %w", err)
			}
			last = pid // advance the keyset even if this row is skipped below
			if err := json.Unmarshal(raw, &c.Prefs.DaysBefore); err != nil {
				s.Log.Error("skipping purchase with invalid days_before", "purchase_id", pid)
				continue
			}
			c.PurchaseID, c.UserID = pid.String(), uid.String()
			batch = append(batch, plannedRow{pid, uid, c})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return created, fmt.Errorf("notifications: plan rows: %w", err)
		}
		scanned := len(batch)
		for _, b := range batch {
			for _, p := range Plan(b.c, now) {
				tag, err := s.Pool.Exec(ctx, `
INSERT INTO notifications (user_id, purchase_id, title, body, scheduled_at, status,
                           idempotency_key, channel, offset_days, warranty_end_date)
VALUES ($1,$2,$3,$4,$5,'scheduled',$6,$7,$8,$9)
ON CONFLICT (idempotency_key) DO NOTHING`,
					b.uid, b.pid, p.Title, p.Body, p.ScheduledAt, p.Key, p.Channel, p.OffsetDays, b.c.End)
				if err != nil {
					return created, fmt.Errorf("notifications: plan insert: %w", err)
				}
				created += int(tag.RowsAffected())
			}
		}
		if scanned < planBatch && last != uuid.Nil {
			// A short page means we reached the end (skipped rows only make it shorter).
			return created, nil
		}
		if scanned == 0 {
			return created, nil
		}
	}
}

type plannedRow struct {
	pid, uid uuid.UUID
	c        Candidate
}

// DispatchStats counts outcomes of one Dispatch run.
type DispatchStats struct{ Sent, Cancelled, Deferred int }

const dispatchBatch = 100

type dueRow struct {
	id      uuid.UUID
	channel string
	due     Due
}

// Dispatch delivers due notifications to the in-app inbox after re-validating them against the
// current purchase and preferences. Rows are locked with FOR UPDATE SKIP LOCKED, so concurrent
// workers never deliver the same notification twice. For push/email channels it then asks the
// queue to send the external message; that is best-effort and retried by the queue, and a
// failure never un-delivers the in-app notification.
func (s *Scheduler) Dispatch(ctx context.Context) (DispatchStats, error) {
	var st DispatchStats
	now := s.now()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return st, fmt.Errorf("notifications: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT n.id, n.channel, n.offset_days, n.warranty_end_date,
       p.warranty_end_date,
       COALESCE(rp.enabled, true),
       COALESCE(rp.days_before, '[30,14,7,3,1]'::jsonb),
       COALESCE(rp.channel, 'push'),
       to_char(rp.quiet_hours_start, 'HH24:MI'), to_char(rp.quiet_hours_end, 'HH24:MI'),
       COALESCE(rp.timezone, 'Asia/Jakarta'),
       u.deletion_scheduled_at IS NOT NULL
FROM notifications n
LEFT JOIN users u ON u.id = n.user_id
LEFT JOIN purchases p ON p.id = n.purchase_id
LEFT JOIN reminder_preferences rp ON rp.user_id = n.user_id
WHERE n.status = 'scheduled' AND n.offset_days IS NOT NULL AND n.scheduled_at <= $1
ORDER BY n.scheduled_at
LIMIT $2
FOR UPDATE OF n SKIP LOCKED`, now, dispatchBatch)
	if err != nil {
		return st, fmt.Errorf("notifications: select due: %w", err)
	}
	var due []dueRow
	for rows.Next() {
		var r dueRow
		var raw []byte
		var channel *string
		var offset *int
		var plannedEnd *time.Time
		if err := rows.Scan(&r.id, &channel, &offset, &plannedEnd,
			&r.due.CurrentEnd, &r.due.Prefs.Enabled, &raw, &r.due.Prefs.Channel,
			&r.due.Prefs.QuietStart, &r.due.Prefs.QuietEnd, &r.due.Prefs.Timezone, &r.due.AccountPending); err != nil {
			rows.Close()
			return st, fmt.Errorf("notifications: scan due: %w", err)
		}
		if channel == nil || offset == nil || plannedEnd == nil {
			continue // not a scheduler-managed row
		}
		r.channel = *channel
		r.due.Channel, r.due.OffsetDays, r.due.PlannedEnd = *channel, *offset, *plannedEnd
		if err := json.Unmarshal(raw, &r.due.Prefs.DaysBefore); err != nil {
			continue
		}
		due = append(due, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("notifications: due rows: %w", err)
	}

	var push []uuid.UUID
	for _, r := range due {
		action, reason, until := Decide(r.due, now)
		switch action {
		case ActionCancel:
			err = exec(ctx, tx, `UPDATE notifications SET status='cancelled', last_error=$2 WHERE id=$1`, r.id, reason)
			st.Cancelled++
		case ActionDefer:
			err = exec(ctx, tx, `UPDATE notifications SET scheduled_at=$2 WHERE id=$1`, r.id, until)
			st.Deferred++
		case ActionSend:
			// Deliver through the channel the user has NOW, not the one that was current when it was planned.
			ch := r.due.Prefs.Channel
			err = exec(ctx, tx, `UPDATE notifications SET status='sent', sent_at=$2, channel=$3, last_error=NULL WHERE id=$1`, r.id, now, ch)
			st.Sent++
			if ch != "in_app" {
				push = append(push, r.id)
			}
		}
		if err != nil {
			return DispatchStats{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DispatchStats{}, fmt.Errorf("notifications: commit: %w", err)
	}

	// Enqueue only after commit so a worker never sees a notification that is not yet 'sent'.
	for _, id := range push {
		if err := s.Pushes.EnqueuePush(ctx, id); err != nil {
			s.Log.Error("could not enqueue push delivery; in-app notification was delivered", "notification_id", id, "err", err)
		}
	}
	return st, nil
}

func exec(ctx context.Context, tx pgx.Tx, sql string, args ...any) error {
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("notifications: update: %w", err)
	}
	return nil
}
