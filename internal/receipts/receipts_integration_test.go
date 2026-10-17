//go:build integration

package receipts_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/receipts"
	"github.com/returnit/returnit/internal/retention"
	"github.com/returnit/returnit/internal/storage"
	"github.com/returnit/returnit/internal/users"
)

// Needs migrated Postgres (TEST_DATABASE_URL) and MinIO (TEST_S3_ENDPOINT/ACCESS_KEY/SECRET_KEY).
func TestReceiptsEndToEnd(t *testing.T) {
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
	rsvc := receipts.NewService(receipts.NewPGRepo(pool), store, malware.Disabled{}, nil, 1<<20, log)
	psvc := purchases.NewService(purchases.NewPGRepo(pool), rsvc)

	us := users.NewStore(pool)
	sfx := uuid.NewString()
	alice, _ := us.EnsureBySubject(ctx, "ra-"+sfx, "ra-"+sfx+"@example.com", "")
	bob, _ := us.EnsureBySubject(ctx, "rb-"+sfx, "rb-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})

	p, err := psvc.Create(ctx, alice, purchases.RawInput{ProductName: "Camera", PurchaseDate: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Receipt != nil {
		t.Fatal("new purchase should have no receipt")
	}

	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("A"), 2048)...)
	if _, err := rsvc.Upload(ctx, bob, p.ID, "x.pdf", int64(len(pdf)), bytes.NewReader(pdf)); !errors.Is(err, receipts.ErrNotFound) {
		t.Fatalf("bob upload: %v", err)
	}
	if _, err := rsvc.Upload(ctx, alice, p.ID, "scan.pdf", int64(len(pdf)), bytes.NewReader(pdf)); err != nil {
		t.Fatal(err)
	}

	got, _ := psvc.Get(ctx, alice, p.ID)
	if got.Receipt == nil || got.Receipt.ContentType != "application/pdf" || got.Receipt.SizeBytes != int64(len(pdf)) {
		t.Fatalf("receipt in purchase: %+v", got.Receipt)
	}

	// Owner can download via signed URL; the bytes match.
	url, _, _, err := rsvc.SignedURL(ctx, alice, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(body, pdf) {
		t.Fatalf("download: status=%d len=%d", resp.StatusCode, len(body))
	}
	if _, _, _, err := rsvc.SignedURL(ctx, bob, p.ID); !errors.Is(err, receipts.ErrNotFound) {
		t.Fatalf("bob signed url: %v", err)
	}

	// The bucket is private: the bare object URL (no signature) must be refused.
	bare := url[:bytes.IndexByte([]byte(url), '?')]
	if r2, err := http.Get(bare); err == nil {
		r2.Body.Close()
		if r2.StatusCode == 200 {
			t.Fatal("unsigned object access succeeded; bucket is not private")
		}
	}

	// Replace, then delete the purchase: the object must be purged.
	pdf2 := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("B"), 10)...)
	if _, err := rsvc.Upload(ctx, alice, p.ID, "v2.pdf", int64(len(pdf2)), bytes.NewReader(pdf2)); err != nil {
		t.Fatal(err)
	}
	url2, _, _, _ := rsvc.SignedURL(ctx, alice, p.ID)
	if err := psvc.Delete(ctx, alice, p.ID); err != nil {
		t.Fatal(err)
	}
	// Deleting the purchase queued the file (DB trigger); the web process does not delete it.
	// The worker's purge job does.
	w := &retention.Worker{Pool: pool, Store: store, Log: log, OrphanGrace: time.Hour}
	if n, err := w.PurgeQueued(ctx); err != nil || n < 2 { // replaced file + current file
		t.Fatalf("worker purge: n=%d err=%v", n, err)
	}
	r3, err := http.Get(url2)
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != 404 {
		t.Fatalf("object should be purged after purchase delete, status=%d", r3.StatusCode)
	}
}
