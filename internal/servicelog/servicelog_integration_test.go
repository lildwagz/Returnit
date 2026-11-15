//go:build integration

package servicelog_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/servicelog"
	"github.com/returnit/returnit/internal/users"
)

func sp(s string) *string { return &s }

// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/servicelog (database migrated to 19).
func TestServiceHistory(t *testing.T) {
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
	alice, _ := us.EnsureBySubject(ctx, "sa-"+sfx, "sa-"+sfx+"@example.com", "Alice")
	bob, _ := us.EnsureBySubject(ctx, "sb-"+sfx, "sb-"+sfx+"@example.com", "Bob")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob} {
			pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
	})

	psvc := purchases.NewService(purchases.NewPGRepo(pool), nil)
	repo := servicelog.NewPGRepo(pool)
	svc := servicelog.NewService(repo)

	p, err := psvc.Create(ctx, alice, purchases.RawInput{ProductName: "Kulkas", PurchaseDate: "2026-01-10"})
	if err != nil {
		t.Fatal(err)
	}

	// Empty history, then two visits: the newest service date comes first.
	if got, err := svc.List(ctx, alice, p.ID); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	cost := 150000.0
	first, err := svc.Create(ctx, alice, p.ID, servicelog.RawInput{ServiceDate: "2026-05-02", Place: sp("Service Center A"),
		Problem: sp("Tidak dingin"), Result: sp("Ganti freon"), Cost: &cost})
	if err != nil {
		t.Fatal(err)
	}
	if first.AddedBy == nil || *first.AddedBy != "Alice" || !first.AddedByMe || first.Cost == nil || *first.Cost != cost {
		t.Errorf("created: %+v", first)
	}
	if _, err := svc.Create(ctx, alice, p.ID, servicelog.RawInput{ServiceDate: "2026-09-20", Problem: sp("Bunyi keras")}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx, alice, p.ID)
	if err != nil || len(list) != 2 || list[0].ServiceDate.Format("2006-01-02") != "2026-09-20" {
		t.Fatalf("list order: %+v %v", list, err)
	}

	// Someone who cannot see the purchase sees nothing and can change nothing (404, not 403).
	if _, err := svc.List(ctx, bob, p.ID); !errors.Is(err, servicelog.ErrNotFound) {
		t.Errorf("bob list: %v", err)
	}
	if _, err := svc.Create(ctx, bob, p.ID, servicelog.RawInput{ServiceDate: "2026-10-01"}); !errors.Is(err, servicelog.ErrNotFound) {
		t.Errorf("bob create: %v", err)
	}
	if _, err := svc.Update(ctx, bob, p.ID, first.ID, servicelog.RawInput{ServiceDate: "2026-10-01"}); !errors.Is(err, servicelog.ErrNotFound) {
		t.Errorf("bob update: %v", err)
	}
	if err := svc.Delete(ctx, bob, p.ID, first.ID); !errors.Is(err, servicelog.ErrNotFound) {
		t.Errorf("bob delete: %v", err)
	}

	// Update replaces the fields; a record of another purchase cannot be reached through this one.
	upd, err := svc.Update(ctx, alice, p.ID, first.ID, servicelog.RawInput{ServiceDate: "2026-05-03", Result: sp("Ganti kompresor")})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Place != nil || upd.Problem != nil || upd.Result == nil || *upd.Result != "Ganti kompresor" || upd.Cost != nil {
		t.Errorf("updated: %+v", upd)
	}
	other, _ := psvc.Create(ctx, alice, purchases.RawInput{ProductName: "TV", PurchaseDate: "2026-01-10"})
	if err := svc.Delete(ctx, alice, other.ID, first.ID); !errors.Is(err, servicelog.ErrNotFound) {
		t.Errorf("delete through another purchase: %v", err)
	}
	if err := svc.Delete(ctx, alice, p.ID, first.ID); err != nil {
		t.Fatal(err)
	}

	// The limit holds.
	if _, err := repo.Create(ctx, alice, other.ID, servicelog.Input{ServiceDate: first.ServiceDate.Time}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, alice, other.ID, servicelog.Input{ServiceDate: first.ServiceDate.Time}, 1); !errors.Is(err, servicelog.ErrLimit) {
		t.Errorf("limit: %v", err)
	}

	// Deleting the purchase deletes its history.
	if err := psvc.Delete(ctx, alice, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM service_records WHERE purchase_id = $1`, p.ID).Scan(&n)
	if n != 0 {
		t.Errorf("records left after deleting the purchase: %d", n)
	}
}
