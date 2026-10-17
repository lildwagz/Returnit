package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/queue"
)

// criticalJobs must keep succeeding for the worker to count as healthy. They run every minute.
var criticalJobs = []string{queue.TypePlanReminders, queue.TypeDispatchReminders}

// Health answers liveness/readiness for orchestrators and exposes operational metrics. It listens
// on its own port (WORKER_HEALTH_ADDR): never publish that port to the internet. Nothing it
// reports contains personal data.
type Health struct {
	Pool  *pgxpool.Pool
	Redis queue.Config
	// MaxJobAge is how stale a critical job's last success may be before the worker is
	// reported not-ready. Defaults to 3 minutes (the jobs run every minute).
	MaxJobAge time.Duration
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time

	started time.Time
	mu      sync.Mutex
	beats   map[string]time.Time
}

func NewHealth(pool *pgxpool.Pool, redis queue.Config) *Health {
	h := &Health{Pool: pool, Redis: redis, beats: map[string]time.Time{}}
	h.started = h.now()
	return h
}

func (h *Health) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Health) maxAge() time.Duration {
	if h.MaxJobAge > 0 {
		return h.MaxJobAge
	}
	return 3 * time.Minute
}

// Beat records that a job of this type just finished successfully.
func (h *Health) Beat(jobType string) {
	h.mu.Lock()
	h.beats[jobType] = h.now()
	h.mu.Unlock()
}

// Failures returns the names of failing checks; empty means ready.
func (h *Health) Failures(ctx context.Context) []string {
	var failed []string
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := h.Pool.Ping(pctx); err != nil {
		failed = append(failed, "database")
	}
	if err := queue.Ping(h.Redis); err != nil {
		failed = append(failed, "redis")
	}
	failed = append(failed, h.staleJobs()...)
	sort.Strings(failed)
	return failed
}

// staleJobs lists critical jobs whose last success is older than MaxJobAge. A job that has not
// run yet is given MaxJobAge from startup before it counts as stale.
func (h *Health) staleJobs() []string {
	var stale []string
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, job := range criticalJobs {
		last, ok := h.beats[job]
		if !ok {
			last = h.started
		}
		if now.Sub(last) > h.maxAge() {
			stale = append(stale, "job:"+job)
		}
	}
	return stale
}

// Handler serves /health/live, /health/ready and /metrics.
func (h *Health) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if failed := h.Failures(r.Context()); len(failed) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "failing": failed})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", h.metrics)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// metrics writes Prometheus text exposition format (no client library needed for plain gauges).
func (h *Health) metrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var b strings.Builder
	gauge := func(name, help string, value any, labels ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s", name, help, name, name)
		if len(labels) > 0 {
			fmt.Fprintf(&b, "{%s}", strings.Join(labels, ","))
		}
		fmt.Fprintf(&b, " %v\n", value)
	}
	series := func(name string, value any, labels ...string) {
		fmt.Fprintf(&b, "%s{%s} %v\n", name, strings.Join(labels, ","), value)
	}

	var pending, failing, overdueNotif, overdueAccounts, emailsPending, emailsFailing, identitiesPending, identitiesFailing int
	var oldestAge, emailOldest float64
	err := h.Pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM object_deletions),
  COALESCE((SELECT extract(epoch FROM now() - min(created_at)) FROM object_deletions), 0),
  (SELECT count(*) FROM object_deletions WHERE attempts >= 10),
  (SELECT count(*) FROM notifications WHERE status = 'scheduled' AND scheduled_at < now() - interval '5 minutes'),
  (SELECT count(*) FROM users WHERE deletion_scheduled_at < now() - interval '1 hour'),
  (SELECT count(*) FROM account_emails),
  COALESCE((SELECT extract(epoch FROM now() - min(next_attempt_at)) FROM account_emails WHERE next_attempt_at < now()), 0),
  (SELECT count(*) FROM account_emails WHERE attempts >= 5),
  (SELECT count(*) FROM identity_deletions),
  (SELECT count(*) FROM identity_deletions WHERE attempts >= 5)`).
		Scan(&pending, &oldestAge, &failing, &overdueNotif, &overdueAccounts, &emailsPending, &emailOldest, &emailsFailing, &identitiesPending, &identitiesFailing)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	gauge("returnit_object_deletions_pending", "Receipt files queued for deletion.", pending)
	gauge("returnit_object_deletions_oldest_age_seconds", "Age of the oldest queued file deletion (alert if > 3600).", fmt.Sprintf("%.0f", oldestAge))
	gauge("returnit_object_deletions_failing", "Queued deletions that failed 10 or more times (alert if > 0).", failing)
	gauge("returnit_notifications_overdue", "Reminders scheduled more than 5 minutes ago and still unsent.", overdueNotif)
	gauge("returnit_accounts_overdue_erasure", "Accounts past their erasure date by more than an hour (alert if > 0).", overdueAccounts)

	gauge("returnit_account_emails_pending", "Account-deletion e-mails waiting to be sent.", emailsPending)
	gauge("returnit_account_emails_oldest_due_seconds", "How long the longest-waiting due e-mail has been waiting (alert if > 900).", fmt.Sprintf("%.0f", emailOldest))
	gauge("returnit_account_emails_failing", "E-mails that failed 5 or more times (alert if > 0).", emailsFailing)

	gauge("returnit_identity_deletions_pending", "Erased users still to be removed at the identity provider.", identitiesPending)
	gauge("returnit_identity_deletions_failing", "Identity deletions that failed 5 or more times (alert if > 0).", identitiesFailing)

	// Per-queue gauges; Redis trouble degrades to a comment, not a failed scrape.
	var stats []queue.QueueStat
	qerr := queue.Ping(h.Redis) // fast failure first: the statistics call is slower to give up
	if qerr == nil {
		stats, qerr = queue.Stats(h.Redis)
	}
	if qerr != nil {
		b.WriteString("# queue statistics unavailable\n")
	} else {
		b.WriteString("# HELP returnit_queue_tasks Tasks per queue and state (archived = gave up after all retries; alert if > 0).\n# TYPE returnit_queue_tasks gauge\n")
		for _, s := range stats {
			q := fmt.Sprintf(`queue=%q`, s.Queue)
			series("returnit_queue_tasks", s.Pending, q, `state="pending"`)
			series("returnit_queue_tasks", s.Active, q, `state="active"`)
			series("returnit_queue_tasks", s.Retry, q, `state="retry"`)
			series("returnit_queue_tasks", s.Archived, q, `state="archived"`)
		}
		b.WriteString("# HELP returnit_queue_latency_seconds How long the oldest pending task has waited.\n# TYPE returnit_queue_latency_seconds gauge\n")
		for _, s := range stats {
			series("returnit_queue_latency_seconds", fmt.Sprintf("%.0f", s.Latency.Seconds()), fmt.Sprintf(`queue=%q`, s.Queue))
		}
	}

	h.mu.Lock()
	jobs := make([]string, 0, len(h.beats))
	for j := range h.beats {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	b.WriteString("# HELP returnit_job_last_success_timestamp_seconds Unix time of each periodic job's last success.\n# TYPE returnit_job_last_success_timestamp_seconds gauge\n")
	for _, j := range jobs {
		series("returnit_job_last_success_timestamp_seconds", h.beats[j].Unix(), fmt.Sprintf(`job=%q`, j))
	}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// Serve runs the health server until ctx is cancelled.
func (h *Health) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: h.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
