// Package logging builds the structured (JSON) logger shared by the web and worker processes.
//
// Policy (see Docs/docs/technical-design-notes.md §8): never log tokens, secrets, signed URLs,
// receipt contents, filenames, product names or e-mail addresses. Identify people only by their
// internal user UUID. ReplaceAttr below is a safety net against accidental leaks by attribute
// name; it does not replace choosing log fields carefully.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// redactedKeys are attribute names whose values are always replaced (compared case-insensitively).
var redactedKeys = map[string]bool{
	"authorization": true, "cookie": true, "set-cookie": true,
	"token": true, "access_token": true, "refresh_token": true, "id_token": true,
	"password": true, "secret": true, "secret_key": true, "access_key": true, "api_key": true,
	"signed_url": true, "url": true, "email": true, "file_name": true, "filename": true,
}

const redacted = "[REDACTED]"

// New returns a JSON logger writing to w. Every record carries the service name.
func New(w io.Writer, service string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if redactedKeys[strings.ToLower(a.Key)] {
				return slog.String(a.Key, redacted)
			}
			return a
		},
	})
	return slog.New(h).With("service", service)
}

// NewFromEnv builds the process logger on stdout. LOG_LEVEL is debug, info (default), warn or error.
func NewFromEnv(service string) *slog.Logger {
	return New(os.Stdout, service, ParseLevel(os.Getenv("LOG_LEVEL")))
}

// ParseLevel maps a LOG_LEVEL string to a slog level; unknown or empty values mean info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type ctxKey struct{}

// WithContext stores a (request-scoped) logger in ctx.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger, or the default logger if there is none. Lower
// layers (services, repositories) use it so their logs carry the request ID automatically.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
