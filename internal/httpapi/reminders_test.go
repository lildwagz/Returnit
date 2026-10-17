package httpapi

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/reminders"
)

type memPrefs struct {
	rows map[uuid.UUID]reminders.Preferences
}

func (m *memPrefs) Get(_ context.Context, u uuid.UUID) (reminders.Preferences, bool, error) {
	p, ok := m.rows[u]
	return p, ok, nil
}

func (m *memPrefs) Upsert(_ context.Context, u uuid.UUID, p reminders.Preferences) (reminders.Preferences, error) {
	m.rows[u] = p
	return p, nil
}

func TestReminderPreferences(t *testing.T) {
	h := newTestRouter(fakeDB{})
	const path = "/api/v1/reminder-preferences"

	if w := do(h, "GET", path, "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	// Defaults for a user who never saved anything.
	w := do(h, "GET", path, "token-alice", "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"daysBefore":[30,14,7,3,1]`)) {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}
	// Replace; offsets come back sorted descending.
	body := `{"enabled":true,"daysBefore":[1,30,7],"channel":"in_app","quietHoursStart":"22:00","quietHoursEnd":"07:00","timezone":"Asia/Jakarta"}`
	if w := do(h, "PUT", path, "token-alice", body); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"daysBefore":[30,7,1]`)) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", path, "token-alice", ""); !bytes.Contains(w.Body.Bytes(), []byte(`"channel":"in_app"`)) {
		t.Fatalf("not persisted: %s", w.Body)
	}
	// Preferences are per user.
	if w := do(h, "GET", path, "token-bob", ""); !bytes.Contains(w.Body.Bytes(), []byte(`"channel":"push"`)) {
		t.Fatalf("bob sees alice's prefs: %s", w.Body)
	}
	for name, bad := range map[string]string{
		"offset not allowed": `{"enabled":true,"daysBefore":[5],"channel":"push"}`,
		"duplicate":          `{"enabled":true,"daysBefore":[7,7],"channel":"push"}`,
		"bad channel":        `{"enabled":true,"daysBefore":[7],"channel":"sms"}`,
		"missing fields":     `{"enabled":true}`,
		"empty while on":     `{"enabled":true,"daysBefore":[],"channel":"push"}`,
		"quiet only start":   `{"enabled":true,"daysBefore":[7],"channel":"push","quietHoursStart":"22:00"}`,
		"bad timezone":       `{"enabled":true,"daysBefore":[7],"channel":"push","timezone":"Mars/Olympus"}`,
		"malformed":          `{`,
	} {
		if w := do(h, "PUT", path, "token-alice", bad); w.Code != 400 {
			t.Errorf("%s: code=%d", name, w.Code)
		}
	}
	// No timezone sent -> WIB.
	w = do(h, "PUT", path, "token-bob", `{"enabled":true,"daysBefore":[7],"channel":"push","quietHoursStart":"22:00","quietHoursEnd":"07:00"}`)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"timezone":"Asia/Jakarta"`)) {
		t.Errorf("default tz: %d %s", w.Code, w.Body)
	}
	// Disabling with no offsets is allowed.
	if w := do(h, "PUT", path, "token-alice", `{"enabled":false,"daysBefore":[],"channel":"push"}`); w.Code != 200 {
		t.Errorf("disable: %d %s", w.Code, w.Body)
	}
}
