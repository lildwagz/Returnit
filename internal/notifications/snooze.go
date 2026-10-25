package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrBadSnooze: the requested delay is not one of the choices.
	ErrBadSnooze = errors.New("notifications: unknown snooze choice")
	// ErrSnoozeNotPossible: the reminder cannot be repeated (its purchase or date is gone or changed, the new time would
	// fall after the date it is about, or it was already snoozed and has been delivered again).
	ErrSnoozeNotPossible = errors.New("notifications: cannot be snoozed")
)

// SnoozeChoices are the delays a reminder can be pushed back by, in days.
var SnoozeChoices = map[string]int{"tomorrow": 1, "in_3_days": 3, "next_week": 7}

// SnoozeTarget returns when a snoozed reminder fires: ReminderHour in the user timezone, that many days from now,
// moved to the end of the quiet hours if it falls inside them.
func SnoozeTarget(choice string, now time.Time, loc *time.Location, qs, qe *string) (time.Time, error) {
	days, ok := SnoozeChoices[choice]
	if !ok {
		return time.Time{}, ErrBadSnooze
	}
	ln := now.In(loc)
	t := time.Date(ln.Year(), ln.Month(), ln.Day()+days, ReminderHour, 0, 0, 0, loc)
	return AdjustForQuietHours(t, loc, qs, qe), nil
}

// snoozeOrder is the order the choices are offered in.
var snoozeOrder = []string{"tomorrow", "in_3_days", "next_week"}

// beforeEnd reports whether a reminder at target still falls on or before the end date it is about.
func beforeEnd(target time.Time, loc *time.Location, end time.Time) bool {
	lt := target.In(loc)
	return !time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.UTC).After(end)
}

// SnoozeOptions are the choices that make sense for a reminder about end: the ones whose time is not after that date
// (a warranty that ends tomorrow cannot be reminded again in a week). Empty when none is left.
func SnoozeOptions(now time.Time, loc *time.Location, qs, qe *string, end *time.Time) []string {
	out := []string{}
	if end == nil {
		return out
	}
	for _, c := range snoozeOrder {
		if t, err := SnoozeTarget(c, now, loc, qs, qe); err == nil && beforeEnd(t, loc, *end) {
			out = append(out, c)
		}
	}
	return out
}

// Snooze repeats a delivered reminder later ("remind me again tomorrow"): the original is marked read and a copy is
// scheduled, which goes through the same checks as every reminder right before it is sent (a changed date or
// switched-off reminders cancel it). Snoozing the same reminder again, while the copy is still waiting, moves it.
// It returns the time the copy is scheduled for.
func (s *Service) Snooze(ctx context.Context, userID, id uuid.UUID, choice string) (time.Time, error) {
	if _, ok := SnoozeChoices[choice]; !ok {
		return time.Time{}, ErrBadSnooze
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("notifications: snooze begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		purchaseID             *uuid.UUID
		kind, title, body, tz  string
		channel                *string
		offset                 *int
		plannedEnd, currentEnd *time.Time
		quietStart, quietEnd   *string
	)
	err = tx.QueryRow(ctx, `
SELECT n.purchase_id, n.kind, n.channel, n.title, n.body, n.offset_days, n.warranty_end_date,
       CASE WHEN n.kind = 'return' THEN p.return_deadline ELSE p.warranty_end_date END,
       COALESCE(rp.timezone, 'Asia/Jakarta'), to_char(rp.quiet_hours_start, 'HH24:MI'), to_char(rp.quiet_hours_end, 'HH24:MI')
FROM notifications n
LEFT JOIN purchases p ON p.id = n.purchase_id
LEFT JOIN reminder_preferences rp ON rp.user_id = n.user_id
WHERE n.id = $1 AND n.user_id = $2 AND n.status = 'sent'
FOR UPDATE OF n`, id, userID).Scan(&purchaseID, &kind, &channel, &title, &body, &offset, &plannedEnd, &currentEnd, &tz, &quietStart, &quietEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("notifications: snooze load: %w", err)
	}
	if purchaseID == nil || offset == nil || plannedEnd == nil || currentEnd == nil || !currentEnd.Equal(*plannedEnd) {
		return time.Time{}, ErrSnoozeNotPossible
	}

	loc := loadLocation(tz)
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	target, err := SnoozeTarget(choice, now, loc, quietStart, quietEnd)
	if err != nil {
		return time.Time{}, err
	}
	if !beforeEnd(target, loc, *currentEnd) {
		return time.Time{}, ErrSnoozeNotPossible // it would fire after the date it is about
	}

	var scheduled time.Time
	err = tx.QueryRow(ctx, `
INSERT INTO notifications (user_id, purchase_id, title, body, scheduled_at, status, idempotency_key,
                           channel, offset_days, warranty_end_date, kind)
VALUES ($1,$2,$3,$4,$5,'scheduled','snooze:' || $6::text,$7,$8,$9,$10)
ON CONFLICT (idempotency_key) DO UPDATE SET scheduled_at = EXCLUDED.scheduled_at
WHERE notifications.status = 'scheduled'
RETURNING scheduled_at`, userID, purchaseID, title, body, target.UTC(), id, channel, offset, plannedEnd, kind).Scan(&scheduled)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrSnoozeNotPossible // already snoozed once and the copy has been delivered
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("notifications: snooze insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, now()) WHERE id = $1`, id); err != nil {
		return time.Time{}, fmt.Errorf("notifications: snooze mark read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, fmt.Errorf("notifications: snooze commit: %w", err)
	}
	return scheduled, nil
}
