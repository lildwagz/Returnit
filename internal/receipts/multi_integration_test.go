//go:build integration

package receipts_test

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

	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/receipts"
	"github.com/returnit/returnit/internal/users"
)

// memStore is an in-memory storage.Store, so this test needs Postgres only (no MinIO).
type memStore struct{ objects map[string][]byte }

func (m *memStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, _ := io.ReadAll(r)
	m.objects[key] = b
	return nil
}
func (m *memStore) Delete(_ context.Context, key string) error                        { delete(m.objects, key); return nil }
func (m *memStore) List(context.Context, string, func(string, time.Time) error) error { return nil }
func (m *memStore) SignedGetURL(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://signed.example/" + key, nil
}

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/receipts (database migrated to 13).
func TestSeveralReceiptsPerPurchase(t *testing.T) {
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

	store := &memStore{objects: map[string][]byte{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rsvc := receipts.NewService(receipts.NewPGRepo(pool), store, malware.Disabled{}, nil, 1<<20, log)
	psvc := purchases.NewService(purchases.NewPGRepo(pool), rsvc)

	sfx := uuid.NewString()
	us := users.NewStore(pool)
	alice, _ := us.EnsureBySubject(ctx, "ma-"+sfx, "ma-"+sfx+"@example.com", "")
	bob, _ := us.EnsureBySubject(ctx, "mb-"+sfx, "mb-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
	})

	pdf := func(tag string) []byte { return append([]byte("%PDF-1.7\n"), []byte(tag)...) }
	add := func(p purchases.Purchase, name string) receipts.Receipt {
		t.Helper()
		b := pdf(name)
		r, err := rsvc.Add(ctx, alice, p.ID, name, int64(len(b)), bytes.NewReader(b))
		if err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
		return r
	}

	p, err := psvc.Create(ctx, alice, purchases.RawInput{ProductName: "Camera", PurchaseDate: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Receipt != nil || p.Receipts == nil || len(p.Receipts) != 0 {
		t.Fatalf("new purchase: receipt=%v receipts=%v (must be an empty list, not null)", p.Receipt, p.Receipts)
	}

	// Several receipts; one purchase is still one row in the list, oldest first, with the oldest as "receipt".
	r1 := add(p, "one.pdf")
	time.Sleep(5 * time.Millisecond)
	r2 := add(p, "two.pdf")
	time.Sleep(5 * time.Millisecond)
	r3 := add(p, "three.pdf")
	got, err := psvc.Get(ctx, alice, p.ID)
	if err != nil || len(got.Receipts) != 3 || got.Receipts[0].ID != r1.ID || got.Receipts[2].ID != r3.ID {
		t.Fatalf("get: %+v %v", got.Receipts, err)
	}
	if got.Receipt == nil || got.Receipt.ID != r1.ID || got.Receipt.FileName != "one.pdf" {
		t.Fatalf("receipt (oldest) = %+v", got.Receipt)
	}
	items, total, err := psvc.List(ctx, alice, purchases.ListFilter{})
	if err != nil || total != 1 || len(items) != 1 || len(items[0].Receipts) != 3 {
		t.Fatalf("list: total=%d items=%d err=%v", total, len(items), err)
	}

	// The original API acts on the oldest.
	url, _, rec, err := rsvc.SignedURL(ctx, alice, p.ID)
	if err != nil || rec.ID != r1.ID || url == "" {
		t.Fatalf("original download: %v %v", rec.ID, err)
	}
	b := pdf("replacement.pdf")
	if _, err := rsvc.Upload(ctx, alice, p.ID, "replacement.pdf", int64(len(b)), bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	got, _ = psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != 3 || got.Receipts[0].FileName != "replacement.pdf" && got.Receipts[2].FileName != "replacement.pdf" {
		t.Fatalf("replace: %+v", got.Receipts)
	}

	// By id, owner scoped.
	if _, _, _, err := rsvc.SignedURLByID(ctx, bob, p.ID, r2.ID); !errors.Is(err, receipts.ErrNotFound) {
		t.Errorf("bob download: %v", err)
	}
	if err := rsvc.DeleteByID(ctx, bob, p.ID, r2.ID); !errors.Is(err, receipts.ErrNotFound) {
		t.Errorf("bob delete: %v", err)
	}
	if err := rsvc.DeleteByID(ctx, alice, p.ID, r2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != 2 {
		t.Fatalf("after delete: %d", len(got.Receipts))
	}

	// The limit, under concurrency: the purchase row lock serialises, so at most 5 ever exist.
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			b := pdf("race")
			_, err := rsvc.Add(ctx, alice, p.ID, "race.pdf", int64(len(b)), bytes.NewReader(b))
			done <- err
		}(i)
	}
	ok, limited := 0, 0
	for i := 0; i < 8; i++ {
		switch err := <-done; {
		case err == nil:
			ok++
		case errors.Is(err, receipts.ErrLimit):
			limited++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	got, _ = psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != receipts.MaxPerPurchase || ok != 3 || limited != 5 {
		t.Errorf("race: receipts=%d ok=%d limited=%d", len(got.Receipts), ok, limited)
	}

	// Deleting a purchase with several receipts queues every file (the trigger) and does not fail.
	var before int
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions`).Scan(&before)
	if err := psvc.Delete(ctx, alice, p.ID); err != nil {
		t.Fatalf("delete purchase with %d receipts: %v", len(got.Receipts), err)
	}
	var after int
	pool.QueryRow(ctx, `SELECT count(*) FROM object_deletions`).Scan(&after)
	if after-before != receipts.MaxPerPurchase {
		t.Errorf("queued deletions: %d, want %d", after-before, receipts.MaxPerPurchase)
	}
}
