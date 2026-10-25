package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/notifications"
)

// Snooze for the in-memory fake: the owner's delivered reminders that belong to a purchase can be repeated.
func (m *memNotifs) Snooze(_ context.Context, u, id uuid.UUID, choice string) (time.Time, error) {
	if _, ok := notifications.SnoozeChoices[choice]; !ok {
		return time.Time{}, notifications.ErrBadSnooze
	}
	r, ok := m.rows[id]
	if !ok || r.owner != u {
		return time.Time{}, notifications.ErrNotFound
	}
	if r.n.PurchaseID == nil {
		return time.Time{}, notifications.ErrSnoozeNotPossible
	}
	r.n.Read = true
	return time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC), nil
}

func TestSnoozeEndpoint(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	pid := uuid.New()
	withPurchase, withoutPurchase := uuid.New(), uuid.New()
	store := &memNotifs{rows: map[uuid.UUID]*memNotif{
		withPurchase:    {owner: alice, n: notifications.Notification{ID: withPurchase, PurchaseID: &pid, Title: "t", Body: "b"}},
		withoutPurchase: {owner: alice, n: notifications.Notification{ID: withoutPurchase, Title: "t", Body: "b"}},
	}}
	users := &fakeUsers{ids: map[string]uuid.UUID{"alice": alice, "bob": bob}}
	h := newRouterWith(func(d *Deps) { d.Users = users; d.Notifications = store })
	path := func(id uuid.UUID) string { return "/api/v1/notifications/" + id.String() + "/snooze" }

	if w := do(h, "POST", path(withPurchase), "", `{"until":"tomorrow"}`); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	w := do(h, "POST", path(withPurchase), "token-alice", `{"until":"tomorrow"}`)
	if w.Code != 200 {
		t.Fatalf("snooze: %d %s", w.Code, w.Body)
	}
	var ok struct {
		ScheduledAt time.Time `json:"scheduledAt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ok); err != nil || ok.ScheduledAt.IsZero() {
		t.Fatalf("body: %s (%v)", w.Body, err)
	}
	if !store.rows[withPurchase].n.Read {
		t.Fatal("the reminder must be marked read")
	}
	for name, c := range map[string]struct {
		id     uuid.UUID
		token  string
		body   string
		status int
		code   string
	}{
		"another user":       {withPurchase, "token-bob", `{"until":"tomorrow"}`, 404, "not_found"},
		"unknown id":         {uuid.New(), "token-alice", `{"until":"tomorrow"}`, 404, "not_found"},
		"bad choice":         {withPurchase, "token-alice", `{"until":"someday"}`, 400, "validation_error"},
		"missing choice":     {withPurchase, "token-alice", `{}`, 400, "validation_error"},
		"not JSON":           {withPurchase, "token-alice", `nope`, 400, "bad_request"},
		"no purchase behind": {withoutPurchase, "token-alice", `{"until":"tomorrow"}`, 422, "snooze_not_possible"},
	} {
		w := do(h, "POST", path(c.id), c.token, c.body)
		var e struct {
			Error struct{ Code string } `json:"error"`
		}
		json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != c.status || e.Error.Code != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, w.Code, w.Body, c.status, c.code)
		}
	}
	if w := do(h, "POST", "/api/v1/notifications/not-a-uuid/snooze", "token-alice", `{"until":"tomorrow"}`); w.Code != 404 {
		t.Fatalf("malformed id: %d", w.Code)
	}
}
