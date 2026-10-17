//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/auth"
	"github.com/returnit/returnit/internal/users"
)

type kcTokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}

const kcRedirect = "http://localhost:9/callback"

func kcToken(t *testing.T, issuer string, form url.Values) kcTokens {
	t.Helper()
	form.Set("client_id", "returnit-app")
	resp, err := http.PostForm(issuer+"/protocol/openid-connect/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tk kcTokens
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&tk) != nil || tk.Access == "" {
		t.Fatalf("token endpoint: %d", resp.StatusCode)
	}
	return tk
}

var formAction = regexp.MustCompile(`action="([^"]+)"`)

// browserLogin does what a real app does: the authorization-code flow through Keycloak's login page.
// The same cookie jar is one browser, so a second call without prompt=login reuses its SSO session.
func browserLogin(t *testing.T, issuer string, jar http.CookieJar, user, pass, prompt string) kcTokens {
	t.Helper()
	cl := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"response_type": {"code"}, "client_id": {"returnit-app"}, "redirect_uri": {kcRedirect},
		"scope": {"openid"}, "state": {"s"}}
	if prompt != "" {
		q.Set("prompt", prompt)
	}
	resp, err := cl.Get(issuer + "/protocol/openid-connect/auth?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == 200 { // login page: submit the form
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := formAction.FindStringSubmatch(string(b))
		if m == nil {
			t.Fatal("login form not found")
		}
		resp, err = cl.PostForm(html.UnescapeString(m[1]), url.Values{"username": {user}, "password": {pass}})
		if err != nil {
			t.Fatal(err)
		}
	}
	resp.Body.Close()
	loc, err := resp.Location()
	if err != nil || loc.Query().Get("code") == "" {
		t.Fatalf("no authorization code (status %d, location %v)", resp.StatusCode, loc)
	}
	return kcToken(t, issuer, url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "redirect_uri": {kcRedirect}})
}

func newJar(t *testing.T) http.CookieJar {
	j, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// TestRealOIDCAndFreshLogin runs the API against real Keycloak tokens: real signatures, issuer,
// audience and the auth_time claim, not fakes.
func TestRealOIDCAndFreshLogin(t *testing.T) {
	issuer, dbURL := os.Getenv("TEST_OIDC_ISSUER"), os.Getenv("TEST_DATABASE_URL")
	if issuer == "" || dbURL == "" {
		t.Skip("TEST_OIDC_ISSUER / TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE email IN ('alice@example.com','bob@example.com')`)
		pool.Exec(context.Background(), `DELETE FROM account_emails WHERE to_address IN ('alice@example.com','bob@example.com')`)
	})

	verifier, err := auth.NewOIDC(ctx, issuer, "returnit-api")
	if err != nil {
		t.Fatal(err)
	}
	h := newRouterWith(func(d *Deps) {
		d.Verifier = verifier
		d.Users = users.NewStore(pool)
		d.Account = account.NewService(pool, 7*24*time.Hour)
		d.FreshLoginMaxAge = 3 * time.Second
	})

	// --- real login ---
	if w := do(h, "GET", "/api/v1/account", "", ""); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	aliceJar := newJar(t)
	alice := browserLogin(t, issuer, aliceJar, "alice", "alice-dev-pass", "")
	w := do(h, "GET", "/api/v1/account", alice.Access, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alice@example.com") {
		t.Fatalf("real token rejected: %d %s", w.Code, w.Body)
	}
	// Tampered signature must fail.
	if w := do(h, "GET", "/api/v1/account", alice.Access[:len(alice.Access)-4]+"AAAA", ""); w.Code != 401 {
		t.Errorf("tampered token accepted: %d", w.Code)
	}
	// A verifier that expects another audience must refuse this (otherwise tokens minted for
	// some other API could be replayed here).
	other, err := auth.NewOIDC(ctx, issuer, "some-other-api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(ctx, alice.Access); err == nil {
		t.Error("token for a different audience accepted")
	}
	// Two real users stay separate.
	bob := browserLogin(t, issuer, newJar(t), "bob", "bob-dev-pass", "")
	if w := do(h, "GET", "/api/v1/account", bob.Access, ""); w.Code != 200 || strings.Contains(w.Body.String(), "alice@") {
		t.Errorf("bob sees: %d %s", w.Code, w.Body)
	}

	// --- fresh login ---
	claims, err := verifier.Verify(ctx, alice.Access)
	if err != nil || claims.AuthTime.IsZero() || time.Since(claims.AuthTime) > time.Minute {
		t.Fatalf("auth_time missing or wrong in a real token: %v %v", claims.AuthTime, err)
	}
	if w := do(h, "DELETE", "/api/v1/account", alice.Access, ""); w.Code != 202 {
		t.Fatalf("delete with just-logged-in token: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/v1/account/deletion/cancel", alice.Access, ""); w.Code != 204 {
		t.Fatalf("cancel: %d", w.Code)
	}

	time.Sleep(4 * time.Second) // login is now older than FreshLoginMaxAge; token itself still valid
	if w := do(h, "GET", "/api/v1/account", alice.Access, ""); w.Code != 200 {
		t.Fatalf("token should still be valid: %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/v1/account", alice.Access, ""); w.Code != 401 || !strings.Contains(w.Body.String(), "reauthentication_required") {
		t.Fatalf("stale login must be refused: %d %s", w.Code, w.Body)
	}
	// Refreshing the token must NOT count as logging in again.
	refreshed := kcToken(t, issuer, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {alice.Refresh}})
	if w := do(h, "DELETE", "/api/v1/account", refreshed.Access, ""); w.Code != 401 {
		t.Fatalf("refreshed token passed the fresh-login check: %d %s", w.Code, w.Body)
	}
	// Neither does silently reusing the browser's SSO session (no password asked).
	silent := browserLogin(t, issuer, aliceJar, "", "", "")
	if w := do(h, "DELETE", "/api/v1/account", silent.Access, ""); w.Code != 401 {
		t.Fatalf("SSO-reused session passed the fresh-login check: %d %s", w.Code, w.Body)
	}
	// Only a real new login does: the app sends the user back with prompt=login.
	again := browserLogin(t, issuer, aliceJar, "alice", "alice-dev-pass", "login")
	if w := do(h, "DELETE", "/api/v1/account", again.Access, ""); w.Code != 202 {
		t.Fatalf("after re-login: %d %s", w.Code, w.Body)
	}
}
