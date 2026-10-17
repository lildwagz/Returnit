package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/notifications"
)

type memNotifs struct {
	rows map[uuid.UUID]*memNotif
}
type memNotif struct {
	owner uuid.UUID
	n     notifications.Notification
}

func (m *memNotifs) List(_ context.Context, u uuid.UUID, unreadOnly bool, limit int) ([]notifications.Notification, error) {
	out := []notifications.Notification{}
	for _, r := range m.rows {
		if r.owner == u && (!unreadOnly || !r.n.Read) {
			out = append(out, r.n)
		}
	}
	return out, nil
}

func (m *memNotifs) MarkRead(_ context.Context, u, id uuid.UUID) error {
	r, ok := m.rows[id]
	if !ok || r.owner != u {
		return notifications.ErrNotFound
	}
	r.n.Read = true
	return nil
}

func TestNotificationEndpoints(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	id := uuid.New()
	store := &memNotifs{rows: map[uuid.UUID]*memNotif{
		id: {owner: alice, n: notifications.Notification{ID: id, Title: "t", Body: "b", CreatedAt: time.Now()}},
	}}
	users := &fakeUsers{ids: map[string]uuid.UUID{"alice": alice, "bob": bob}}
	h := newRouterWith(func(d *Deps) { d.Users = users; d.Notifications = store })

	if w := do(h, "GET", "/api/v1/notifications", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := do(h, "GET", "/api/v1/notifications", "token-alice", ""); w.Code != 200 || w.Body.Len() < 10 {
		t.Fatalf("alice list: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/api/v1/notifications", "token-bob", ""); w.Body.String() != "[]" {
		t.Fatalf("bob must see nothing, got %s", w.Body)
	}
	for _, q := range []string{"?unreadOnly=maybe", "?limit=0", "?limit=abc"} {
		if w := do(h, "GET", "/api/v1/notifications"+q, "token-alice", ""); w.Code != 400 {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
	path := "/api/v1/notifications/" + id.String() + "/read"
	if w := do(h, "POST", path, "token-bob", ""); w.Code != 404 {
		t.Fatalf("bob mark-read must 404, got %d", w.Code)
	}
	if store.rows[id].n.Read {
		t.Fatal("bob changed alice's notification")
	}
	if w := do(h, "POST", path, "token-alice", ""); w.Code != 204 {
		t.Fatalf("owner mark-read: %d", w.Code)
	}
	if w := do(h, "POST", path, "token-alice", ""); w.Code != 204 {
		t.Fatalf("mark-read must be idempotent: %d", w.Code)
	}
	if w := do(h, "POST", "/api/v1/notifications/not-a-uuid/read", "token-alice", ""); w.Code != 404 {
		t.Fatalf("bad id: %d", w.Code)
	}
}
