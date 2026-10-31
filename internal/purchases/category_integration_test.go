//go:build integration

package purchases_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/purchases (database migrated to 12).
func TestCategory(t *testing.T) {
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
	suffix := uuid.NewString()
	uid, err := users.NewStore(pool).EnsureBySubject(ctx, "cat-"+suffix, "cat-"+suffix+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := users.NewStore(pool).EnsureBySubject(ctx, "cat2-"+suffix, "cat2-"+suffix+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{uid, other} {
			pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
	})
	svc := purchases.NewService(purchases.NewPGRepo(pool), nil)

	parse := func(body string) purchases.RawInput {
		var r purchases.RawInput
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	mk := func(body string) purchases.Purchase {
		p, err := svc.Create(ctx, uid, parse(body))
		if err != nil {
			t.Fatalf("create %s: %v", body, err)
		}
		return p
	}

	phone := mk(`{"productName":"Phone","purchaseDate":"2026-01-01","category":"phone"}`)
	mk(`{"productName":"Laptop","purchaseDate":"2026-01-02","category":"computer"}`)
	plain := mk(`{"productName":"No category","purchaseDate":"2026-01-03"}`)
	if phone.Category == nil || *phone.Category != "phone" || plain.Category != nil {
		t.Fatalf("create: %v / %v", phone.Category, plain.Category)
	}

	// Filter, alone and with the status filter and search.
	if _, total, err := svc.List(ctx, uid, purchases.ListFilter{Category: "phone"}); err != nil || total != 1 {
		t.Errorf("phone filter: total=%d err=%v", total, err)
	}
	if _, total, _ := svc.List(ctx, uid, purchases.ListFilter{Category: "phone", Status: "none"}); total != 1 {
		t.Errorf("phone + none: total=%d", total)
	}
	if _, total, _ := svc.List(ctx, uid, purchases.ListFilter{Category: "phone", Query: "laptop"}); total != 0 {
		t.Errorf("phone + laptop: total=%d", total)
	}
	if _, total, _ := svc.List(ctx, other, purchases.ListFilter{Category: "phone"}); total != 0 {
		t.Errorf("other user sees %d", total)
	}

	// An update without the field keeps the category (an old app sends the whole record without it).
	up, err := svc.Update(ctx, uid, phone.ID, parse(`{"productName":"Phone 2","purchaseDate":"2026-01-01"}`))
	if err != nil || up.ProductName != "Phone 2" || up.Category == nil || *up.Category != "phone" {
		t.Fatalf("absent keeps: %+v %v", up.Category, err)
	}
	// A new value changes it.
	up, _ = svc.Update(ctx, uid, phone.ID, parse(`{"productName":"Phone 2","purchaseDate":"2026-01-01","category":"wearable"}`))
	if up.Category == nil || *up.Category != "wearable" {
		t.Fatalf("change: %v", up.Category)
	}
	// An explicit null clears it.
	up, _ = svc.Update(ctx, uid, phone.ID, parse(`{"productName":"Phone 2","purchaseDate":"2026-01-01","category":null}`))
	if up.Category != nil {
		t.Fatalf("null clears: %v", *up.Category)
	}

	// The database refuses a key outside the list even if the service were bypassed.
	if _, err := pool.Exec(ctx, `UPDATE purchases SET category = 'bogus' WHERE id = $1`, plain.ID); err == nil {
		t.Error("CHECK constraint did not fire")
	}
}
