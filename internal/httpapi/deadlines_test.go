package httpapi

import (
	"encoding/json"
	"testing"
)

func TestDeadlinesEndpoint(t *testing.T) {
	h := newTestRouter(fakeDB{})
	if w := do(h, "GET", "/api/v1/deadlines", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	w := do(h, "GET", "/api/v1/deadlines", "token-alice", "")
	if w.Code != 200 {
		t.Fatalf("deadlines: %d %s", w.Code, w.Body)
	}
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Items == nil {
		t.Fatalf("items must be [] and never null: %v %s", err, w.Body)
	}
	for _, q := range []string{"?pastDays=abc", "?pastDays=-1", "?pastDays=91"} {
		if w := do(h, "GET", "/api/v1/deadlines"+q, "token-alice", ""); w.Code != 400 {
			t.Errorf("%s: want 400, got %d", q, w.Code)
		}
	}
	if w := do(h, "GET", "/api/v1/deadlines?pastDays=0", "token-alice", ""); w.Code != 200 {
		t.Errorf("pastDays=0: %d", w.Code)
	}
}
