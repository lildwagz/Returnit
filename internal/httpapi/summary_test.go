package httpapi

import (
	"encoding/json"
	"testing"
)

func TestSummaryEndpoint(t *testing.T) {
	h := newTestRouter(fakeDB{})
	if w := do(h, "GET", "/api/v1/summary", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	for _, name := range []string{"A", "B"} {
		if w := do(h, "POST", "/api/v1/purchases", "token-alice", `{"productName":"`+name+`","purchaseDate":"2026-01-01"}`); w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
	}
	if w := do(h, "POST", "/api/v1/purchases", "token-bob", `{"productName":"C","purchaseDate":"2026-01-01"}`); w.Code != 201 {
		t.Fatalf("create bob: %d", w.Code)
	}

	total := func(token string) (int, map[string]json.RawMessage) {
		w := do(h, "GET", "/api/v1/summary", token, "")
		if w.Code != 200 {
			t.Fatalf("summary: %d %s", w.Code, w.Body)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var counts struct{ Total int }
		if err := json.Unmarshal(body["counts"], &counts); err != nil {
			t.Fatal(err)
		}
		return counts.Total, body
	}
	if n, body := total("token-alice"); n != 2 {
		t.Fatalf("alice sees %d, want 2", n)
	} else {
		// lists are [], never null, and the "next" items are null when there is none
		if string(body["coveredValue"]) != "[]" || string(body["nextExpiring"]) != "null" || string(body["nextReturn"]) != "null" {
			t.Fatalf("shape: %s", body)
		}
	}
	if n, _ := total("token-bob"); n != 1 {
		t.Fatalf("bob sees %d, want 1: another user's purchases must not be counted", n)
	}
}
