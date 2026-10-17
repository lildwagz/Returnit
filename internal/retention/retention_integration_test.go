//go:build integration

package retention_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/retention"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/users"
)

func TestRetention(t *testing.T) {
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
	w := &retention.Worker{Pool: pool, Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		OrphanGrace: time.Hour}

	exists := func(key string) bool {
		found := false
		store.List(ctx, key, func(k string, _ time.Time) error { found = found || k == key; return nil })
		return found
	}
	put := func(key string) {
		if err := store.Put(ctx, key, bytes.NewReader([]byte("data")), 4, "application/pdf"); err != nil {
			t.Fatal(err)
		}
	}

	// Account deletion: deleting the user cascades to receipts; the trigger must queue the file.
	us := users.NewStore(pool)
	sfx := uuid.NewString()
	uid, _ := us.EnsureBySubject(ctx, "ret-"+sfx, "ret-"+sfx+"@example.com", "")
	var pid uuid.UUID
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date) VALUES ($1,'x','2026-01-01') RETURNING id`, uid).Scan(&pid)
	keyA := "receipts/" + uid.String() + "/" + uuid.NewString() + ".pdf"
	keyB := "receipts/" + uid.String() + "/" + uuid.NewString() + ".pdf"
	put(keyA)
	put(keyB)
	pool.Exec(ctx, `INSERT INTO receipts (purchase_id, storage_key, file_name, content_type, size_bytes) VALUES ($1,$2,'a.pdf','application/pdf',4)`, pid, keyA)
	// Replacing the file (UPDATE of storage_key) must queue the previous key.
	pool.Exec(ctx, `UPDATE receipts SET storage_key = $2 WHERE purchase_id = $1`, pid, keyB)
	var queued int
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions WHERE storage_key = $1`, keyA).Scan(&queued)
	if queued != 1 {
		t.Fatalf("replace did not queue old key (queued=%d)", queued)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions WHERE storage_key IN ($1,$2)`, keyA, keyB).Scan(&queued)
	if queued != 2 {
		t.Fatalf("account deletion did not queue files (queued=%d)", queued)
	}

	if n, err := w.PurgeQueued(ctx); err != nil || n < 2 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	if exists(keyA) || exists(keyB) {
		t.Fatal("objects still in storage after purge")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions WHERE storage_key IN ($1,$2)`, keyA, keyB).Scan(&queued)
	if queued != 0 {
		t.Fatal("queue rows not removed after successful purge")
	}

	// Orphan sweep: an unreferenced object is removed only once it is older than the grace period.
	orphan := "receipts/" + uuid.NewString() + "/" + uuid.NewString() + ".pdf"
	put(orphan)
	if n, _ := w.SweepOrphans(ctx); n != 0 || !exists(orphan) {
		t.Fatalf("fresh object must be protected by the grace period (removed=%d)", n)
	}
	w.OrphanGrace = -time.Minute // treat everything as old
	if _, err := w.SweepOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(orphan) {
		t.Fatal("orphan not swept")
	}
}
