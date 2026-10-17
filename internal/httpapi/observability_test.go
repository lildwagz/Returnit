package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/logging"
	"github.com/returnit/returnit/internal/purchases"
)

// failingPurchases makes every service call fail (or panic) so we can inspect the log.
type failingPurchases struct {
	PurchaseService
	err   error
	panic bool
}

func (f failingPurchases) Get(context.Context, uuid.UUID, uuid.UUID) (purchases.Purchase, error) {
	if f.panic {
		panic("kaboom: index out of range")
	}
	return purchases.Purchase{}, f.err
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("not JSON: %q", ln)
		}
		out = append(out, m)
	}
	return out
}

func requestLine(t *testing.T, lines []map[string]any) map[string]any {
	t.Helper()
	for _, m := range lines {
		if m["msg"] == "request" {
			return m
		}
	}
	t.Fatalf("no request line in %v", lines)
	return nil
}

func routerWithLog(level slog.Level, mod func(*Deps)) (http.Handler, *bytes.Buffer) {
	var buf bytes.Buffer
	log := logging.New(&buf, "web", level)
	return newRouterWith(func(d *Deps) {
		d.Logger = log
		if mod != nil {
			mod(d)
		}
	}), &buf
}

func TestRequestLineHasUsefulFieldsAndNoSensitiveData(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, nil)
	const secret = "Bearer-token-super-secret"

	w := do(h, "POST", "/api/v1/purchases", "token-alice", `{"productName":"Passport Laptop","purchaseDate":"2026-01-31"}`)
	var created struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &created)
	buf.Reset()

	req := httptest.NewRequest("GET", "/api/v1/purchases/"+created.ID+"?q=my-private-search", nil)
	req.Header.Set("Authorization", "Bearer token-alice")
	req.Header.Set("X-Secret", secret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	m := requestLine(t, logLines(t, buf))
	if m["service"] != "web" || m["method"] != "GET" || m["status"] != float64(200) || m["level"] != "INFO" {
		t.Fatalf("unexpected request line: %v", m)
	}
	// Route pattern, not the raw path: no resource IDs, no query string.
	if m["route"] != "/api/v1/purchases/:id" {
		t.Errorf("route = %v", m["route"])
	}
	if m["request_id"] != rec.Header().Get("X-Request-ID") || m["request_id"] == "" {
		t.Errorf("request_id %v does not match response header %q", m["request_id"], rec.Header().Get("X-Request-ID"))
	}
	if _, ok := m["latency_ms"].(float64); !ok {
		t.Errorf("latency_ms missing: %v", m)
	}
	if _, err := uuid.Parse(m["user_id"].(string)); err != nil {
		t.Errorf("user_id should be the internal UUID: %v", m["user_id"])
	}
	for _, leak := range []string{"my-private-search", created.ID, "Passport", "Laptop", "token-alice", secret, "alice@example.com", "Authorization"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("log leaks %q: %s", leak, buf.String())
		}
	}
}

func TestInternalErrorsAreLoggedButNeverSentToClient(t *testing.T) {
	failing := failingPurchases{err: errors.New("pq: connection refused to db-host-7")}
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Purchases = failing })

	w := do(h, "GET", "/api/v1/purchases/"+uuid.NewString(), "token-alice", "")
	if w.Code != 500 {
		t.Fatalf("code=%d", w.Code)
	}
	if strings.Contains(w.Body.String(), "db-host-7") || strings.Contains(w.Body.String(), "connection refused") {
		t.Fatalf("internal error leaked to client: %s", w.Body)
	}
	m := requestLine(t, logLines(t, buf))
	if m["level"] != "ERROR" || m["status"] != float64(500) || !strings.Contains(m["err"].(string), "db-host-7") {
		t.Fatalf("500 must be logged at error level with its cause: %v", m)
	}
}

func TestPanicIsRecoveredAndLogged(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Purchases = failingPurchases{panic: true} })

	w := do(h, "GET", "/api/v1/purchases/"+uuid.NewString(), "token-alice", "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal"`) {
		t.Fatalf("panic must become the standard 500 envelope: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "kaboom") || strings.Contains(w.Body.String(), "goroutine") {
		t.Fatalf("panic details leaked to client: %s", w.Body)
	}
	lines := logLines(t, buf)
	var panicLine map[string]any
	for _, m := range lines {
		if m["msg"] == "panic recovered" {
			panicLine = m
		}
	}
	if panicLine == nil || !strings.Contains(panicLine["panic"].(string), "kaboom") || !strings.Contains(panicLine["stack"].(string), "goroutine") {
		t.Fatalf("panic with stack not logged: %v", lines)
	}
	if panicLine["request_id"] == "" || panicLine["request_id"] != requestLine(t, lines)["request_id"] {
		t.Error("panic line must carry the request id")
	}
	if requestLine(t, lines)["status"] != float64(500) {
		t.Error("request line must record the 500")
	}
}

func TestRequestIDHandling(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, nil)
	send := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/health/live", nil)
		req.Header.Set("X-Request-ID", id)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if got := send("gateway-abc_123.4").Header().Get("X-Request-ID"); got != "gateway-abc_123.4" {
		t.Errorf("valid inbound id should be kept, got %q", got)
	}
	for _, bad := range []string{strings.Repeat("a", 65), "has space", "semi;colon", "<script>"} {
		got := send(bad).Header().Get("X-Request-ID")
		if got == bad {
			t.Errorf("unsafe id %q was trusted", bad)
		}
		if _, err := uuid.Parse(got); err != nil {
			t.Errorf("replacement for %q should be a UUID, got %q", bad, got)
		}
	}
	_ = buf
}

func TestUnauthorizedAndUnmatchedLogging(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, nil)

	req := httptest.NewRequest("GET", "/api/v1/purchases", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token-xyz")
	h.ServeHTTP(httptest.NewRecorder(), req)
	m := requestLine(t, logLines(t, buf))
	if m["status"] != float64(401) || m["level"] != "WARN" || m["err"] == nil {
		t.Fatalf("401 should be a warning that records the reason: %v", m)
	}
	if strings.Contains(buf.String(), "not-a-valid-token-xyz") {
		t.Fatalf("token leaked into the log: %s", buf.String())
	}

	buf.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/wp-admin/../secret-path?x=1", nil))
	m = requestLine(t, logLines(t, buf))
	if m["route"] != "unmatched" || strings.Contains(buf.String(), "secret-path") {
		t.Fatalf("arbitrary client paths must not be logged: %v", m)
	}
}

func TestHealthChecksAreDebugOnly(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, nil)
	do(h, "GET", "/health/live", "", "")
	do(h, "GET", "/health/ready", "", "")
	if buf.Len() != 0 {
		t.Fatalf("probes must not log at info level: %s", buf.String())
	}
	h, buf = routerWithLog(slog.LevelDebug, nil)
	do(h, "GET", "/health/live", "", "")
	if requestLine(t, logLines(t, buf))["level"] != "DEBUG" {
		t.Fatal("probe should be logged at debug level")
	}
}
