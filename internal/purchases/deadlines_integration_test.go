//go:build integration

package purchases_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/purchases (database migrated).
func TestDeadlines(t *testing.T) {
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
	alice, _ := us.EnsureBySubject(ctx, "dl-"+sfx, "dl-"+sfx+"@example.com", "")
	bob, _ := us.EnsureBySubject(ctx, "dm-"+sfx, "dm-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
	})
	svc := purchases.NewService(purchases.NewPGRepo(pool), nil)

	day := func(offset int) *string {
		s := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		return &s
	}
	mk := func(u uuid.UUID, name string, warranty, ret *string, cat string) purchases.Purchase {
		raw := purchases.RawInput{ProductName: name, PurchaseDate: "2020-01-01", WarrantyEndDate: warranty, ReturnDeadline: ret}
		if cat != "" {
			raw.Category = purchases.OptString{Set: true, Value: &cat}
		}
		p, err := svc.Create(ctx, u, raw)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return p
	}
	soon := mk(alice, "Soon", day(3), nil, "phone")
	both := mk(alice, "Both", day(40), day(10), "")
	mk(alice, "Two months ago", day(-60), nil, "") // outside the default 30-day window, inside 90
	recent := mk(alice, "Just ended", day(-5), nil, "")
	mk(alice, "No dates", nil, nil, "")
	mk(bob, "Bobs", day(2), day(1), "")

	items, err := svc.Deadlines(ctx, alice, 30)
	if err != nil {
		t.Fatal(err)
	}
	// Soonest first: recent (-5), both/return (+10), soon (+3) ... in date order.
	want := []struct {
		id   uuid.UUID
		kind string
	}{{recent.ID, "warranty"}, {soon.ID, "warranty"}, {both.ID, "return"}, {both.ID, "warranty"}}
	if len(items) != len(want) {
		t.Fatalf("got %d items: %+v", len(items), items)
	}
	for i, w := range want {
		if items[i].PurchaseID != w.id || items[i].Kind != w.kind {
			t.Errorf("item %d = %s %s, want %s %s", i, items[i].PurchaseID, items[i].Kind, w.id, w.kind)
		}
	}
	if items[1].Category == nil || *items[1].Category != "phone" || items[0].Category != nil {
		t.Errorf("category: %v / %v", items[1].Category, items[0].Category)
	}
	// A wider window brings the two-month-old one back; pastDays=0 drops everything already past.
	if wide, _ := svc.Deadlines(ctx, alice, 90); len(wide) != 5 {
		t.Errorf("90 days: %d", len(wide))
	}
	if none, _ := svc.Deadlines(ctx, alice, 0); len(none) != 3 {
		t.Errorf("0 days: %d (only upcoming)", len(none))
	}
	// Never someone else's.
	if bobs, _ := svc.Deadlines(ctx, bob, 30); len(bobs) != 2 {
		t.Errorf("bob: %d", len(bobs))
	}
	if _, err := svc.Deadlines(ctx, alice, 91); err == nil {
		t.Error("pastDays 91 must be refused")
	}
}
