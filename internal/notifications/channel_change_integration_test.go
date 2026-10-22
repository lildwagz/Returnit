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

// Switching the reminder channel (push <-> in_app) must not create a second reminder for the same
// purchase/date/offset, and must not lose the read state of the one already delivered.
func TestChannelChangeDoesNotDuplicateReminders(t *testing.T) {
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
	owner, err := users.NewStore(pool).EnsureBySubject(ctx, "cc-"+sfx, "cc-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, owner) })

	setChannel := func(ch string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, timezone)
			VALUES ($1, true, '[30,14,7,3,1]', $2, 'Asia/Jakarta')
			ON CONFLICT (user_id) DO UPDATE SET channel = EXCLUDED.channel`, owner, ch); err != nil {
			t.Fatal(err)
		}
	}
	newPurchase := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
			VALUES ($1,$2,'2026-01-01','2026-11-23') RETURNING id`, owner, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	rows := func(pid uuid.UUID, where string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE purchase_id = $1 `+where, pid).Scan(&n)
		return
	}

	// Warranty ends 2026-11-23, so the "3 days" reminder is due 2026-11-20 09:00 WIB.
	clock := time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	queue := &recordingQueue{}
	sch := &notifications.Scheduler{Pool: pool, Pushes: queue, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock }}

	// --- Case 1: the reminder was already delivered (and read), then the channel changes ---
	delivered := newPurchase("Laptop")
	setChannel("push")
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 11, 20, 9, 1, 0, 0, wib)
	if st, err := sch.Dispatch(ctx); err != nil || st.Sent < 1 {
		t.Fatalf("dispatch: %+v %v", st, err)
	}
	if rows(delivered, `AND status = 'sent'`) != 1 {
		t.Fatalf("want exactly one delivered reminder, got %d", rows(delivered, `AND status = 'sent'`))
	}
	if _, err := pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE purchase_id = $1`, delivered); err != nil {
		t.Fatal(err)
	}

	for _, ch := range []string{"in_app", "push", "email", "in_app"} {
		setChannel(ch)
		if _, err := sch.Plan(ctx); err != nil { // still inside the 24 h catch-up window
			t.Fatal(err)
		}
		if _, err := sch.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		// (the "1 day" reminder is legitimately planned later in this window; only the "3 days" one is under test)
		if n := rows(delivered, `AND offset_days = 3`); n != 1 {
			t.Fatalf("after switching to %s there are %d \"3 days\" reminders for the purchase, want 1", ch, n)
		}
		if n := rows(delivered, `AND offset_days = 3 AND status = 'sent' AND read_at IS NOT NULL`); n != 1 {
			t.Fatalf("after switching to %s the delivered reminder lost its read state", ch)
		}
	}

	// --- Case 2: the channel changes BEFORE the reminder is due: it is sent once, through the new channel ---
	clock = time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	pending := newPurchase("Phone")
	setChannel("push")
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if rows(pending, `AND status = 'scheduled'`) != 1 {
		t.Fatalf("want one planned reminder, got %d", rows(pending, `AND status = 'scheduled'`))
	}
	setChannel("in_app")
	sch.Plan(ctx)
	if n := rows(pending, ``); n != 1 {
		t.Fatalf("switching channel before it is due created %d rows, want 1", n)
	}
	pushesBefore := queue.count()
	clock = time.Date(2026, 11, 20, 9, 1, 0, 0, wib)
	if _, err := sch.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var status, channel string
	if err := pool.QueryRow(ctx, `SELECT status, channel FROM notifications WHERE purchase_id = $1`, pending).Scan(&status, &channel); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || channel != "in_app" {
		t.Fatalf("want sent via in_app, got %s via %s", status, channel)
	}
	if queue.count() != pushesBefore {
		t.Fatal("an in_app reminder must not queue a push job")
	}
}
