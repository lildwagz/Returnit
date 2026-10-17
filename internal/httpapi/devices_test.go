package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/devices"
)

type memDevices struct {
	rows map[uuid.UUID]devices.Device
	own  map[uuid.UUID]uuid.UUID // device -> user
}

func (m *memDevices) Register(_ context.Context, u uuid.UUID, in devices.Input) (devices.Device, error) {
	if in.Platform != "android" && in.Platform != "ios" {
		return devices.Device{}, devices.ErrValidation
	}
	if len(in.PushToken) < 20 {
		return devices.Device{}, devices.ErrValidation
	}
	d := devices.Device{ID: uuid.New(), Platform: in.Platform, CreatedAt: time.Now(), LastSeenAt: time.Now()}
	m.rows[d.ID], m.own[d.ID] = d, u
	return d, nil
}

func (m *memDevices) Unregister(_ context.Context, u, id uuid.UUID) error {
	if m.own[id] != u {
		return devices.ErrNotFound
	}
	delete(m.rows, id)
	delete(m.own, id)
	return nil
}

func TestDevicesEndpoints(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	mem := &memDevices{rows: map[uuid.UUID]devices.Device{}, own: map[uuid.UUID]uuid.UUID{}}
	h := newRouterWith(func(d *Deps) {
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice, "bob": bob}}
		d.Devices = mem
	})
	const body = `{"platform":"android","pushToken":"fcm-token-aaaaaaaaaaaaaaaaaaaa-SECRET"}`

	if w := do(h, "POST", "/api/v1/devices", "", body); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	w := do(h, "POST", "/api/v1/devices", "token-alice", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"platform":"android"`) {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(strings.ToLower(w.Body.String()), "pushtoken") {
		t.Errorf("response must not echo the push token: %s", w.Body)
	}
	id := jsonField(t, w.Body.Bytes(), "id")

	for _, bad := range []string{`{"platform":"windows","pushToken":"fcm-token-aaaaaaaaaaaaaaaaaaaa"}`, `{"platform":"ios","pushToken":"short"}`, `not json`, `{}`} {
		if w := do(h, "POST", "/api/v1/devices", "token-alice", bad); w.Code != 400 {
			t.Errorf("%q: want 400, got %d", bad, w.Code)
		}
	}

	// Another user cannot remove it; it looks like it does not exist.
	if w := do(h, "DELETE", "/api/v1/devices/"+id, "token-bob", ""); w.Code != 404 {
		t.Errorf("cross-user delete: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/v1/devices/not-a-uuid", "token-alice", ""); w.Code != 404 {
		t.Errorf("bad id: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/v1/devices/"+id, "token-alice", ""); w.Code != 204 {
		t.Errorf("delete: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/v1/devices/"+id, "token-alice", ""); w.Code != 404 {
		t.Errorf("second delete: %d", w.Code)
	}
}

func jsonField(t *testing.T, b []byte, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	s, _ := m[name].(string)
	if s == "" {
		t.Fatalf("no %q in %s", name, b)
	}
	return s
}
