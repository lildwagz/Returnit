package httpapi

import (
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/returnit/returnit/internal/logging"
)

const (
	ctxRequestID = "request_id"
	ctxLogger    = "logger"
)

// An inbound X-Request-ID (e.g. from a gateway) is trusted only if it is short and made of safe
// characters; anything else is replaced, so it can never inject content into logs or headers.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !validRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		c.Set(ctxRequestID, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// requestLogger writes exactly one structured line per request after it completes, and puts a
// request-scoped logger (carrying request_id) on the gin and standard contexts so lower layers
// log with the same ID.
//
// Deliberately NOT logged: query strings, raw paths (only the route pattern), headers, bodies,
// client IP, e-mail, filenames. The caller is identified by internal user UUID only.
func requestLogger(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		l := base.With("request_id", c.GetString(ctxRequestID))
		c.Set(ctxLogger, l)
		c.Request = c.Request.WithContext(logging.WithContext(c.Request.Context(), l))

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched" // never log arbitrary client-supplied paths
		}
		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"latency_ms", float64(time.Since(start).Microseconds()) / 1000,
			"resp_bytes", c.Writer.Size(),
		}
		if uid, ok := c.Get(ctxUserID); ok {
			attrs = append(attrs, "user_id", uid)
		}
		if last := c.Errors.Last(); last != nil {
			attrs = append(attrs, "err", last.Err.Error())
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		case strings.HasPrefix(route, "/health/"):
			level = slog.LevelDebug // probes would otherwise drown everything else
		}
		l.Log(c.Request.Context(), level, "request", attrs...)
	}
}

// recovery turns a panic into a logged error (with stack) and a clean 500 envelope. Stack traces
// go to the log only, never to the client.
func recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger(c).Error("panic recovered", "panic", recovered, "stack", string(debug.Stack()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorBody("internal", "internal error", c))
	})
}

// logger returns the request-scoped logger (falls back to the default one outside a request).
func logger(c *gin.Context) *slog.Logger {
	if l, ok := c.Get(ctxLogger); ok {
		if l, ok := l.(*slog.Logger); ok {
			return l
		}
	}
	return slog.Default()
}

// internalError responds 500 and records the real cause for the request log line. Clients only
// ever see the generic message; the underlying error is never serialised into the response.
func internalError(c *gin.Context, err error) {
	_ = c.Error(err)
	c.JSON(http.StatusInternalServerError, ErrorBody("internal", "internal error", c))
}
