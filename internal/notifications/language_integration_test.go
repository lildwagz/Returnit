//go:build integration

package notifications_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/users"
)

// Changing the language rewrites the person's existing reminders (sent and still waiting) and leaves other
// people's alone; repeating the same language changes nothing.
func TestSetLanguageRewritesReminders(t *testing.T) {
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
	mk := func(name string) uuid.UUID {
		id, err := users.NewStore(pool).EnsureBySubject(ctx, name+sfx, name+sfx+"@example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	alice, bob := mk("lang-a-"), mk("lang-b-")
	seed := func(u uuid.UUID, key string) uuid.UUID {
		var pid, nid uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
			VALUES ($1,'Laptop','2026-01-01','2026-12-01') RETURNING id`, u).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, status, sent_at, idempotency_key, channel, offset_days, warranty_end_date, kind)
			VALUES ($1,$2,'Garansi segera berakhir','Garansi Laptop berakhir besok (2026-12-01).','sent',now(),$3,'in_app',1,'2026-12-01','warranty') RETURNING id`,
			u, pid, key+sfx).Scan(&nid); err != nil {
			t.Fatal(err)
		}
		return nid
	}
	na, nb := seed(alice, "a-"), seed(bob, "b-")
	title := func(id uuid.UUID) (s string) {
		pool.QueryRow(ctx, `SELECT title FROM notifications WHERE id = $1`, id).Scan(&s)
		return
	}

	svc := &notifications.LanguageService{Pool: pool}
	if err := svc.Set(ctx, alice, "en"); err != nil {
		t.Fatal(err)
	}
	if got := title(na); got != "Warranty ending soon" {
		t.Fatalf("alice's reminder: %q", got)
	}
	var body string
	pool.QueryRow(ctx, `SELECT body FROM notifications WHERE id = $1`, na).Scan(&body)
	if body != "The warranty for Laptop ends tomorrow (2026-12-01)." {
		t.Fatalf("alice's body: %q", body)
	}
	if got := title(nb); got != "Garansi segera berakhir" {
		t.Fatalf("bob's reminder must not change: %q", got)
	}
	if err := svc.Set(ctx, alice, "en"); err != nil {
		t.Fatalf("repeating the same language: %v", err)
	}
	if err := svc.Set(ctx, alice, "fr"); !errors.Is(err, notifications.ErrLanguage) {
		t.Fatalf("unsupported language: %v", err)
	}
	if err := svc.Set(ctx, alice, "id"); err != nil {
		t.Fatal(err)
	}
	if got := title(na); got != "Garansi segera berakhir" {
		t.Fatalf("back to Indonesian: %q", got)
	}
}
