//go:build integration

package ratelimit_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/ratelimit"
)

func redisAddr(t *testing.T) string {
	t.Helper()
	a := os.Getenv("TEST_REDIS_ADDR")
	if a == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	return a
}

func TestRedisLimiterSharedAcrossInstances(t *testing.T) {
	addr := redisAddr(t)
	// Two limiters stand in for two web instances behind a load balancer.
	a, b := ratelimit.NewRedis(addr, "", 0), ratelimit.NewRedis(addr, "", 0)
	t.Cleanup(func() { a.Close(); b.Close() })
	ctx := context.Background()
	key := "test:" + uuid.NewString()

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(l ratelimit.Limiter) {
			defer wg.Done()
			r, err := l.Allow(ctx, key, 10, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Allowed {
				allowed.Add(1)
			}
		}([]ratelimit.Limiter{a, b}[i%2])
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("two instances allowed %d requests together, want exactly 10 (shared budget)", allowed.Load())
	}

	r, err := a.Allow(ctx, key, 10, time.Minute)
	if err != nil || r.Allowed || r.Remaining != 0 || r.RetryAfter <= 0 || r.RetryAfter > time.Minute {
		t.Fatalf("over-limit result: %+v %v", r, err)
	}
	// Other keys are independent.
	if r, _ := b.Allow(ctx, "test:"+uuid.NewString(), 10, time.Minute); !r.Allowed || r.Remaining != 9 {
		t.Fatalf("fresh key: %+v", r)
	}
}

func TestRedisLimiterWindowSlides(t *testing.T) {
	l := ratelimit.NewRedis(redisAddr(t), "", 0)
	t.Cleanup(func() { l.Close() })
	ctx := context.Background()
	key := "test:" + uuid.NewString()

	for i := 0; i < 2; i++ {
		if r, _ := l.Allow(ctx, key, 2, 1500*time.Millisecond); !r.Allowed {
			t.Fatalf("call %d should pass", i+1)
		}
	}
	r, _ := l.Allow(ctx, key, 2, 1500*time.Millisecond)
	if r.Allowed || r.RetryAfter <= 0 || r.RetryAfter > 1500*time.Millisecond {
		t.Fatalf("expected denial with a short RetryAfter: %+v", r)
	}
	time.Sleep(r.RetryAfter + 100*time.Millisecond)
	if r, _ := l.Allow(ctx, key, 2, 1500*time.Millisecond); !r.Allowed {
		t.Fatalf("budget should be restored once the window has passed: %+v", r)
	}
}

func TestRedisLimiterKeysExpire(t *testing.T) {
	addr := redisAddr(t)
	l := ratelimit.NewRedis(addr, "", 0)
	t.Cleanup(func() { l.Close() })
	ctx := context.Background()
	key := "test:" + uuid.NewString()
	l.Allow(ctx, key, 5, 800*time.Millisecond)
	time.Sleep(1200 * time.Millisecond)
	// An expired key leaves nothing behind: the next call starts from a full budget.
	if r, _ := l.Allow(ctx, key, 5, 800*time.Millisecond); r.Remaining != 4 {
		t.Fatalf("stale state survived the window: %+v", r)
	}
}

func TestFallbackKeepsLimitingWhenRedisIsDown(t *testing.T) {
	down := ratelimit.NewRedis("127.0.0.1:1", "", 0) // nothing listens here
	t.Cleanup(func() { down.Close() })
	f := &ratelimit.Fallback{Primary: down, Secondary: ratelimit.NewMemory()}
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 3; i++ {
		r, err := f.Allow(ctx, "k", 3, time.Minute)
		if err != nil || !r.Allowed {
			t.Fatalf("call %d during outage: %+v %v", i+1, r, err)
		}
	}
	if r, err := f.Allow(ctx, "k", 3, time.Minute); err != nil || r.Allowed {
		t.Fatalf("limit must still hold during an outage: %+v %v", r, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("outage handling is too slow: %v", time.Since(start))
	}
}
