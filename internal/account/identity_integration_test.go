//go:build integration

package account_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/identity"
	"github.com/returnit/returnit/internal/users"
)

// passwordLogin returns the HTTP status of a direct-grant login (dev realm only) and, on success,
// the token's subject.
func passwordLogin(t *testing.T, base, realm, user, pass string) (int, string) {
	t.Helper()
	resp, err := http.PostForm(base+"/realms/"+realm+"/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"}, "client_id": {"returnit-app"}, "username": {user}, "password": {pass}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tk struct {
		Access string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tk)
	return resp.StatusCode, tk.Access
}

// Needs migrated Postgres and the dev Keycloak (docker compose --profile auth up -d keycloak).
func TestIdentityErasedWithAccount(t *testing.T) {
	dbURL, base := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KEYCLOAK_URL")
	if dbURL == "" || base == "" {
		t.Skip("integration env not set")
	}
	const realm = "returnit"
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	kc := &identity.Keycloak{BaseURL: base, Realm: realm, ClientID: "returnit-worker", ClientSecret: "dev-worker-secret"}

	// A throwaway identity, created through the admin API (so the dev users alice/bob stay intact).
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	username, pass := "erase-"+sfx, "pw-"+sfx
	adminTok, err := kcAdminToken(base)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"username": username, "email": username + "@example.com", "enabled": true, "emailVerified": true,
		"firstName": "E", "lastName": "R", "credentials": []map[string]any{{"type": "password", "value": pass, "temporary": false}}})
	req, _ := http.NewRequest("POST", base+"/admin/realms/"+realm+"/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("create identity: %v %v", err, resp)
	}
	resp.Body.Close()
	code, access := passwordLogin(t, base, realm, username, pass)
	if code != 200 {
		t.Fatalf("new identity cannot log in: %d", code)
	}
	subject := jwtSubject(t, access)
	t.Cleanup(func() { kc.DeleteUser(context.Background(), subject) })

	// The app user, linked to that identity by subject, then deletion and erasure.
	uid, err := users.NewStore(pool).EnsureBySubject(ctx, subject, username+"@example.com", "Erase Me")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid)
		pool.Exec(context.Background(), `DELETE FROM account_deletion_log WHERE user_id = $1`, uid)
		pool.Exec(context.Background(), `DELETE FROM account_emails WHERE user_id = $1`, uid)
		pool.Exec(context.Background(), `DELETE FROM identity_deletions WHERE subject = $1`, subject)
	})
	clock := time.Now().UTC()
	svc := account.NewService(pool, 7*24*time.Hour)
	svc.Now = func() time.Time { return clock }
	if _, err := svc.RequestDeletion(ctx, uid); err != nil {
		t.Fatal(err)
	}
	eraser := &account.IdentityEraser{Pool: pool, Provider: kc, Log: log, Now: func() time.Time { return clock }}

	// While the account only *awaits* erasure the identity must stay.
	// (EraseDue is global, so other tests' leftovers may be processed: look at this subject only.)
	if count(t, pool, `SELECT count(*) FROM identity_deletions WHERE subject = $1`, subject) != 0 {
		t.Fatal("identity queued before the account was erased")
	}
	if _, err := eraser.EraseDue(ctx); err != nil {
		t.Fatal(err)
	}
	if code, _ := passwordLogin(t, base, realm, username, pass); code != 200 {
		t.Fatal("identity vanished during the grace period")
	}

	// Erasure queues the identity (same transaction as the account) and the worker removes it.
	clock = clock.Add(8 * 24 * time.Hour)
	if n, err := svc.PurgeDue(ctx); err != nil || n < 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if count(t, pool, `SELECT count(*) FROM identity_deletions WHERE subject = $1`, subject) != 1 {
		t.Fatal("identity deletion not queued")
	}
	if n, err := eraser.EraseDue(ctx); err != nil || n < 1 {
		t.Fatalf("erase identity: %d %v", n, err)
	}
	if code, _ := passwordLogin(t, base, realm, username, pass); code == 200 {
		t.Fatal("erased user can still log in at the identity provider")
	}
	if count(t, pool, `SELECT count(*) FROM identity_deletions WHERE subject = $1`, subject) != 0 {
		t.Error("subject still stored after the identity was deleted")
	}

	// Idempotent: an identity that is already gone counts as deleted.
	if err := kc.DeleteUser(ctx, subject); err != nil {
		t.Errorf("deleting a missing identity must succeed: %v", err)
	}

	// Failures keep the row for a retry and never put the subject into the error text.
	pool.Exec(ctx, `INSERT INTO identity_deletions (subject) VALUES ($1)`, subject)
	bad := &account.IdentityEraser{Pool: pool, Log: log, Now: func() time.Time { return clock },
		Provider: &identity.Keycloak{BaseURL: base, Realm: realm, ClientID: "returnit-worker", ClientSecret: "wrong"}}
	if n, _ := bad.EraseDue(ctx); n != 0 {
		t.Fatal("wrong credentials must not count as success")
	}
	var attempts int
	var lastErr *string
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error, next_attempt_at FROM identity_deletions WHERE subject=$1`, subject).Scan(&attempts, &lastErr, &next); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastErr == nil || strings.Contains(*lastErr, subject) || !next.After(clock.Add(time.Minute)) {
		t.Errorf("failure bookkeeping: attempts=%d err=%v next=%v", attempts, lastErr, next)
	}
}

func kcAdminToken(base string) (string, error) {
	resp, err := http.PostForm(base+"/realms/returnit/protocol/openid-connect/token", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"returnit-worker"}, "client_secret": {"dev-worker-secret"}})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tk struct {
		Access string `json:"access_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tk)
	return tk.Access, err
}

func jwtSubject(t *testing.T, jwt string) string {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal("not a JWT")
	}
	b, err := decodeSegment(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Sub == "" {
		t.Fatalf("no sub in token: %v", err)
	}
	return c.Sub
}

func decodeSegment(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
