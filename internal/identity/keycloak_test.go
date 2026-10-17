package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func fake(t *testing.T, status int) (*Keycloak, *atomic.Int32, *atomic.Int32, *string) {
	t.Helper()
	var tokens, deletes atomic.Int32
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			tokens.Add(1)
			if r.PostFormValue("client_secret") != "s3cret" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"access_token":"tok","expires_in":300}`))
		case r.Method == "DELETE":
			deletes.Add(1)
			path = r.URL.EscapedPath()
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(status)
		}
	}))
	t.Cleanup(srv.Close)
	return &Keycloak{BaseURL: srv.URL, Realm: "returnit", ClientID: "w", ClientSecret: "s3cret"}, &tokens, &deletes, &path
}

func TestDeleteUser(t *testing.T) {
	k, tokens, deletes, path := fake(t, 204)
	for i := 0; i < 3; i++ {
		if err := k.DeleteUser(context.Background(), "abc-123"); err != nil {
			t.Fatal(err)
		}
	}
	if *path != "/admin/realms/returnit/users/abc-123" {
		t.Errorf("path %s", *path)
	}
	if tokens.Load() != 1 || deletes.Load() != 3 {
		t.Errorf("token must be cached: tokens=%d deletes=%d", tokens.Load(), deletes.Load())
	}
}

func TestDeleteUserOutcomes(t *testing.T) {
	if k, _, _, _ := fake(t, 404); k.DeleteUser(context.Background(), "gone") != nil {
		t.Error("404 means already deleted: success")
	}
	k, _, _, _ := fake(t, 500)
	if err := k.DeleteUser(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500 must be an error: %v", err)
	}
	k, _, _, _ = fake(t, 204)
	k.ClientSecret = "wrong"
	if err := k.DeleteUser(context.Background(), "x"); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("bad credentials must fail without echoing the secret: %v", err)
	}
	for _, bad := range []string{"", "a/b", "a?b", "../x", `a\b`} {
		if err := k.DeleteUser(context.Background(), bad); err == nil {
			t.Errorf("subject %q must be refused", bad)
		}
	}
}

func TestSubjectIsPathEscaped(t *testing.T) {
	k, _, _, path := fake(t, 204)
	if err := k.DeleteUser(context.Background(), "a b%2e"); err != nil {
		t.Fatal(err)
	}
	if *path != "/admin/realms/returnit/users/a%20b%252e" {
		t.Errorf("subject not escaped: %s", *path)
	}
}

func TestDeleteUserRetriesOnceWithFreshToken(t *testing.T) {
	var issued, deletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			n := issued.Add(1)
			w.Write([]byte(`{"access_token":"tok` + string(rune('0'+n)) + `","expires_in":300}`))
			return
		}
		deletes.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok2" { // the first token has gone stale at the provider
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)
	k := &Keycloak{BaseURL: srv.URL, Realm: "r", ClientID: "w", ClientSecret: "s"}
	if err := k.DeleteUser(context.Background(), "abc"); err != nil {
		t.Fatalf("a stale cached token must be replaced transparently: %v", err)
	}
	if issued.Load() != 2 || deletes.Load() != 2 {
		t.Errorf("tokens=%d deletes=%d", issued.Load(), deletes.Load())
	}

	// A provider that keeps refusing is reported, not retried forever.
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			w.Write([]byte(`{"access_token":"x","expires_in":300}`))
			return
		}
		w.WriteHeader(401)
	}))
	t.Cleanup(always.Close)
	k2 := &Keycloak{BaseURL: always.URL, Realm: "r", ClientID: "w", ClientSecret: "s"}
	if err := k2.DeleteUser(context.Background(), "abc"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("persistent 401 must surface: %v", err)
	}
}
