//go:build integration

package purchases_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

// The one security rule of shared buckets: a person sees a purchase only through a bucket they are a member of, and a
// purchase they cannot see is "not found" (never "forbidden", so its existence is not given away).
// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/purchases
func TestBucketAccess(t *testing.T) {
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
	alice, err := us.EnsureBySubject(ctx, "ba-alice-"+suffix, "ba-alice-"+suffix+"@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := us.EnsureBySubject(ctx, "ba-bob-"+suffix, "ba-bob-"+suffix+"@example.com", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	var shared uuid.UUID
	t.Cleanup(func() {
		if shared != uuid.Nil {
			pool.Exec(ctx, `DELETE FROM buckets WHERE id = $1`, shared)
		}
		pool.Exec(ctx, `DELETE FROM buckets WHERE personal AND created_by IN ($1, $2)`, alice, bob)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1, $2)`, alice, bob)
	})

	// A new user has exactly one personal bucket and is its owner.
	var personal int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM bucket_members bm JOIN buckets b ON b.id = bm.bucket_id
WHERE bm.user_id = $1 AND b.personal AND bm.role = 'owner'`, alice).Scan(&personal); err != nil || personal != 1 {
		t.Fatalf("personal buckets of a new user: %d, %v", personal, err)
	}

	svc := purchases.NewService(purchases.NewPGRepo(pool), nil)
	end := "2099-01-01"
	p, err := svc.Create(ctx, alice, purchases.RawInput{ProductName: "Shared fridge", PurchaseDate: "2026-01-01", WarrantyEndDate: &end})
	if err != nil {
		t.Fatal(err)
	}

	// Another person's purchase is invisible in every way, and the answer is "not found".
	if _, err := svc.Get(ctx, bob, p.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("get by a non-member: %v", err)
	}
	if _, err := svc.Update(ctx, bob, p.ID, purchases.RawInput{ProductName: "x", PurchaseDate: "2026-01-01"}); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("update by a non-member: %v", err)
	}
	if err := svc.Delete(ctx, bob, p.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("delete by a non-member: %v", err)
	}
	if _, total, err := svc.List(ctx, bob, purchases.ListFilter{Page: 1, PageSize: 50}); err != nil || total != 0 {
		t.Fatalf("list for a non-member: total %d, %v", total, err)
	}
	if s, err := svc.Summary(ctx, bob); err != nil || s.Counts.Total != 0 || s.NextExpiring != nil {
		t.Fatalf("summary for a non-member: %+v, %v", s, err)
	}
	if d, err := svc.Deadlines(ctx, bob, 0); err != nil || len(d) != 0 {
		t.Fatalf("deadlines for a non-member: %d, %v", len(d), err)
	}

	// A shared bucket with both as members: the purchase moves in, and Bob sees and may change it.
	if err := pool.QueryRow(ctx, `INSERT INTO buckets (name, created_by) VALUES ('Home', $1) RETURNING id`, alice).Scan(&shared); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO bucket_members (bucket_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`, shared, alice, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE purchases SET bucket_id = $1 WHERE id = $2`, shared, p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, bob, p.ID)
	if err != nil || got.ProductName != "Shared fridge" {
		t.Fatalf("get by a member: %+v, %v", got, err)
	}
	if _, total, _ := svc.List(ctx, bob, purchases.ListFilter{Page: 1, PageSize: 50}); total != 1 {
		t.Fatalf("list for a member: total %d", total)
	}
	if s, _ := svc.Summary(ctx, bob); s.Counts.Total != 1 || s.NextExpiring == nil {
		t.Fatalf("summary for a member: %+v", s)
	}
	if d, _ := svc.Deadlines(ctx, bob, 0); len(d) != 1 {
		t.Fatalf("deadlines for a member: %d", len(d))
	}
	if _, err := svc.Update(ctx, bob, p.ID, purchases.RawInput{ProductName: "Renamed by Bob", PurchaseDate: "2026-01-01", WarrantyEndDate: &end}); err != nil {
		t.Fatalf("update by a member: %v", err)
	}

	// Leaving the bucket takes the access away at once.
	if _, err := pool.Exec(ctx, `DELETE FROM bucket_members WHERE bucket_id = $1 AND user_id = $2`, shared, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, bob, p.ID); !errors.Is(err, purchases.ErrNotFound) {
		t.Fatalf("get after leaving: %v", err)
	}
	// The purchase is still there for the person who stayed.
	if _, err := svc.Get(ctx, alice, p.ID); err != nil {
		t.Fatalf("get by the remaining member: %v", err)
	}
}
