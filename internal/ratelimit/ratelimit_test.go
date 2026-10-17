package ratelimit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newMem() (*Memory, *clock) {
	c := &clock{t: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	m := NewMemory()
	m.Now = c.now
	return m, c
}

func allow(t *testing.T, l Limiter, key string, limit int, w time.Duration) Result {
	t.Helper()
	r, err := l.Allow(context.Background(), key, limit, w)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMemoryLimitsAndSlidingWindow(t *testing.T) {
	m, c := newMem()
	for i := 1; i <= 3; i++ {
		r := allow(t, m, "k", 3, time.Minute)
		if !r.Allowed || r.Remaining != 3-i || r.Limit != 3 {
			t.Fatalf("call %d: %+v", i, r)
		}
		c.t = c.t.Add(10 * time.Second) // hits at 0s, 10s, 20s
	}
	// 30s in: the window still holds all three; the oldest (0s) frees up in 30s.
	r := allow(t, m, "k", 3, time.Minute)
	if r.Allowed || r.RetryAfter != 30*time.Second {
		t.Fatalf("4th call should be denied with RetryAfter=30s: %+v", r)
	}
	// A denied call must not consume budget or extend the window.
	c.t = c.t.Add(30 * time.Second) // 60s: the 0s hit has expired
	if r := allow(t, m, "k", 3, time.Minute); !r.Allowed || r.Remaining != 0 {
		t.Fatalf("after the oldest hit expires one slot opens: %+v", r)
	}
	if r := allow(t, m, "k", 3, time.Minute); r.Allowed {
		t.Fatalf("budget is exhausted again: %+v", r)
	}
	// Smooth sliding (no fixed-window boundary burst): slots free one at a time.
	c.t = c.t.Add(10 * time.Second) // the 10s hit expires
	if r := allow(t, m, "k", 3, time.Minute); !r.Allowed {
		t.Fatalf("expected exactly one slot: %+v", r)
	}
	if r := allow(t, m, "k", 3, time.Minute); r.Allowed {
		t.Fatalf("only one slot should have freed: %+v", r)
	}
}

func TestMemoryKeysAreIndependent(t *testing.T) {
	m, _ := newMem()
	allow(t, m, "alice", 1, time.Minute)
	if r := allow(t, m, "alice", 1, time.Minute); r.Allowed {
		t.Fatal("alice should be limited")
	}
	if r := allow(t, m, "bob", 1, time.Minute); !r.Allowed {
		t.Fatal("bob must have his own budget")
	}
}

func TestMemoryGarbageCollectsIdleKeys(t *testing.T) {
	m, c := newMem()
	for i := 0; i < 100; i++ {
		allow(t, m, "k"+string(rune('a'+i%26))+string(rune('a'+i/26)), 5, time.Minute)
	}
	c.t = c.t.Add(3 * time.Minute)
	allow(t, m, "fresh", 5, time.Minute) // triggers the sweep
	m.mu.Lock()
	n := len(m.keys)
	m.mu.Unlock()
	if n != 1 {
		t.Fatalf("idle keys not collected: %d left", n)
	}
}

func TestMemoryIsExactUnderConcurrency(t *testing.T) {
	m := NewMemory()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, _ := m.Allow(context.Background(), "hot", 25, time.Minute); r.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 25 {
		t.Fatalf("allowed %d requests, want exactly 25", allowed.Load())
	}
}

type brokenLimiter struct{ calls int }

func (b *brokenLimiter) Allow(context.Context, string, int, time.Duration) (Result, error) {
	b.calls++
	return Result{}, errors.New("redis down")
}

func TestFallbackUsesMemoryWhenPrimaryFails(t *testing.T) {
	primary := &brokenLimiter{}
	f := &Fallback{Primary: primary, Secondary: NewMemory()}
	for i := 0; i < 2; i++ {
		if r, err := f.Allow(context.Background(), "k", 2, time.Minute); err != nil || !r.Allowed {
			t.Fatalf("call %d: %+v %v", i, r, err)
		}
	}
	if r, err := f.Allow(context.Background(), "k", 2, time.Minute); err != nil || r.Allowed {
		t.Fatalf("limits must still apply during an outage: %+v %v", r, err)
	}
	if primary.calls != 1 {
		t.Fatalf("after one failure the primary must be skipped during the cooldown, calls=%d", primary.calls)
	}
}

func TestFallbackRetriesPrimaryAfterCooldownAndRecovers(t *testing.T) {
	primary := &flakyLimiter{inner: NewMemory(), failing: true}
	f := &Fallback{Primary: primary, Secondary: NewMemory(), Cooldown: 40 * time.Millisecond}
	ctx := context.Background()

	f.Allow(ctx, "k", 100, time.Minute) // fails, opens the breaker
	f.Allow(ctx, "k", 100, time.Minute) // skipped
	if primary.calls != 1 {
		t.Fatalf("breaker did not open: %d primary calls", primary.calls)
	}
	primary.failing = false
	time.Sleep(60 * time.Millisecond)
	f.Allow(ctx, "k", 100, time.Minute) // cooldown over: tries the primary again
	if primary.calls != 2 || primary.served != 1 {
		t.Fatalf("primary not retried after cooldown: calls=%d served=%d", primary.calls, primary.served)
	}
	f.Allow(ctx, "k", 100, time.Minute)
	if primary.served != 2 {
		t.Fatalf("traffic should return to the primary once it recovers: served=%d", primary.served)
	}
}

type flakyLimiter struct {
	inner         Limiter
	failing       bool
	calls, served int
}

func (l *flakyLimiter) Allow(ctx context.Context, key string, limit int, w time.Duration) (Result, error) {
	l.calls++
	if l.failing {
		return Result{}, errors.New("down")
	}
	l.served++
	return l.inner.Allow(ctx, key, limit, w)
}

func TestFallbackPrefersPrimary(t *testing.T) {
	primary, secondary := NewMemory(), NewMemory()
	f := &Fallback{Primary: primary, Secondary: secondary}
	f.Allow(context.Background(), "k", 1, time.Minute)
	if len(secondary.keys) != 0 {
		t.Fatal("secondary used although the primary works")
	}
}

func TestDefaultLimits(t *testing.T) {
	l := DefaultLimits()
	if l.UserPerMinute != 60 || l.UploadPerMinute != 10 || l.AccountDeletionPerHour != 3 || l.UnauthPerMinutePerIP != 30 || l.ExportPerHour != 3 {
		t.Fatalf("defaults drifted from the agreed limits: %+v", l)
	}
}

func TestWithDefaultsFillsOnlyUnsetFields(t *testing.T) {
	got := Limits{UserPerMinute: 5}.WithDefaults()
	want := DefaultLimits()
	want.UserPerMinute = 5
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if (Limits{}).WithDefaults() != DefaultLimits() {
		t.Fatal("the zero value must become the defaults")
	}
}
