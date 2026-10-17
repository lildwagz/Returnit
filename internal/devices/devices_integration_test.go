//go:build integration

package devices_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/devices"
	"github.com/returnit/returnit/internal/users"
)

func tokenN(sfx string, n int) string { return fmt.Sprintf("tok-%s-%02d-aaaaaaaaaaaaaaaa", sfx, n) }

func TestDevices(t *testing.T) {
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
	us, svc := users.NewStore(pool), devices.NewService(pool)
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	alice, _ := us.EnsureBySubject(ctx, "da-"+sfx, "da-"+sfx+"@example.com", "A")
	bob, _ := us.EnsureBySubject(ctx, "db-"+sfx, "db-"+sfx+"@example.com", "B")
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{alice, bob})
	})

	// Register is an upsert: the same token twice is one device.
	d1, err := svc.Register(ctx, alice, devices.Input{Platform: "android", PushToken: tokenN(sfx, 1)})
	if err != nil {
		t.Fatal(err)
	}
	d1b, _ := svc.Register(ctx, alice, devices.Input{Platform: "android", PushToken: tokenN(sfx, 1)})
	if d1.ID != d1b.ID {
		t.Error("same token must be the same device")
	}
	if toks, _ := svc.TokensFor(ctx, alice); len(toks) != 1 {
		t.Fatalf("alice tokens: %v", toks)
	}

	// The phone changes hands: the token moves, and alice's reminders no longer reach it.
	if _, err := svc.Register(ctx, bob, devices.Input{Platform: "ios", PushToken: tokenN(sfx, 1)}); err != nil {
		t.Fatal(err)
	}
	if toks, _ := svc.TokensFor(ctx, alice); len(toks) != 0 {
		t.Errorf("token still delivers to the previous user: %v", toks)
	}
	if toks, _ := svc.TokensFor(ctx, bob); len(toks) != 1 {
		t.Errorf("bob tokens: %v", toks)
	}

	// Ownership on unregister.
	if err := svc.Unregister(ctx, alice, d1.ID); !errors.Is(err, devices.ErrNotFound) {
		t.Errorf("alice removed bob's device: %v", err)
	}
	if err := svc.Unregister(ctx, bob, d1.ID); err != nil {
		t.Errorf("owner unregister: %v", err)
	}

	// Cap per user: the oldest are dropped.
	for i := 0; i < devices.MaxPerUser+3; i++ {
		if _, err := svc.Register(ctx, alice, devices.Input{Platform: "android", PushToken: tokenN(sfx, 10+i)}); err != nil {
			t.Fatal(err)
		}
	}
	toks, _ := svc.TokensFor(ctx, alice)
	if len(toks) != devices.MaxPerUser {
		t.Errorf("want cap %d, got %d", devices.MaxPerUser, len(toks))
	}
	if toks[0] != tokenN(sfx, 10+devices.MaxPerUser+2) {
		t.Errorf("newest must survive and come first: %v", toks[0])
	}

	// Dead tokens can be removed, and deleting the user removes the rest.
	if err := svc.RemoveToken(ctx, toks[0]); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice)
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id = $1`, alice).Scan(&n)
	if n != 0 {
		t.Errorf("devices survived their user: %d", n)
	}

	for _, bad := range []devices.Input{{Platform: "web", PushToken: tokenN(sfx, 99)}, {Platform: "ios", PushToken: "short"},
		{Platform: "ios", PushToken: strings.Repeat("x", 5000)}, {Platform: "ios", PushToken: "has space in it aaaaaaaaaaaaaaa"}} {
		if _, err := svc.Register(ctx, bob, bad); !errors.Is(err, devices.ErrValidation) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
}
