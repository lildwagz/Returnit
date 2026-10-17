//go:build integration

package users_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/users"
)

func TestEnsureBySubject(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration env not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := users.NewStore(pool)
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	email := "u-" + sfx + "@example.com"
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email) })

	id1, err := s.EnsureBySubject(ctx, "sub-a-"+sfx, email, "Person")
	if err != nil {
		t.Fatal(err)
	}
	// The same login again is the same user, whatever the e-mail casing.
	id1b, err := s.EnsureBySubject(ctx, "sub-a-"+sfx, strings.ToUpper(email), "Person")
	if err != nil || id1 != id1b {
		t.Fatalf("same identity must map to the same user: %v %v %v", id1, id1b, err)
	}
	// A different login claiming the same e-mail is a conflict, never a merge and never an internal error.
	for _, e := range []string{email, strings.ToUpper(email)} {
		if _, err := s.EnsureBySubject(ctx, "sub-b-"+sfx, e, "Impostor"); !errors.Is(err, users.ErrIdentityConflict) {
			t.Errorf("e-mail %q: want ErrIdentityConflict, got %v", e, err)
		}
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, email).Scan(&n)
	if n != 1 {
		t.Errorf("a conflicting login must not create or merge users: %d rows", n)
	}
}
