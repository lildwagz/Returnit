package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/ratelimit"
	"github.com/returnit/returnit/internal/receipts"
)

// generous leaves every bucket wide open except the ones a test narrows.
func generous() ratelimit.Limits {
	return ratelimit.Limits{UserPerMinute: 1000, UploadPerMinute: 1000, AccountDeletionPerHour: 1000, UnauthPerMinutePerIP: 1000}
}

func status(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	return do(h, method, path, token, "")
}

func TestPerUserLimitReturns429WithHeaders(t *testing.T) {
	lim := generous()
	lim.UserPerMinute = 3
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Limits = lim })

	for i, wantRemaining := range []string{"2", "1", "0"} {
		w := status(h, "GET", "/api/v1/notifications", "token-alice")
		if w.Code != 200 || w.Header().Get("X-RateLimit-Limit") != "3" || w.Header().Get("X-RateLimit-Remaining") != wantRemaining {
			t.Fatalf("call %d: %d limit=%q remaining=%q", i+1, w.Code, w.Header().Get("X-RateLimit-Limit"), w.Header().Get("X-RateLimit-Remaining"))
		}
	}
	w := status(h, "GET", "/api/v1/notifications", "token-alice")
	if w.Code != 429 {
		t.Fatalf("4th call: %d %s", w.Code, w.Body)
	}
	secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "rate_limited" || env.Error.RequestID == "" {
		t.Fatalf("envelope: %s", w.Body)
	}
	// Each user has an independent budget.
	if w := status(h, "GET", "/api/v1/notifications", "token-bob"); w.Code != 200 {
		t.Fatalf("bob was limited by alice's usage: %d", w.Code)
	}
	// The log says which bucket tripped and who (by internal id), at warning level.
	var line map[string]any
	for _, m := range logLines(t, buf) {
		if m["status"] == float64(429) {
			line = m
		}
	}
	if line == nil || line["level"] != "WARN" || !strings.Contains(line["err"].(string), "rate limited: api") || line["user_id"] == nil {
		t.Fatalf("429 not logged usefully: %v", line)
	}
}

func TestDefaultsApplyWhenLimitsAreZero(t *testing.T) {
	h := newRouterWith(func(d *Deps) {}) // zero Limits => 60/min per user
	for i := 1; i <= 60; i++ {
		if w := status(h, "GET", "/api/v1/notifications", "token-alice"); w.Code != 200 {
			t.Fatalf("call %d unexpectedly limited: %d", i, w.Code)
		}
	}
	if w := status(h, "GET", "/api/v1/notifications", "token-alice"); w.Code != 429 {
		t.Fatalf("61st call should hit the default limit of 60/min: %d", w.Code)
	}
}

type stubReceipts struct{ ReceiptService }

func (stubReceipts) MaxBytes() int64 { return 1024 }

func TestUploadHasItsOwnStricterLimit(t *testing.T) {
	lim := generous()
	lim.UploadPerMinute = 2
	h := newRouterWith(func(d *Deps) { d.Limits = lim; d.Receipts = stubReceipts{} })
	path := "/api/v1/purchases/" + uuid.NewString() + "/receipt"

	// No multipart body: the handler answers 400, but each attempt still costs upload budget.
	for i := 1; i <= 2; i++ {
		if w := status(h, "POST", path, "token-alice"); w.Code != 400 {
			t.Fatalf("upload attempt %d: %d %s", i, w.Code, w.Body)
		}
	}
	w := status(h, "POST", path, "token-alice")
	if w.Code != 429 || w.Header().Get("X-RateLimit-Limit") != "2" {
		t.Fatalf("3rd upload: %d limit=%q", w.Code, w.Header().Get("X-RateLimit-Limit"))
	}
	// Everything else is unaffected by the upload bucket, and so are other users.
	if w := status(h, "GET", "/api/v1/notifications", "token-alice"); w.Code != 200 {
		t.Fatalf("other endpoints must not be limited by the upload bucket: %d", w.Code)
	}
	if w := status(h, "POST", path, "token-bob"); w.Code != 400 {
		t.Fatalf("bob's upload budget: %d", w.Code)
	}
}

func TestAccountDeletionIsLimited(t *testing.T) {
	lim := generous()
	lim.AccountDeletionPerHour = 3
	alice := uuid.New()
	svc := &memAccounts{grace: 7 * 24 * time.Hour, now: time.Now(), rows: map[uuid.UUID]*memAccount{
		alice: {a: account.Account{ID: alice, Email: "a@example.com"}},
	}}
	h := newRouterWith(func(d *Deps) {
		d.Limits = lim
		d.Account = svc
		d.Users = &fakeUsers{ids: map[string]uuid.UUID{"alice": alice}}
	})
	for i := 1; i <= 3; i++ {
		if w := status(h, "DELETE", "/api/v1/account", "token-alice"); w.Code != 202 {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
	}
	w := status(h, "DELETE", "/api/v1/account", "token-alice")
	if w.Code != 429 {
		t.Fatalf("4th deletion request: %d", w.Code)
	}
	if secs, _ := strconv.Atoi(w.Header().Get("Retry-After")); secs < 60 {
		t.Fatalf("an hourly bucket should ask for a long wait, got Retry-After=%q", w.Header().Get("Retry-After"))
	}
	// Cancelling (a recovery action) must stay possible.
	if w := status(h, "POST", "/api/v1/account/deletion/cancel", "token-alice"); w.Code != 204 {
		t.Fatalf("cancel must not be blocked by the deletion bucket: %d", w.Code)
	}
}

func req(h http.Handler, token, remote, xff string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/v1/notifications", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if remote != "" {
		r.RemoteAddr = remote
	}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFailedAuthIsLimitedPerIPButValidTrafficIsNeverCharged(t *testing.T) {
	lim := generous()
	lim.UnauthPerMinutePerIP = 3
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Limits = lim })

	// Plenty of authenticated requests from the same IP do not eat the IP's budget (carrier NAT).
	for i := 0; i < 50; i++ {
		if w := req(h, "token-alice", "198.51.100.7:4000", ""); w.Code != 200 {
			t.Fatalf("authenticated call %d: %d", i, w.Code)
		}
	}
	// Missing and invalid tokens both count as failures.
	got := []int{}
	for _, tok := range []string{"", "garbage", "", "garbage"} {
		got = append(got, req(h, tok, "198.51.100.7:4000", "").Code)
	}
	if want := []int{401, 401, 401, 429}; !equalInts(got, want) {
		t.Fatalf("failure sequence %v, want %v", got, want)
	}
	// ...and even a flood of failures does not lock out a legitimate user on that IP.
	if w := req(h, "token-alice", "198.51.100.7:4000", ""); w.Code != 200 {
		t.Fatalf("valid user locked out by failures from the same IP: %d", w.Code)
	}
	// A different IP has its own budget.
	if w := req(h, "", "203.0.113.9:5000", ""); w.Code != 401 {
		t.Fatalf("other IP: %d", w.Code)
	}
	// The raw IP is never logged.
	if strings.Contains(buf.String(), "198.51.100.7") || strings.Contains(buf.String(), "203.0.113.9") {
		t.Fatal("client IP leaked into the log")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSpoofedForwardedForCannotEvadeTheLimit(t *testing.T) {
	lim := generous()
	lim.UnauthPerMinutePerIP = 3
	h := newRouterWith(func(d *Deps) { d.Limits = lim }) // no trusted proxies configured

	codes := []int{}
	for i := 0; i < 5; i++ {
		// A new fake client address on every request.
		codes = append(codes, req(h, "", "198.51.100.7:4000", "10.0.0."+strconv.Itoa(i+1)).Code)
	}
	if want := []int{401, 401, 401, 429, 429}; !equalInts(codes, want) {
		t.Fatalf("spoofing X-Forwarded-For evaded the limit: %v", codes)
	}
}

func TestForwardedForIsHonouredOnlyFromTrustedProxies(t *testing.T) {
	lim := generous()
	lim.UnauthPerMinutePerIP = 2
	h := newRouterWith(func(d *Deps) { d.Limits = lim; d.TrustedProxies = []string{"192.0.2.0/24"} })

	// Traffic arriving from the trusted proxy is attributed to the forwarded client address,
	// so two real clients behind one proxy get separate budgets.
	for i := 0; i < 2; i++ {
		req(h, "", "192.0.2.10:1111", "203.0.113.1")
	}
	if w := req(h, "", "192.0.2.10:1111", "203.0.113.1"); w.Code != 429 {
		t.Fatalf("client 1 should be limited: %d", w.Code)
	}
	if w := req(h, "", "192.0.2.10:1111", "203.0.113.2"); w.Code != 401 {
		t.Fatalf("client 2 behind the same proxy must have its own budget: %d", w.Code)
	}
	// The same header from an untrusted source is ignored.
	for i := 0; i < 2; i++ {
		req(h, "", "198.51.100.50:1111", "203.0.113.77")
	}
	if w := req(h, "", "198.51.100.50:1111", "203.0.113.88"); w.Code != 429 {
		t.Fatalf("untrusted sender changed its identity with X-Forwarded-For: %d", w.Code)
	}
}

func TestHealthChecksAreNeverLimited(t *testing.T) {
	lim := ratelimit.Limits{UserPerMinute: 1, UploadPerMinute: 1, AccountDeletionPerHour: 1, UnauthPerMinutePerIP: 1}
	h := newRouterWith(func(d *Deps) { d.Limits = lim })
	for i := 0; i < 100; i++ {
		if w := status(h, "GET", "/health/live", ""); w.Code != 200 {
			t.Fatalf("probe %d limited: %d", i, w.Code)
		}
	}
}

type erroringLimiter struct{}

func (erroringLimiter) Allow(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{}, errors.New("redis exploded")
}

func TestLimiterFailureFailsOpenAndIsLogged(t *testing.T) {
	h, buf := routerWithLog(slog.LevelInfo, func(d *Deps) { d.Limiter = erroringLimiter{} })
	w := status(h, "GET", "/api/v1/notifications", "token-alice")
	if w.Code != 200 {
		t.Fatalf("a broken limiter store must not take the API down: %d", w.Code)
	}
	if !strings.Contains(buf.String(), "failing open") {
		t.Fatalf("limiter failure should be visible in the log: %s", buf.String())
	}
}

// Compile-time guards that the fakes used above still satisfy the real interfaces.
var (
	_ ReceiptService = stubReceipts{}
	_                = receipts.ErrNotFound
	_ io.Reader      = (*bytes.Buffer)(nil)
)
