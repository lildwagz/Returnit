package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/account"
)

func (m *memAccounts) Export(_ context.Context, u uuid.UUID, w io.Writer) error {
	r, ok := m.rows[u]
	if !ok {
		return account.ErrNotFound
	}
	_, err := io.WriteString(w, `{"format":"returnit-export/1","account":{"email":"`+r.a.Email+`"}}`+"\n")
	return err
}

func exportRouter(t *testing.T, mod func(*Deps)) (alice, bob uuid.UUID, h func(method, path, token string) (int, string, map[string][]string)) {
	t.Helper()
	alice, bob = uuid.New(), uuid.New()
	svc := &memAccounts{grace: 7 * 24 * time.Hour, now: time.Now(), rows: map[uuid.UUID]*memAccount{
		alice: {a: account.Account{ID: alice, Email: "alice@example.com"}},
		bob:   {a: account.Account{ID: bob, Email: "bob@example.com"}},
	}}
	router := newRouterWith(func(d *Deps) {
		d.Account = svc
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice, "bob": bob}}
		if mod != nil {
			mod(d)
		}
	})
	return alice, bob, func(method, path, token string) (int, string, map[string][]string) {
		w := do(router, method, path, token, "")
		return w.Code, w.Body.String(), w.Header()
	}
}

func TestExportEndpoint(t *testing.T) {
	_, _, call := exportRouter(t, nil)

	if code, _, _ := call("GET", "/api/v1/account/export", ""); code != 401 {
		t.Fatalf("unauthenticated export: %d", code)
	}
	code, body, hdr := call("GET", "/api/v1/account/export", "token-alice")
	if code != 200 || !strings.Contains(body, "alice@example.com") || strings.Contains(body, "bob@example.com") {
		t.Fatalf("export: %d %s", code, body)
	}
	get := func(k string) string { return strings.Join(hdr[k], ",") }
	if !strings.HasPrefix(get("Content-Type"), "application/json") ||
		!strings.HasPrefix(get("Content-Disposition"), `attachment; filename="returnit-export-`) ||
		get("Cache-Control") != "no-store" || get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download headers wrong: %v", hdr)
	}
}

func TestExportIsRateLimited(t *testing.T) {
	_, _, call := exportRouter(t, func(d *Deps) {
		d.Limits = generous()
		d.Limits.ExportPerHour = 2
	})
	for i := 1; i <= 2; i++ {
		if code, _, _ := call("GET", "/api/v1/account/export", "token-alice"); code != 200 {
			t.Fatalf("export %d: %d", i, code)
		}
	}
	code, _, hdr := call("GET", "/api/v1/account/export", "token-alice")
	if code != 429 || len(hdr["Retry-After"]) == 0 {
		t.Fatalf("3rd export: %d %v", code, hdr)
	}
	if code, _, _ := call("GET", "/api/v1/account/export", "token-bob"); code != 200 {
		t.Fatalf("bob shares alice's export budget: %d", code)
	}
}

type failBeforeWrite struct{ AccountService }

func (failBeforeWrite) Export(context.Context, uuid.UUID, io.Writer) error {
	return errors.New("pq: connection refused db-host-9")
}

func TestExportFailureBeforeAnyOutputIsAProperError(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Account = failBeforeWrite{} })
	w := do(h, "GET", "/api/v1/account/export", "token-alice", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "db-host-9") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("an error response must not look like a file download: %q", got)
	}
	if !strings.Contains(buf.String(), "db-host-9") {
		t.Fatal("cause must be logged")
	}
}

type failMidStream struct{ AccountService }

func (failMidStream) Export(_ context.Context, _ uuid.UUID, w io.Writer) error {
	io.WriteString(w, `{"format":"returnit-export/1","purchases":[`)
	return errors.New("connection reset mid-export")
}

func TestExportFailureMidStreamIsLoggedAndLeavesInvalidJSON(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Account = failMidStream{} })
	w := do(h, "GET", "/api/v1/account/export", "token-alice", "")
	if strings.HasSuffix(strings.TrimSpace(w.Body.String()), "}") {
		t.Fatalf("a truncated export must not look complete: %q", w.Body.String())
	}
	if !strings.Contains(buf.String(), "connection reset mid-export") {
		t.Fatal("mid-stream failure must be logged")
	}
}
