//go:build integration

package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/queue"
	"github.com/returnit/returnit/internal/users"
	"github.com/returnit/returnit/internal/worker"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func metric(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` (\S+)$`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("metric %s missing from:\n%s", name, body)
	}
	return m[1]
}

func TestHealthAndMetrics(t *testing.T) {
	dbURL, redisAddr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_ADDR")
	if dbURL == "" || redisAddr == "" {
		t.Skip("integration env not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	now := time.Now()
	clock := now
	h := worker.NewHealth(pool, queue.Config{Addr: redisAddr})
	h.Now = func() time.Time { return clock }
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	// Liveness is unconditional.
	if code, _ := get(t, srv.URL+"/health/live"); code != 200 {
		t.Fatalf("live: %d", code)
	}
	// Right after start, no job has run yet but the startup grace period applies.
	if code, body := get(t, srv.URL+"/health/ready"); code != 200 {
		t.Fatalf("ready right after start: %d %s", code, body)
	}
	// Minutes later with no successful job: not ready, and it says which jobs are stuck.
	clock = now.Add(5 * time.Minute)
	code, body := get(t, srv.URL+"/health/ready")
	var r struct {
		Status  string   `json:"status"`
		Failing []string `json:"failing"`
	}
	json.Unmarshal([]byte(body), &r)
	if code != 503 || r.Status != "not_ready" || len(r.Failing) != 2 || !strings.HasPrefix(r.Failing[0], "job:") {
		t.Fatalf("stuck worker: %d %s", code, body)
	}
	// Heartbeats restore readiness.
	h.Beat(queue.TypePlanReminders)
	h.Beat(queue.TypeDispatchReminders)
	if code, body := get(t, srv.URL+"/health/ready"); code != 200 {
		t.Fatalf("after heartbeats: %d %s", code, body)
	}

	// An unreachable Redis makes the worker not ready, and the response names the dependency.
	broken := worker.NewHealth(pool, queue.Config{Addr: "127.0.0.1:1"})
	bsrv := httptest.NewServer(broken.Handler())
	t.Cleanup(bsrv.Close)
	code, body = get(t, bsrv.URL+"/health/ready")
	if code != 503 || !strings.Contains(body, `"redis"`) {
		t.Fatalf("redis down: %d %s", code, body)
	}

	// --- Metrics reflect real backlog ---
	us := users.NewStore(pool)
	sfx := uuid.NewString()
	uid, _ := us.EnsureBySubject(ctx, "hm-"+sfx, "private-person-"+sfx+"@example.com", "Private Person")
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid)
		pool.Exec(context.Background(), `DELETE FROM object_deletions WHERE storage_key LIKE $1`, "%"+sfx+"%")
	})
	pool.Exec(ctx, `INSERT INTO object_deletions (storage_key, created_at, attempts) VALUES ($1, now() - interval '2 hours', 0), ($2, now(), 12)`,
		"receipts/a/"+sfx+".pdf", "receipts/b/"+sfx+".pdf")
	pool.Exec(ctx, `INSERT INTO notifications (user_id, title, body, scheduled_at, status, idempotency_key, channel, offset_days, warranty_end_date)
		VALUES ($1,'t','b', now() - interval '1 hour','scheduled','hm-'||$2,'push',1,'2030-01-01')`, uid, sfx)
	pool.Exec(ctx, `UPDATE users SET deletion_requested_at = now() - interval '8 days', deletion_scheduled_at = now() - interval '3 hours' WHERE id = $1`, uid)

	code, metrics := get(t, srv.URL+"/metrics")
	if code != 200 {
		t.Fatalf("metrics: %d", code)
	}
	if metric(t, metrics, "returnit_object_deletions_pending") != "2" {
		t.Errorf("pending deletions:\n%s", metrics)
	}
	if age, err := strconv.ParseFloat(metric(t, metrics, "returnit_object_deletions_oldest_age_seconds"), 64); err != nil || age < 7100 || age > 7400 {
		t.Errorf("oldest age should be ~7200s, got %v (%v)", age, err)
	}
	if metric(t, metrics, "returnit_object_deletions_failing") != "1" {
		t.Error("exactly one deletion has failed 10+ times")
	}
	if metric(t, metrics, "returnit_notifications_overdue") != "1" {
		t.Error("one reminder is overdue")
	}
	if metric(t, metrics, "returnit_accounts_overdue_erasure") != "1" {
		t.Error("one account is overdue for erasure")
	}
	for _, want := range []string{
		`returnit_queue_tasks{queue="default",state="pending"}`,
		`returnit_queue_tasks{queue="push",state="archived"}`,
		`returnit_queue_latency_seconds{queue="default"}`,
		`returnit_job_last_success_timestamp_seconds{job="reminders:plan"}`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("missing series %s", want)
		}
	}
	// Metrics are operational numbers only: no personal data, no storage keys.
	for _, leak := range []string{"private-person", "Private Person", uid.String(), sfx, "receipts/"} {
		if strings.Contains(metrics, leak) {
			t.Errorf("metrics leak %q", leak)
		}
	}
}
