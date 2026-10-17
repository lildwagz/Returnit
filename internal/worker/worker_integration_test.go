//go:build integration

package worker_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/retention"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/users"
	"github.com/returnit/returnit/internal/worker"
)

// Needs Postgres (TEST_DATABASE_URL), MinIO (TEST_S3_*) and Redis (TEST_REDIS_ADDR).
// Run integration packages serially (-p 1): they share one database.

var wib = time.FixedZone("WIB", 7*3600)

// behaviorPusher records every delivery attempt per user and fails on demand.
type behaviorPusher struct {
	mu        sync.Mutex
	calls     map[uuid.UUID]int
	failFirst map[uuid.UUID]bool // transient error on the first attempt only
	permanent map[uuid.UUID]bool // always a permanent error
}

func (b *behaviorPusher) Send(_ context.Context, u uuid.UUID, channel string, _ notifications.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls[u]++
	if b.permanent[u] {
		return notifications.ErrPermanent
	}
	if b.failFirst[u] && b.calls[u] == 1 {
		return context.DeadlineExceeded // transient
	}
	return nil
}

func (b *behaviorPusher) count(u uuid.UUID) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[u]
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", within, what)
}

func TestWebWorkerThroughRedis(t *testing.T) {
	dbURL, s3 := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_S3_ENDPOINT")
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if dbURL == "" || s3 == "" || redisAddr == "" {
		t.Skip("integration env not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := storage.NewS3(ctx, storage.Config{Endpoint: s3, AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), Bucket: "returnit-test", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	rcfg := queue.Config{Addr: redisAddr}
	if err := queue.Ping(rcfg); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jobs := queue.NewClient(rcfg) // what the web process holds
	t.Cleanup(func() { jobs.Close() })

	clock := time.Date(2026, 11, 20, 9, 1, 0, 0, wib) // offset-3 reminder (09:00) is just due
	pusher := &behaviorPusher{calls: map[uuid.UUID]int{}, failFirst: map[uuid.UUID]bool{}, permanent: map[uuid.UUID]bool{}}
	sched := &notifications.Scheduler{Pool: pool, Pushes: jobs, Log: log, Now: func() time.Time { return clock }}

	// Start the worker exactly as cmd/worker does, but with fast retries.
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx, worker.Deps{
			Redis: rcfg, Concurrency: 5, Log: log, ShutdownTimeout: 5 * time.Second,
			Reminders:  sched,
			Retention:  &retention.Worker{Pool: pool, Store: store, Log: log, OrphanGrace: 24 * time.Hour},
			Push:       &notifications.PushSender{Pool: pool, Pusher: pusher},
			Accounts:   account.NewService(pool, 0),
			RetryDelay: func(int) time.Duration { return 200 * time.Millisecond },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not shut down in time")
		}
	})

	// ---- 1. Web nudges the worker; the worker deletes the queued file well before its 30s cron ----
	key := "receipts/" + uuid.NewString() + "/" + uuid.NewString() + ".pdf"
	if err := store.Put(ctx, key, bytes.NewReader([]byte("data")), 4, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO object_deletions (storage_key) VALUES ($1)`, key); err != nil {
		t.Fatal(err)
	}
	exists := func() bool {
		found := false
		store.List(ctx, key, func(k string, _ time.Time) error { found = found || k == key; return nil })
		return found
	}
	if !exists() {
		t.Fatal("test object missing")
	}
	if err := jobs.RequestPurge(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "worker to purge the object after the web nudge", func() bool { return !exists() })
	var queued int
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions WHERE storage_key = $1`, key).Scan(&queued)
	if queued != 0 {
		t.Fatal("outbox row not cleared")
	}

	// ---- 2. Push delivery goes through the queue: success, dedupe, transient retry, permanent failure ----
	us := users.NewStore(pool)
	sfx := uuid.NewString()
	mkUser := func(tag string) (uuid.UUID, uuid.UUID) {
		uid, err := us.EnsureBySubject(ctx, tag+"-"+sfx, tag+"-"+sfx+"@example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		})
		var pid uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
			VALUES ($1,'Laptop','2026-01-01','2026-11-23') RETURNING id`, uid).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		return uid, pid
	}
	okU, okP := mkUser("ok")
	retryU, _ := mkUser("retry")
	permU, _ := mkUser("perm")
	pusher.failFirst[retryU] = true
	pusher.permanent[permU] = true

	if _, err := sched.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	eventually(t, 10*time.Second, "push for the healthy user", func() bool { return pusher.count(okU) >= 1 })
	eventually(t, 10*time.Second, "transient failure to be retried and then succeed", func() bool { return pusher.count(retryU) == 2 })
	eventually(t, 10*time.Second, "permanent failure to be attempted", func() bool { return pusher.count(permU) >= 1 })

	// Duplicate enqueue of the same notification is ignored (task ID dedupe).
	var nid uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM notifications WHERE purchase_id = $1 AND status = 'sent'`, okP).Scan(&nid); err != nil {
		t.Fatal(err)
	}
	if err := jobs.EnqueuePush(ctx, nid); err != nil {
		t.Fatalf("duplicate enqueue should be a no-op, got: %v", err)
	}

	time.Sleep(2 * time.Second) // give any wrongly retried / duplicated job time to show up
	if got := pusher.count(okU); got != 1 {
		t.Errorf("healthy user pushed %d times, want exactly 1", got)
	}
	if got := pusher.count(retryU); got != 2 {
		t.Errorf("transient failure: %d attempts, want 2 (fail, then succeed)", got)
	}
	if got := pusher.count(permU); got != 1 {
		t.Errorf("permanent failure was retried: %d attempts, want 1", got)
	}

	// The in-app notification exists regardless of how push went.
	var sent int
	pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = ANY(ARRAY[$1,$2,$3]::uuid[]) AND status = 'sent'`,
		okU.String(), retryU.String(), permU.String()).Scan(&sent)
	if sent != 3 {
		t.Errorf("in-app notifications sent = %d, want 3", sent)
	}
}
