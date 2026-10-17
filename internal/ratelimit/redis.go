package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Sliding-window log in a sorted set, evaluated atomically in Redis. Time comes from the Redis
// server (TIME), so web instances with skewed clocks still share one consistent window.
// Returns {allowed, remaining, retry_after_ms}.
var slidingWindow = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local window = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
local count = redis.call('ZCARD', KEYS[1])
if count < limit then
  redis.call('ZADD', KEYS[1], now, ARGV[3])
  redis.call('PEXPIRE', KEYS[1], window)
  return {1, limit - count - 1, 0}
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local retry = tonumber(oldest[2]) + window - now
if retry < 1 then retry = 1 end
return {0, 0, retry}
`)

type Redis struct {
	client *redis.Client
	prefix string
}

// NewRedis builds a limiter on its own client with short timeouts, so a Redis outage costs a
// request milliseconds (then Fallback takes over) instead of stalling it.
func NewRedis(addr, password string, db int) *Redis {
	return &Redis{
		prefix: "rl:",
		client: redis.NewClient(&redis.Options{
			Addr: addr, Password: password, DB: db,
			DialTimeout: time.Second, ReadTimeout: 300 * time.Millisecond, WriteTimeout: 300 * time.Millisecond,
			MaxRetries: 0,
		}),
	}
}

func (r *Redis) Close() error { return r.client.Close() }

func (r *Redis) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	raw, err := slidingWindow.Run(ctx, r.client, []string{r.prefix + key},
		window.Milliseconds(), limit, uuid.NewString()).Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: redis: %w", err)
	}
	if len(raw) != 3 {
		return Result{}, fmt.Errorf("ratelimit: unexpected script reply %v", raw)
	}
	allowed, _ := raw[0].(int64)
	remaining, _ := raw[1].(int64)
	retryMs, _ := raw[2].(int64)
	return Result{
		Allowed: allowed == 1, Limit: limit, Remaining: int(remaining),
		RetryAfter: time.Duration(retryMs) * time.Millisecond,
	}, nil
}
