package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/users"
)

// memAccounts models the account lifecycle with the same rules as account.Service.
type memAccounts struct {
	rows  map[uuid.UUID]*memAccount
	grace time.Duration
	now   time.Time
}
type memAccount struct {
	a account.Account
}

func (m *memAccounts) Get(_ context.Context, u uuid.UUID) (account.Account, error) {
	r, ok := m.rows[u]
	if !ok {
		return account.Account{}, account.ErrNotFound
	}
	return r.a, nil
}

func (m *memAccounts) RequestDeletion(_ context.Context, u uuid.UUID) (time.Time, error) {
	r, ok := m.rows[u]
	if !ok {
		return time.Time{}, account.ErrNotFound
	}
	if r.a.DeletionScheduledFor == nil { // idempotent: first request wins
		at := m.now.Add(m.grace)
		r.a.DeletionScheduledFor = &at
	}
	return *r.a.DeletionScheduledFor, nil
}

func (m *memAccounts) CancelDeletion(_ context.Context, u uuid.UUID) error {
	r, ok := m.rows[u]
	if !ok {
		return account.ErrNotFound
	}
	if r.a.DeletionScheduledFor != nil && !r.a.DeletionScheduledFor.After(m.now) {
		return account.ErrTooLate
	}
	r.a.DeletionScheduledFor = nil
	return nil
}

func TestAccountEndpoints(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	svc := &memAccounts{grace: 7 * 24 * time.Hour, now: now, rows: map[uuid.UUID]*memAccount{
		alice: {a: account.Account{ID: alice, Email: "alice@example.com", CreatedAt: now}},
		bob:   {a: account.Account{ID: bob, Email: "bob@example.com", CreatedAt: now}},
	}}
	users := &fakeUsers{ids: map[string]uuid.UUID{"alice": alice, "bob": bob}}
	h := newRouterWith(func(d *Deps) { d.Users = users; d.Account = svc })

	for _, tc := range [][2]string{{"GET", "/api/v1/account"}, {"DELETE", "/api/v1/account"}, {"POST", "/api/v1/account/deletion/cancel"}} {
		if w := do(h, tc[0], tc[1], "", ""); w.Code != 401 {
			t.Errorf("%s %s unauthenticated: %d", tc[0], tc[1], w.Code)
		}
	}

	w := do(h, "GET", "/api/v1/account", "token-alice", "")
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["email"] != "alice@example.com" || got["deletionScheduledFor"] != nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}

	// Request deletion: 202 with the schedule = now + 7 days.
	w = do(h, "DELETE", "/api/v1/account", "token-alice", "")
	json.Unmarshal(w.Body.Bytes(), &got)
	want := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	if w.Code != 202 || got["status"] != "deletion_scheduled" || got["deletionScheduledFor"] != want {
		t.Fatalf("delete: %d %s (want schedule %s)", w.Code, w.Body, want)
	}
	// Asking again must not move the date.
	svc.now = now.Add(48 * time.Hour)
	w = do(h, "DELETE", "/api/v1/account", "token-alice", "")
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 202 || got["deletionScheduledFor"] != want {
		t.Fatalf("repeat request moved the schedule: %s", w.Body)
	}
	// GET now shows it; Bob is unaffected.
	if w := do(h, "GET", "/api/v1/account", "token-alice", ""); !strings.Contains(w.Body.String(), want) {
		t.Fatalf("get should show the schedule: %s", w.Body)
	}
	if w := do(h, "GET", "/api/v1/account", "token-bob", ""); !strings.Contains(w.Body.String(), `"deletionScheduledFor":null`) {
		t.Fatalf("bob affected: %s", w.Body)
	}

	// Cancel inside the grace period, and cancelling again is harmless.
	for i := 0; i < 2; i++ {
		if w := do(h, "POST", "/api/v1/account/deletion/cancel", "token-alice", ""); w.Code != 204 {
			t.Fatalf("cancel #%d: %d %s", i+1, w.Code, w.Body)
		}
	}
	if w := do(h, "GET", "/api/v1/account", "token-alice", ""); !strings.Contains(w.Body.String(), `"deletionScheduledFor":null`) {
		t.Fatalf("cancel did not clear the schedule: %s", w.Body)
	}

	// After the grace period has ended, cancelling is refused with 409.
	svc.now = now
	do(h, "DELETE", "/api/v1/account", "token-alice", "")
	svc.now = now.Add(8 * 24 * time.Hour)
	if w := do(h, "POST", "/api/v1/account/deletion/cancel", "token-alice", ""); w.Code != 409 {
		t.Fatalf("late cancel: %d %s", w.Code, w.Body)
	}
}

type failingAccounts struct{ AccountService }

func (failingAccounts) RequestDeletion(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, errors.New("pq: connection refused db-host-3")
}

func TestAccountDeletionFailureIsLoggedNotLeaked(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Account = failingAccounts{} })
	w := do(h, "DELETE", "/api/v1/account", "token-alice", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "db-host-3") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(buf.String(), "db-host-3") {
		t.Fatal("cause must be in the log")
	}
}

func TestAccountDeletionNeedsFreshLogin(t *testing.T) {
	alice := uuid.New()
	now := time.Now()
	svc := &memAccounts{grace: 7 * 24 * time.Hour, now: now, rows: map[uuid.UUID]*memAccount{
		alice: {a: account.Account{ID: alice, Email: "alice@example.com", CreatedAt: now}},
	}}
	h := newRouterWith(func(d *Deps) {
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice}}
		d.Account = svc
	})
	for _, tok := range []string{"old-alice", "noauth-alice"} {
		w := do(h, "DELETE", "/api/v1/account", tok, "")
		if w.Code != 401 || !strings.Contains(w.Body.String(), "reauthentication_required") {
			t.Errorf("%s: want 401 reauthentication_required, got %d %s", tok, w.Code, w.Body)
		}
	}
	if svc.rows[alice].a.DeletionScheduledFor != nil {
		t.Fatal("a stale login must not schedule deletion")
	}
	// Other account endpoints do not demand a fresh login.
	if w := do(h, "GET", "/api/v1/account", "old-alice", ""); w.Code != 200 {
		t.Errorf("GET with old login: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/v1/account", "token-alice", ""); w.Code != 202 {
		t.Errorf("fresh login: %d %s", w.Code, w.Body)
	}
}

type conflictUsers struct{}

func (conflictUsers) EnsureBySubject(context.Context, string, string, string) (uuid.UUID, error) {
	return uuid.Nil, users.ErrIdentityConflict
}

func TestIdentityConflictIsAClearError(t *testing.T) {
	h := newRouterWith(func(d *Deps) { d.Users = conflictUsers{} })
	w := do(h, "GET", "/api/v1/account", "token-alice", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "identity_conflict") {
		t.Fatalf("want 409 identity_conflict, got %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "alice") {
		t.Errorf("the error must not echo identity details: %s", w.Body)
	}
}
