package worker

import (
	"testing"
	"time"

	"github.com/returnit/returnit/internal/queue"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newClockedHealth() (*Health, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	h := &Health{beats: map[string]time.Time{}, Now: c.now}
	h.started = c.t
	return h, c
}

func TestStaleJobsStartupGraceThenStale(t *testing.T) {
	h, c := newClockedHealth()

	c.t = c.t.Add(2 * time.Minute) // jobs have not run yet, but the worker only just started
	if got := h.staleJobs(); len(got) != 0 {
		t.Fatalf("startup grace period violated: %v", got)
	}
	c.t = c.t.Add(2 * time.Minute) // 4 minutes and still nothing: stuck
	got := h.staleJobs()
	if len(got) != 2 || got[0] != "job:"+queue.TypePlanReminders && got[1] != "job:"+queue.TypePlanReminders {
		t.Fatalf("both critical jobs should be stale: %v", got)
	}
}

func TestHeartbeatsKeepTheWorkerReady(t *testing.T) {
	h, c := newClockedHealth()
	c.t = c.t.Add(10 * time.Minute)
	h.Beat(queue.TypePlanReminders)
	got := h.staleJobs()
	if len(got) != 1 || got[0] != "job:"+queue.TypeDispatchReminders {
		t.Fatalf("only dispatch should be stale now: %v", got)
	}
	h.Beat(queue.TypeDispatchReminders)
	if got := h.staleJobs(); len(got) != 0 {
		t.Fatalf("both beating: %v", got)
	}
	c.t = c.t.Add(2 * time.Minute) // still within MaxJobAge (3m)
	if got := h.staleJobs(); len(got) != 0 {
		t.Fatalf("a job that ran 2m ago is healthy: %v", got)
	}
	c.t = c.t.Add(2 * time.Minute) // 4m since the last success
	if got := h.staleJobs(); len(got) != 2 {
		t.Fatalf("jobs silent for 4m are stale: %v", got)
	}
}

func TestNonCriticalJobsDoNotAffectReadiness(t *testing.T) {
	h, c := newClockedHealth()
	c.t = c.t.Add(time.Hour)
	// The hourly orphan sweep etc. are not liveness signals; only the per-minute jobs are.
	h.Beat(queue.TypeSweepOrphans)
	if got := h.staleJobs(); len(got) != 2 {
		t.Fatalf("sweep must not mask a stuck planner/dispatcher: %v", got)
	}
}
