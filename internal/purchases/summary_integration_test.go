//go:build integration

package purchases_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

func TestSummaryAgainstDatabase(t *testing.T) {
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
	st := users.NewStore(pool)
	owner, err := st.EnsureBySubject(ctx, "sm-"+sfx, "sm-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.EnsureBySubject(ctx, "sm2-"+sfx, "sm2-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{owner, other}) })

	add := func(u uuid.UUID, name, cur string, price any, endOffset, retOffset any) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, price, currency, warranty_end_date, return_deadline)
			VALUES ($1,$2,CURRENT_DATE - 100,$3,$4, CURRENT_DATE + $5::int, CURRENT_DATE + $6::int) RETURNING id`,
			u, name, price, cur, endOffset, retOffset).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add(owner, "active", "IDR", 1000000, 60, nil)
	expiring := add(owner, "expiring", "IDR", 500000, 10, 9)
	add(owner, "today", "IDR", 100000, 0, nil) // ends today: still covered, counts as expiring
	add(owner, "expired", "IDR", 200000, -5, -2)
	add(owner, "unknown", "IDR", 300000, nil, nil)
	add(owner, "usd", "USD", 100, 40, 3)
	add(other, "someone else", "IDR", 9999999, 5, 1)

	s, err := purchases.NewService(purchases.NewPGRepo(pool), nil).Summary(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Counts
	if c.Total != 6 || c.Active != 2 || c.Expiring != 2 || c.Expired != 1 || c.Unknown != 1 {
		t.Fatalf("counts: %+v", c)
	}
	covered := map[string]float64{}
	for _, a := range s.CoveredValue {
		covered[a.Currency] = a.Amount
	}
	// active + expiring + today (an expired or undated purchase is not covered, nor is someone else's)
	if len(covered) != 2 || covered["IDR"] != 1600000 || covered["USD"] != 100 {
		t.Fatalf("covered value: %+v", s.CoveredValue)
	}
	if s.NextExpiring == nil || s.NextExpiring.ProductName != "today" {
		t.Fatalf("next expiring: %+v", s.NextExpiring)
	}
	if s.NextReturn == nil || s.NextReturn.ProductName != "usd" { // +3 days is sooner than +9 and the past one is ignored
		t.Fatalf("next return: %+v", s.NextReturn)
	}
	_ = expiring

	empty, err := purchases.NewService(purchases.NewPGRepo(pool), nil).Summary(ctx, uuid.New())
	if err != nil || empty.Counts.Total != 0 || empty.NextExpiring != nil || empty.NextReturn != nil || len(empty.CoveredValue) != 0 {
		t.Fatalf("a user without purchases: %+v %v", empty, err)
	}
}
