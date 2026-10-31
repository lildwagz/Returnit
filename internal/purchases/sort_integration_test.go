//go:build integration

package purchases_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/users"
)

// sort=warrantyEnd: running warranties first (soonest end on top), then ended ones (latest end first), then the
// purchases without a warranty end date. The default order (newest purchase first) is unchanged.
func TestSortByWarrantyEnd(t *testing.T) {
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
	uid, err := users.NewStore(pool).EnsureBySubject(ctx, "sort-"+sfx, "sort-"+sfx+"@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid) })
	svc := purchases.NewService(purchases.NewPGRepo(pool), nil)

	day := func(offset int) string { return time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02") }
	mk := func(name, bought, end string) {
		body := `{"productName":"` + name + `","purchaseDate":"` + bought + `"`
		if end != "" {
			body += `,"warrantyEndDate":"` + end + `"`
		}
		var raw purchases.RawInput
		if err := json.Unmarshal([]byte(body+`}`), &raw); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create(ctx, uid, raw); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	mk("far", day(-300), day(200))
	mk("none", day(-5), "")
	mk("ended-old", day(-400), day(-100))
	mk("soon", day(-200), day(10))
	mk("ended-recent", day(-380), day(-3))

	names := func(sort string) (out []string) {
		items, _, err := svc.List(ctx, uid, purchases.ListFilter{Sort: sort})
		if err != nil {
			t.Fatalf("list %q: %v", sort, err)
		}
		for _, p := range items {
			out = append(out, p.ProductName)
		}
		return
	}
	want := []string{"soon", "far", "ended-recent", "ended-old", "none"}
	got := names("warrantyEnd")
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("warrantyEnd order: got %v, want %v", got, want)
		}
	}
	if def := names(""); def[0] != "none" || def[len(def)-1] != "ended-old" { // newest purchase first
		t.Fatalf("default order changed: %v", def)
	}
	if _, _, err := svc.List(ctx, uid, purchases.ListFilter{Sort: "price"}); !errors.Is(err, purchases.ErrValidation) {
		t.Fatalf("unknown sort must be a validation error, got %v", err)
	}
}
