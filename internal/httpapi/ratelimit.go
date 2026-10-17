package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/returnit/returnit/internal/ratelimit"
)

// tooManyRequests answers 429 with Retry-After and records which bucket tripped, so the request
// log line says why (the client only sees the generic message).
func tooManyRequests(c *gin.Context, bucket string, res ratelimit.Result) {
	secs := int(math.Ceil(res.RetryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	c.Header("X-RateLimit-Limit", strconv.Itoa(res.Limit))
	c.Header("X-RateLimit-Remaining", "0")
	_ = c.Error(fmt.Errorf("rate limited: %s", bucket))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, ErrorBody("rate_limited", "too many requests, retry later", c))
}

// allow consumes one slot. A limiter error fails open: the request is allowed and the error is
// recorded on the request line. (Production uses ratelimit.Fallback, which never errors.)
func allow(c *gin.Context, l ratelimit.Limiter, key string, limit int, window time.Duration) (ratelimit.Result, bool) {
	res, err := l.Allow(c.Request.Context(), key, limit, window)
	if err != nil {
		_ = c.Error(fmt.Errorf("rate limiter error (failing open): %w", err))
		return ratelimit.Result{Allowed: true, Limit: limit, Remaining: limit}, true
	}
	return res, res.Allowed
}

// userLimit limits the authenticated caller on a named bucket. It must run after authRequired.
// With headers=true the remaining budget is reported on every response.
func userLimit(l ratelimit.Limiter, bucket string, limit int, window time.Duration, headers bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, ok := allow(c, l, "u:"+bucket+":"+userID(c).String(), limit, window)
		if !ok {
			tooManyRequests(c, bucket, res)
			return
		}
		if headers {
			c.Header("X-RateLimit-Limit", strconv.Itoa(res.Limit))
			c.Header("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		}
		c.Next()
	}
}

// authFailureLimit returns a function the auth middleware calls whenever a request fails
// authentication. It charges the client IP's budget and reports whether the IP is now over it.
// Authenticated traffic is never charged to the IP, because many mobile users share one address
// behind carrier NAT. The IP is hashed before it reaches Redis and is never logged.
func authFailureLimit(l ratelimit.Limiter, limit int) func(c *gin.Context) (ratelimit.Result, bool) {
	return func(c *gin.Context) (ratelimit.Result, bool) {
		sum := sha256.Sum256([]byte(c.ClientIP()))
		return allow(c, l, "ip-unauth:"+hex.EncodeToString(sum[:8]), limit, time.Minute)
	}
}
