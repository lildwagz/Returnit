// Package ratelimit implements sliding-window rate limiting.
//
// Redis is the shared store, so limits hold across several web instances. If Redis is
// unreachable, Fallback degrades to a per-instance in-memory limiter instead of switching
// protection off (or rejecting everyone).
package ratelimit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Result is the outcome of one Allow call.
type Result struct {
	Allowed   bool
	Limit     int
	Remaining int
	// RetryAfter is how long until a request would be allowed; zero when Allowed.
	RetryAfter time.Duration
}

// Limiter decides whether the caller identified by key may proceed. Each allowed call consumes
// one slot of the window.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error)
}

// Limits are the per-bucket budgets enforced by the web process.
type Limits struct {
	UserPerMinute          int // every authenticated API call, per user
	UploadPerMinute        int // receipt uploads (they cost a malware scan and storage), per user
	AccountDeletionPerHour int // DELETE /account, per user
	UnauthPerMinutePerIP   int // requests that fail authentication, per client IP
	ExportPerHour          int // GET /account/export (reads all of a user's data), per user
}

func DefaultLimits() Limits {
	return Limits{UserPerMinute: 60, UploadPerMinute: 10, AccountDeletionPerHour: 3, UnauthPerMinutePerIP: 30, ExportPerHour: 3}
}

// WithDefaults replaces every unset (zero) field with its default, so a partly filled Limits is
// safe: a forgotten field means "default", never "block everything".
func (l Limits) WithDefaults() Limits {
	d := DefaultLimits()
	if l.UserPerMinute == 0 {
		l.UserPerMinute = d.UserPerMinute
	}
	if l.UploadPerMinute == 0 {
		l.UploadPerMinute = d.UploadPerMinute
	}
	if l.AccountDeletionPerHour == 0 {
		l.AccountDeletionPerHour = d.AccountDeletionPerHour
	}
	if l.UnauthPerMinutePerIP == 0 {
		l.UnauthPerMinutePerIP = d.UnauthPerMinutePerIP
	}
	if l.ExportPerHour == 0 {
		l.ExportPerHour = d.ExportPerHour
	}
	return l
}

// Memory is a per-process sliding-window limiter. It is the fallback when Redis is down and the
// default in tests.
type Memory struct {
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex
	keys   map[string]*entry
	lastGC time.Time
}

type entry struct {
	hits    []time.Time
	expires time.Time
}

func NewMemory() *Memory { return &Memory{keys: map[string]*entry{}} }

func (m *Memory) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Memory) Allow(_ context.Context, key string, limit int, window time.Duration) (Result, error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys == nil {
		m.keys = map[string]*entry{}
	}
	m.gc(now)

	e := m.keys[key]
	if e == nil {
		e = &entry{}
		m.keys[key] = e
	}
	cutoff := now.Add(-window)
	i := 0
	for i < len(e.hits) && !e.hits[i].After(cutoff) {
		i++
	}
	e.hits = e.hits[i:]

	if len(e.hits) >= limit {
		return Result{Limit: limit, RetryAfter: e.hits[0].Add(window).Sub(now)}, nil
	}
	e.hits = append(e.hits, now)
	e.expires = now.Add(window)
	return Result{Allowed: true, Limit: limit, Remaining: limit - len(e.hits)}, nil
}

// gc drops idle keys at most once a minute so the map cannot grow without bound.
func (m *Memory) gc(now time.Time) {
	if now.Sub(m.lastGC) < time.Minute {
		return
	}
	m.lastGC = now
	for k, e := range m.keys {
		if now.After(e.expires) {
			delete(m.keys, k)
		}
	}
}

// Fallback uses Primary (Redis) and, when it errors, Secondary (memory).
//
// After a failure it also stops calling Primary for Cooldown (default 5s). Without that, every
// request during an outage would first wait out the connection timeout; with it, only one
// request per cooldown pays that price and the rest go straight to the in-memory limiter.
type Fallback struct {
	Primary, Secondary Limiter
	Log                *slog.Logger
	Cooldown           time.Duration // 0 means 5s

	lastWarn  atomic.Int64 // unix seconds; throttles the warning
	retryAt   atomic.Int64 // unix nanos; Primary is skipped until then
	primaryOK atomic.Bool  // set once Primary has succeeded after an outage (for the recovery log)
}

func (f *Fallback) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	if time.Now().UnixNano() < f.retryAt.Load() {
		return f.Secondary.Allow(ctx, key, limit, window)
	}
	res, err := f.Primary.Allow(ctx, key, limit, window)
	if err == nil {
		if f.primaryOK.CompareAndSwap(false, true) && f.lastWarn.Load() != 0 && f.Log != nil {
			f.Log.Info("rate limiter store recovered; using shared limits again")
		}
		return res, nil
	}
	f.primaryOK.Store(false)
	cooldown := f.Cooldown
	if cooldown == 0 {
		cooldown = 5 * time.Second
	}
	f.retryAt.Store(time.Now().Add(cooldown).UnixNano())
	if f.Log != nil {
		now := time.Now().Unix()
		if last := f.lastWarn.Load(); now-last >= 30 && f.lastWarn.CompareAndSwap(last, now) {
			f.Log.Warn("rate limiter store unavailable; using per-instance in-memory limits", "err", err)
		}
	}
	return f.Secondary.Allow(ctx, key, limit, window)
}
