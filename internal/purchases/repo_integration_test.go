//go:build integration

package purchases_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/purchases
// The database must already be migrated. Each run creates its own users and cleans up.
func TestPGRepo(t *testing.T) {
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

	us := users.NewStore(pool)
	suffix := uuid.NewString()
	alice, err := us.EnsureBySubject(ctx, "alice-"+suffix, "Alice-"+suffix+"@Example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, _ := us.EnsureBySubject(ctx, "bob-"+suffix, "bob-"+suffix+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})

	// EnsureBySubject is idempotent.
	if again, _ := us.EnsureBySubject(ctx, "alice-"+suffix, "x@y.z", ""); again != alice {
		t.Fatalf("expected same user id")
	}

	svc := purchases.NewService(purchases.NewPGRepo(pool), nil)
	day := func(offset int) *string {
		s := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		return &s
	}
	price := 1500000.5
	mk := func(name string, end *string) purchases.Purchase {
		p, err := svc.Create(ctx, alice, purchases.RawInput{ProductName: name, PurchaseDate: "2026-01-01",
			Price: &price, WarrantyEndDate: end})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return p
	}
	active := mk("Laptop Active", day(90))
	mk("Phone Expiring", day(10))
	mk("TV Expired", day(-5))
	mk("100% Fan_Club", nil)

	if active.Price == nil || *active.Price != price {
		t.Fatalf("price round trip: %v", active.Price)
	}

	counts := map[string]int{"": 4, "active": 1, "expiring": 1, "expired": 1, "none": 1}
	for status, want := range counts {
		_, total, err := svc.List(ctx, alice, purchases.ListFilter{Status: status})
		if err != nil || total != want {
			t.Errorf("status %q: total=%d err=%v want %d", status, total, err, want)
		}
	}
	// LIKE wildcards in the query are treated literally.
	if _, total, _ := svc.List(ctx, alice, purchases.ListFilter{Query: "%"}); total != 1 {
		t.Errorf("literal %%: total=%d", total)
	}
	if _, total, _ := svc.List(ctx, alice, purchases.ListFilter{Query: "laptop"}); total != 1 {
		t.Errorf("case-insensitive search: total=%d", total)
	}
	items, total, _ := svc.List(ctx, alice, purchases.ListFilter{Page: 2, PageSize: 3})
	if total != 4 || len(items) != 1 {
		t.Errorf("pagination: total=%d len=%d", total, len(items))
	}

	// Bob cannot see or touch Alice's data.
	if _, err := svc.Get(ctx, bob, active.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Errorf("bob get: %v", err)
	}
	if _, err := svc.Update(ctx, bob, active.ID, purchases.RawInput{ProductName: "x", PurchaseDate: "2026-01-01"}); !errors.Is(err, purchases.ErrNotFound) {
		t.Errorf("bob update: %v", err)
	}
	if err := svc.Delete(ctx, bob, active.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Errorf("bob delete: %v", err)
	}
	if _, total, _ := svc.List(ctx, bob, purchases.ListFilter{}); total != 0 {
		t.Errorf("bob list total=%d", total)
	}

	up, err := svc.Update(ctx, alice, active.ID, purchases.RawInput{ProductName: "Renamed", PurchaseDate: "2026-01-02"})
	if err != nil || up.ProductName != "Renamed" || up.WarrantyEndDate != nil {
		t.Errorf("update: %+v %v", up, err)
	}
	if err := svc.Delete(ctx, alice, active.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, alice, active.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
}
