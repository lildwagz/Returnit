//go:build integration

package notifications_test

import (
	"context"
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

// A return deadline gets its own reminders (3 and 1 day before), even for a purchase without a warranty end date;
// they are re-checked before sending (a changed deadline cancels them), and they never collide with the warranty
// reminders of the same date.
func TestReturnDeadlineReminders(t *testing.T) {
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
	owner, err := users.NewStore(pool).EnsureBySubject(ctx, "rr-"+sfx, "rr-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, owner) })
	if _, err := pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, timezone)
		VALUES ($1, true, '[30,14,7,3,1]', 'in_app', 'Asia/Jakarta')`, owner); err != nil {
		t.Fatal(err)
	}

	clock := time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	sch := &notifications.Scheduler{Pool: pool, Pushes: &recordingQueue{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock }}
	count := func(pid uuid.UUID, where string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE purchase_id = $1 `+where, pid).Scan(&n)
		return
	}

	// --- no warranty at all, only a return deadline on 2026-11-23: the "3 days" reminder is due 2026-11-20 09:00 ---
	var onlyReturn uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, return_deadline)
		VALUES ($1,'Shoes','2026-11-10','2026-11-23') RETURNING id`, owner).Scan(&onlyReturn); err != nil {
		t.Fatal(err)
	}
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(onlyReturn, `AND kind = 'return' AND offset_days = 3 AND status = 'scheduled' AND idempotency_key LIKE 'return:%'`); n != 1 {
		t.Fatalf("want one planned return reminder, got %d", n)
	}
	if n := count(onlyReturn, `AND kind <> 'return'`); n != 0 {
		t.Fatalf("a purchase without a warranty must get no warranty reminders, got %d", n)
	}
	// planning again changes nothing
	sch.Plan(ctx)
	if n := count(onlyReturn, ``); n != 1 {
		t.Fatalf("planning twice created %d rows, want 1", n)
	}
	clock = time.Date(2026, 11, 20, 9, 1, 0, 0, wib)
	if st, err := sch.Dispatch(ctx); err != nil || st.Sent < 1 {
		t.Fatalf("dispatch: %+v %v", st, err)
	}
	var title string
	if err := pool.QueryRow(ctx, `SELECT title FROM notifications WHERE purchase_id = $1 AND status = 'sent'`, onlyReturn).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Batas pengembalian segera berakhir" {
		t.Fatalf("title %q", title)
	}

	// --- the deadline moves after the 1-day reminder was planned: that reminder is cancelled, not sent ---
	clock = time.Date(2026, 11, 21, 8, 0, 0, 0, wib)
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(onlyReturn, `AND offset_days = 1 AND status = 'scheduled'`); n != 1 {
		t.Fatalf("want the 1-day reminder planned, got %d", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE purchases SET return_deadline = '2026-12-05' WHERE id = $1`, onlyReturn); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 11, 22, 9, 1, 0, 0, wib)
	if _, err := sch.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(onlyReturn, `AND offset_days = 1 AND status = 'cancelled'`); n != 1 {
		t.Fatalf("a moved deadline must cancel the planned reminder, got %d cancelled", n)
	}

	// --- warranty and return deadline on the same day: both 1-day reminders exist, side by side ---
	clock = time.Date(2026, 11, 29, 6, 0, 0, 0, wib)
	var both uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date, return_deadline)
		VALUES ($1,'Tablet','2026-11-01','2026-11-30','2026-11-30') RETURNING id`, owner).Scan(&both); err != nil {
		t.Fatal(err)
	}
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if w, r := count(both, `AND kind = 'warranty' AND offset_days = 1`), count(both, `AND kind = 'return' AND offset_days = 1`); w != 1 || r != 1 {
		t.Fatalf("want one warranty and one return reminder for the same day, got %d and %d", w, r)
	}
}
