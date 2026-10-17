//go:build integration

package notifications_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/returnit/returnit/internal/devices"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/push"
	"github.com/returnit/returnit/internal/users"
)

// A reminder in the database becomes a push on the phones registered in the database, through the
// real FCM client (pointed at a local fake of Google's server), and dead tokens are cleaned up.
func TestPushReachesRegisteredPhones(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration env not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	good, dead := "good-token-"+sfx+"-aaaaaaaaaaaa", "dead-token-"+sfx+"-aaaaaaaaaaaa"
	var mu sync.Mutex
	var seen []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]map[string]any
		json.Unmarshal(b, &body)
		mu.Lock()
		seen = append(seen, body["message"])
		mu.Unlock()
		if body["message"]["token"] == dead {
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`)
			return
		}
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(fake.Close)

	us, devs := users.NewStore(pool), devices.NewService(pool)
	uid, _ := us.EnsureBySubject(ctx, "pu-"+sfx, "pu-"+sfx+"@example.com", "P")
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid) })
	devs.Register(ctx, uid, devices.Input{Platform: "android", PushToken: good})
	devs.Register(ctx, uid, devices.Input{Platform: "ios", PushToken: dead})

	var nid uuid.UUID
	err = pool.QueryRow(ctx, `
INSERT INTO notifications (user_id, title, body, scheduled_at, sent_at, status, idempotency_key, channel, offset_days, warranty_end_date)
VALUES ($1,'Warranty ends in 7 days','Laptop', now(), now(), 'sent', 'pu-'||$2, 'push', 7, '2030-01-01') RETURNING id`, uid, sfx).Scan(&nid)
	if err != nil {
		t.Fatal(err)
	}

	sender := &notifications.PushSender{Pool: pool, Pusher: &notifications.Delivery{
		Pool: pool, Devices: devs, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Phones: &push.FCM{ProjectID: "proj", BaseURL: fake.URL, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "a"})},
	}}
	if err := sender.Send(ctx, nid); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 {
		t.Fatalf("FCM should be called once per phone, got %d", len(seen))
	}
	for _, m := range seen {
		if m["data"].(map[string]any)["notificationId"] != nid.String() || m["notification"].(map[string]any)["title"] != "Warranty ends in 7 days" {
			t.Errorf("payload: %v", m)
		}
	}
	toks, _ := devs.TokensFor(ctx, uid)
	if len(toks) != 1 || toks[0] != good {
		t.Errorf("the dead token must be removed and the good one kept: %v", toks)
	}

	// A second delivery reaches only the remaining phone.
	seen = nil
	if err := sender.Send(ctx, nid); err != nil || len(seen) != 1 {
		t.Fatalf("second send: %v, calls=%d", err, len(seen))
	}
}
