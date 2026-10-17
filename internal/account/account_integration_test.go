//go:build integration

package account_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/retention"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/users"
)

var wib = time.FixedZone("WIB", 7*3600)

type noQueue struct{}

func (noQueue) EnqueuePush(context.Context, uuid.UUID) error { return nil }

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) (n int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

// Needs migrated Postgres (TEST_DATABASE_URL) and MinIO (TEST_S3_*) for the file-cleanup step.
func TestAccountDeletionLifecycle(t *testing.T) {
	dbURL, ep := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	if dbURL == "" || ep == "" {
		t.Skip("integration env not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := storage.NewS3(ctx, storage.Config{Endpoint: ep, AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), Bucket: "returnit-test", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	us := users.NewStore(pool)
	sfx := uuid.NewString()
	alice, _ := us.EnsureBySubject(ctx, "aa-"+sfx, "aa-"+sfx+"@example.com", "Alice")
	bob, _ := us.EnsureBySubject(ctx, "ab-"+sfx, "ab-"+sfx+"@example.com", "Bob")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
			pool.Exec(context.Background(), `DELETE FROM account_deletion_log WHERE user_id = $1`, id)
			pool.Exec(context.Background(), `DELETE FROM account_emails WHERE user_id = $1`, id)
		}
	})

	clock := time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	svc := account.NewService(pool, 7*24*time.Hour)
	svc.Now = func() time.Time { return clock }

	// Alice owns a purchase with a receipt file in storage (her data to erase); Bob owns one too.
	var alicePurchase, bobPurchase uuid.UUID
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
		VALUES ($1,'Laptop','2026-01-01','2026-11-23') RETURNING id`, alice).Scan(&alicePurchase)
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
		VALUES ($1,'Phone','2026-01-01','2026-11-23') RETURNING id`, bob).Scan(&bobPurchase)
	key := "receipts/" + alice.String() + "/" + uuid.NewString() + ".pdf"
	if err := store.Put(ctx, key, bytes.NewReader([]byte("data")), 4, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO receipts (purchase_id, storage_key, file_name, content_type, size_bytes)
		VALUES ($1,$2,'a.pdf','application/pdf',4)`, alicePurchase, key)
	pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel) VALUES ($1,true,'[3]','push')`, alice)

	// --- Profile ---
	acct, err := svc.Get(ctx, alice)
	if err != nil || acct.Email != "aa-"+sfx+"@example.com" || acct.DeletionScheduledFor != nil {
		t.Fatalf("get: %+v %v", acct, err)
	}
	if _, err := svc.Get(ctx, uuid.New()); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	// --- Request: scheduled 7 days out; asking again later does not move it ---
	at, err := svc.RequestDeletion(ctx, alice)
	if err != nil || !at.Equal(clock.Add(7*24*time.Hour)) {
		t.Fatalf("schedule: %v %v", at, err)
	}
	clock = clock.Add(48 * time.Hour)
	if again, _ := svc.RequestDeletion(ctx, alice); !again.Equal(at) {
		t.Fatalf("repeat request moved the schedule: %v vs %v", again, at)
	}

	// --- Nothing is erased during the grace period ---
	if n, err := svc.PurgeDue(ctx); err != nil || n != 0 {
		t.Fatalf("purged during grace: n=%d err=%v", n, err)
	}
	if count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, alice) != 1 {
		t.Fatal("account vanished during the grace period")
	}

	// --- Reminders stop for a user who asked to leave: not planned, and already-planned ones cancelled ---
	rec := &notifications.Scheduler{Pool: pool, Pushes: noQueue{}, Log: log, Now: func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, wib) }}
	rec.Plan(ctx)
	if n := count(t, pool, `SELECT count(*) FROM notifications WHERE purchase_id = $1`, alicePurchase); n != 0 {
		t.Fatalf("planned %d reminders for an account pending deletion", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM notifications WHERE purchase_id = $1`, bobPurchase); n == 0 {
		t.Fatal("Bob's reminders should still be planned")
	}
	pool.Exec(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, scheduled_at, status, idempotency_key, channel, offset_days, warranty_end_date)
		VALUES ($1,$2,'t','b',$3,'scheduled',$4,'push',3,'2026-11-23')`, alice, alicePurchase, time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC), "pending-"+sfx)
	rec.Now = func() time.Time { return time.Date(2026, 11, 20, 9, 1, 0, 0, wib) }
	if st, err := rec.Dispatch(ctx); err != nil || st.Cancelled < 1 {
		t.Fatalf("dispatch: %+v %v", st, err)
	}
	if count(t, pool, `SELECT count(*) FROM notifications WHERE idempotency_key = $1 AND status = 'cancelled'`, "pending-"+sfx) != 1 {
		t.Fatal("pending-deletion reminder was not cancelled")
	}

	// --- Cancel inside the grace period restores normal operation ---
	if err := svc.CancelDeletion(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelDeletion(ctx, alice); err != nil { // idempotent
		t.Fatalf("second cancel: %v", err)
	}
	if a, _ := svc.Get(ctx, alice); a.DeletionScheduledFor != nil {
		t.Fatal("schedule not cleared")
	}
	svc.RequestDeletion(ctx, alice) // schedule again (clock is now Nov 22 08:00 -> due Nov 29 08:00)

	// --- Too late to cancel once the date has passed ---
	clock = time.Date(2026, 11, 29, 8, 0, 1, 0, wib)
	if err := svc.CancelDeletion(ctx, alice); !errors.Is(err, account.ErrTooLate) {
		t.Fatalf("late cancel: %v", err)
	}

	// --- Erasure: everything Alice owns goes; Bob is untouched; her file is queued then deleted ---
	if n, err := svc.PurgeDue(ctx); err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM users WHERE id = $1`,
		`SELECT count(*) FROM purchases WHERE user_id = $1`,
		`SELECT count(*) FROM reminder_preferences WHERE user_id = $1`,
		`SELECT count(*) FROM notifications WHERE user_id = $1`,
	} {
		if n := count(t, pool, q, alice); n != 0 {
			t.Errorf("data left after erasure (%s): %d", q, n)
		}
	}
	if count(t, pool, `SELECT count(*) FROM receipts WHERE purchase_id = $1`, alicePurchase) != 0 {
		t.Error("receipt metadata left after erasure")
	}
	if count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, bob) != 1 ||
		count(t, pool, `SELECT count(*) FROM purchases WHERE user_id = $1`, bob) != 1 {
		t.Error("another user's data was affected")
	}
	if count(t, pool, `SELECT count(*) FROM object_deletions WHERE storage_key = $1`, key) != 1 {
		t.Fatal("receipt file was not queued for deletion")
	}
	w := &retention.Worker{Pool: pool, Store: store, Log: log, OrphanGrace: time.Hour}
	if _, err := w.PurgeQueued(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	store.List(ctx, key, func(k string, _ time.Time) error { found = found || k == key; return nil })
	if found {
		t.Fatal("receipt file still in storage after erasure")
	}

	// --- The deletion log keeps only the UUID and timestamps ---
	var requested, deleted time.Time
	if err := pool.QueryRow(ctx, `SELECT requested_at, deleted_at FROM account_deletion_log WHERE user_id = $1`, alice).Scan(&requested, &deleted); err != nil {
		t.Fatalf("deletion log entry missing: %v", err)
	}
	if !deleted.After(requested) {
		t.Errorf("log timestamps look wrong: requested=%v deleted=%v", requested, deleted)
	}
	// Running the purge again is a no-op.
	if n, _ := svc.PurgeDue(ctx); n != 0 {
		t.Fatalf("second purge removed %d accounts", n)
	}
}
