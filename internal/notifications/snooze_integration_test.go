//go:build integration

package notifications_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/users"
)

// Snoozing a delivered reminder marks it read and schedules one copy that is sent later through the normal checks;
// it cannot go past the date the reminder is about, belongs to its owner only, and survives the dispatcher.
func TestSnoozeReminder(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sfx := uuid.NewString()
	st := users.NewStore(pool)
	owner, err := st.EnsureBySubject(ctx, "sn-"+sfx, "sn-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.EnsureBySubject(ctx, "sn2-"+sfx, "sn2-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, stranger}) })
	if _, err := pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, timezone)
		VALUES ($1, true, '[30,14,7,3,1]', 'in_app', 'Asia/Jakarta')`, owner); err != nil {
		t.Fatal(err)
	}

	// warranty ends 2026-11-30; the 7-day reminder (11-23) has been delivered
	var pid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
		VALUES ($1,'Fridge','2026-01-01','2026-11-30') RETURNING id`, owner).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	delivered := func(offset int, day string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, status, sent_at, idempotency_key,
			channel, offset_days, warranty_end_date, kind)
			VALUES ($1,$2,'Garansi segera berakhir','x','sent', now(), $3,'in_app',$4,'2026-11-30','warranty') RETURNING id`,
			owner, pid, "seed:"+uuid.NewString()+day, offset).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	n7 := delivered(7, "a")

	clock := time.Date(2026, 11, 23, 10, 0, 0, 0, wib)
	svc := notifications.NewService(pool)
	svc.Now = func() time.Time { return clock }
	scheduledOf := func(original uuid.UUID) (n int, at time.Time, status string) {
		pool.QueryRow(ctx, `SELECT count(*), coalesce(max(scheduled_at), 'epoch'), coalesce(max(status), '') FROM notifications
			WHERE idempotency_key = 'snooze:' || $1::text`, original).Scan(&n, &at, &status)
		return
	}

	// someone else's reminder, an unknown id and a bad choice
	if _, err := svc.Snooze(ctx, stranger, n7, "tomorrow"); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("stranger: %v", err)
	}
	if _, err := svc.Snooze(ctx, owner, uuid.New(), "tomorrow"); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := svc.Snooze(ctx, owner, n7, "someday"); !errors.Is(err, notifications.ErrBadSnooze) {
		t.Fatalf("bad choice: %v", err)
	}
	if n, _, _ := scheduledOf(n7); n != 0 {
		t.Fatalf("a refused snooze left %d rows", n)
	}

	// tomorrow 09:00 WIB; the original is marked read
	at, err := svc.Snooze(ctx, owner, n7, "tomorrow")
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(time.Date(2026, 11, 24, 9, 0, 0, 0, wib)) {
		t.Fatalf("scheduled for %v", at)
	}
	var read bool
	pool.QueryRow(ctx, `SELECT read_at IS NOT NULL FROM notifications WHERE id = $1`, n7).Scan(&read)
	if !read {
		t.Fatal("the original must be marked read")
	}
	// snoozing again moves the copy, it does not add one
	if at, err = svc.Snooze(ctx, owner, n7, "in_3_days"); err != nil || !at.Equal(time.Date(2026, 11, 26, 9, 0, 0, 0, wib)) {
		t.Fatalf("second snooze: %v %v", at, err)
	}
	if n, got, status := scheduledOf(n7); n != 1 || status != "scheduled" || !got.Equal(at) {
		t.Fatalf("copies: %d status=%s at=%v", n, status, got)
	}
	// a week from 11-24 is 12-01, after the end of the warranty (11-30); a week from 11-23 would be the last day itself
	clock = time.Date(2026, 11, 24, 10, 0, 0, 0, wib)
	if _, err := svc.Snooze(ctx, owner, n7, "next_week"); !errors.Is(err, notifications.ErrSnoozeNotPossible) {
		t.Fatalf("past the end date: %v", err)
	}

	// the dispatcher delivers the copy when it is due, through the normal checks, into the inbox
	sch := &notifications.Scheduler{Pool: pool, Pushes: &recordingQueue{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 26, 9, 1, 0, 0, wib) }}
	if st, err := sch.Dispatch(ctx); err != nil || st.Sent < 1 {
		t.Fatalf("dispatch: %+v %v", st, err)
	}
	if _, _, status := scheduledOf(n7); status != "sent" {
		t.Fatalf("the copy should have been delivered, status=%q", status)
	}
	// the original has been snoozed and its copy delivered: it cannot be snoozed a third time
	if _, err := svc.Snooze(ctx, owner, n7, "tomorrow"); !errors.Is(err, notifications.ErrSnoozeNotPossible) {
		t.Fatalf("snoozing a reminder whose copy was delivered: %v", err)
	}

	// a copy whose warranty date changes before it is due is cancelled, not sent
	n3 := delivered(3, "b")
	clock = time.Date(2026, 11, 27, 10, 0, 0, 0, wib)
	if _, err := svc.Snooze(ctx, owner, n3, "tomorrow"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE purchases SET warranty_end_date = '2026-12-31' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	sch.Now = func() time.Time { return time.Date(2026, 11, 28, 9, 1, 0, 0, wib) }
	if _, err := sch.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, status := scheduledOf(n3); status != "cancelled" {
		t.Fatalf("a snoozed reminder for a moved date must be cancelled, status=%q", status)
	}
	// and once the date has changed the delivered reminder cannot be snoozed any more
	n1 := delivered(1, "c")
	if _, err := svc.Snooze(ctx, owner, n1, "tomorrow"); !errors.Is(err, notifications.ErrSnoozeNotPossible) {
		t.Fatalf("stale date: %v", err)
	}
}
