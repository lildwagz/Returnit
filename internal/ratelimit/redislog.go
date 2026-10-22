package ratelimit

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/returnit/returnit/internal/logging"
)

// EnableLog logs every Redis command the limiter sends (LOG_REDIS=on): the command name, how long it took
// and whether it failed. Keys and arguments are NOT logged: they contain client IP addresses and user ids.
func (r *Redis) EnableLog(l *slog.Logger) { r.client.AddHook(cmdLog{log: l}) }

type cmdLog struct{ log *slog.Logger }

func (cmdLog) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (h cmdLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.line(ctx, cmd.Name(), 1, time.Since(start), err)
		return err
	}
}

func (h cmdLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		h.line(ctx, "pipeline", len(cmds), time.Since(start), err)
		return err
	}
}

// handshake commands are sent by the client library every time it opens a connection; they say nothing
// about what the application stored, and some servers answer them with an error that is perfectly normal.
var handshake = map[string]bool{"hello": true, "client": true, "auth": true, "select": true}

func (h cmdLog) line(ctx context.Context, name string, n int, took time.Duration, err error) {
	if handshake[name] {
		return
	}
	l := logging.FromContext(ctx)
	if l == slog.Default() {
		l = h.log
	}
	attrs := []any{"command", name, "commands", n, "duration_ms", float64(took.Microseconds()) / 1000}
	switch {
	case err == nil && name == "pipeline":
		l.Debug("redis", attrs...) // connection setup uses a pipeline; real work does not
	case err == nil:
		l.Info("redis", attrs...)
	case strings.HasPrefix(err.Error(), "NOSCRIPT"):
		// first use of the Lua script on this server: the library loads it and retries, not a failure
		l.Info("redis", append(attrs, "note", "script not cached yet, loading it")...)
	default:
		l.Warn("redis", append(attrs, "err", err.Error())...)
	}
}
