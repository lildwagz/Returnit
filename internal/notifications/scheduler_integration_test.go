//go:build integration

package notifications_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/users"
)

var wib = time.FixedZone("WIB", 7*3600)

// recordingQueue captures push jobs the dispatcher would put on the queue.
type recordingQueue struct {
	mu  sync.Mutex
	ids []uuid.UUID
	err error
}

func (r *recordingQueue) EnqueuePush(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.ids = append(r.ids, id)
	return nil
}

func (r *recordingQueue) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func TestSchedulerEndToEnd(t *testing.T) {
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
	us := users.NewStore(pool)
	alice, _ := us.EnsureBySubject(ctx, "na-"+sfx, "na-"+sfx+"@example.com", "")
	bob, _ := us.EnsureBySubject(ctx, "nb-"+sfx, "nb-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})

	// Fixed clock: 2026-11-20 08:00 WIB. Warranty ends 2026-11-23. Offset 3 (11-20 09:00) is
	// planned now; offset 1 (11-22 09:00) is 49h away, beyond the 48h planning window, so it is
	// planned on a later run; bigger offsets are in the past and are skipped.
	clock := time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	push := &recordingQueue{}
	sch := &notifications.Scheduler{Pool: pool, Pushes: push, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock }}
	svc := notifications.NewService(pool)

	var pid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
		VALUES ($1,'Laptop','2026-01-01','2026-11-23') RETURNING id`, alice).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// The scheduler scans every purchase; leave other rows alone by counting only ours.
	count := func(status string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE purchase_id = $1 AND status = $2`, pid, status).Scan(&n)
		return
	}

	// --- Planning is idempotent ---
	if _, err := sch.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if got := count("scheduled"); got != 1 {
		t.Fatalf("planned %d, want 1 (offset 3 only; offset 1 is outside the window)", got)
	}
	for i := 0; i < 3; i++ {
		if _, err := sch.Plan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("scheduled"); got != 1 {
		t.Fatalf("re-planning created duplicates: %d", got)
	}

	// --- Not due yet at 08:00 (reminder is 09:00 WIB) ---
	if st, err := sch.Dispatch(ctx); err != nil || st.Sent != 0 {
		t.Fatalf("early dispatch: %+v %v", st, err)
	}
	if list, _ := svc.List(ctx, alice, false, 0); len(list) != 0 {
		t.Fatalf("inbox should be empty before due time: %d", len(list))
	}

	// --- 09:01 WIB: the offset-3 reminder goes out once (push channel => one push job queued, in-app recorded) ---
	clock = time.Date(2026, 11, 20, 9, 1, 0, 0, wib)
	st, err := sch.Dispatch(ctx)
	if err != nil || st.Sent != 1 || push.count() != 1 {
		t.Fatalf("dispatch: %+v pushes=%d err=%v", st, push.count(), err)
	}
	if st, _ := sch.Dispatch(ctx); st.Sent != 0 {
		t.Fatalf("second dispatch re-sent: %+v", st)
	}
	list, _ := svc.List(ctx, alice, false, 0)
	if len(list) != 1 || list[0].Read || list[0].PurchaseID == nil || *list[0].PurchaseID != pid {
		t.Fatalf("inbox: %+v", list)
	}
	if other, _ := svc.List(ctx, bob, false, 0); len(other) != 0 {
		t.Fatal("bob sees alice's notification")
	}

	// --- Mark read: owner only, idempotent, unread filter ---
	if err := svc.MarkRead(ctx, bob, list[0].ID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("bob mark-read: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.MarkRead(ctx, alice, list[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if unread, _ := svc.List(ctx, alice, true, 0); len(unread) != 0 {
		t.Fatal("read notification still listed as unread")
	}
	// A day later the offset-1 reminder enters the planning window; planning twice adds it once.
	clock = time.Date(2026, 11, 21, 8, 0, 0, 0, wib)
	for i := 0; i < 2; i++ {
		if _, err := sch.Plan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("scheduled"); got != 1 {
		t.Fatalf("offset-1 reminder: scheduled=%d, want 1", got)
	}
	// A scheduled (not delivered) notification cannot be marked read.
	var scheduledID uuid.UUID
	pool.QueryRow(ctx, `SELECT id FROM notifications WHERE purchase_id = $1 AND status = 'scheduled'`, pid).Scan(&scheduledID)
	if err := svc.MarkRead(ctx, alice, scheduledID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("undelivered mark-read: %v", err)
	}

	// --- Re-validation: the user moves the warranty end date before the offset-1 reminder is due ---
	pool.Exec(ctx, `UPDATE purchases SET warranty_end_date = '2026-12-31' WHERE id = $1`, pid)
	clock = time.Date(2026, 11, 22, 9, 5, 0, 0, wib)
	if st, _ := sch.Dispatch(ctx); st.Cancelled != 1 || st.Sent != 0 {
		t.Fatalf("stale reminder must be cancelled: %+v", st)
	}
	if count("cancelled") != 1 {
		t.Fatal("expected one cancelled row")
	}

	// --- A queue outage never un-delivers the in-app notification ---
	pool.Exec(ctx, `UPDATE purchases SET warranty_end_date = '2026-11-24' WHERE id = $1`, pid)
	pool.Exec(ctx, `DELETE FROM notifications WHERE purchase_id = $1`, pid)
	clock = time.Date(2026, 11, 22, 8, 10, 0, 0, wib)
	sch.Plan(ctx) // offset 3 => 11-21 09:00 (23h ago: inside catch-up), offset 1 => 11-23 09:00 (future)
	push.err = errors.New("redis down")
	if st, err := sch.Dispatch(ctx); err != nil || st.Sent != 1 {
		t.Fatalf("dispatch must succeed even if enqueueing fails: %+v %v", st, err)
	}
	if count("sent") != 1 {
		t.Fatal("notification should still be delivered in-app")
	}
	push.err = nil

	// --- Quiet hours defer delivery ---
	pool.Exec(ctx, `DELETE FROM notifications WHERE purchase_id = $1`, pid)
	pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, quiet_hours_start, quiet_hours_end, timezone)
		VALUES ($1, true, '[1]', 'in_app', '08:00', '12:00', 'Asia/Jakarta')`, alice)
	clock = time.Date(2026, 11, 23, 7, 0, 0, 0, wib) // offset 1 => 11-23, planned at 12:00 (end of quiet window)
	sch.Plan(ctx)
	var at time.Time
	if err := pool.QueryRow(ctx, `SELECT scheduled_at FROM notifications WHERE purchase_id = $1`, pid).Scan(&at); err != nil {
		t.Fatalf("expected a planned reminder: %v", err)
	}
	if !at.Equal(time.Date(2026, 11, 23, 12, 0, 0, 0, wib)) {
		t.Fatalf("scheduled_at %v should be pushed to quiet-hours end", at)
	}
	clock = time.Date(2026, 11, 23, 12, 1, 0, 0, wib)
	pushesBefore := push.count()
	if st, _ := sch.Dispatch(ctx); st.Sent != 1 || push.count() != pushesBefore {
		t.Fatalf("in_app channel must not queue a push job: %+v", st)
	}

	// --- Disabled preferences cancel pending reminders ---
	pool.Exec(ctx, `DELETE FROM notifications WHERE purchase_id = $1`, pid)
	pool.Exec(ctx, `UPDATE reminder_preferences SET quiet_hours_start = NULL, quiet_hours_end = NULL WHERE user_id = $1`, alice)
	clock = time.Date(2026, 11, 22, 8, 0, 0, 0, wib)
	sch.Plan(ctx)
	pool.Exec(ctx, `UPDATE reminder_preferences SET enabled = false WHERE user_id = $1`, alice)
	clock = time.Date(2026, 11, 23, 9, 30, 0, 0, wib)
	if st, _ := sch.Dispatch(ctx); st.Sent != 0 || st.Cancelled != 1 {
		t.Fatalf("disabled prefs: %+v", st)
	}
}
