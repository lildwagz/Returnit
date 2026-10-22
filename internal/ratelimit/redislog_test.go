package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/returnit/returnit/internal/logging"
)

func TestRedisCommandLogHasNameAndTimeButNoKeys(t *testing.T) {
	var buf bytes.Buffer
	h := cmdLog{log: logging.New(&buf, "web", slog.LevelDebug)}
	cmd := redis.NewCmd(context.Background(), "evalsha", "abcdef", 1, "rl:ip:203.0.113.7", 1000, 60, "member")
	err := h.ProcessHook(func(context.Context, redis.Cmder) error { return nil })(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if e := json.Unmarshal(buf.Bytes(), &m); e != nil {
		t.Fatalf("not JSON: %q", buf.String())
	}
	if m["msg"] != "redis" || m["command"] != "evalsha" || m["level"] != "INFO" {
		t.Errorf("unexpected line: %v", m)
	}
	if strings.Contains(buf.String(), "203.0.113.7") || strings.Contains(buf.String(), "rl:ip") {
		t.Errorf("key leaked: %s", buf.String())
	}
}

func TestRedisCommandLogFailureIsAWarningAndPassesTheErrorOn(t *testing.T) {
	var buf bytes.Buffer
	h := cmdLog{log: logging.New(&buf, "web", slog.LevelDebug)}
	boom := errors.New("connection refused")
	cmd := redis.NewCmd(context.Background(), "ping")
	if err := h.ProcessHook(func(context.Context, redis.Cmder) error { return boom })(context.Background(), cmd); !errors.Is(err, boom) {
		t.Fatalf("hook must return the original error, got %v", err)
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) || !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("failure not logged as a warning: %s", buf.String())
	}
}

func TestRedisHandshakeAndNoscriptAreNotWarnings(t *testing.T) {
	var buf bytes.Buffer
	h := cmdLog{log: logging.New(&buf, "web", slog.LevelDebug)}
	ctx := context.Background()
	run := func(cmd redis.Cmder, err error) {
		_ = h.ProcessHook(func(context.Context, redis.Cmder) error { return err })(ctx, cmd)
	}
	run(redis.NewCmd(ctx, "hello", 3), nil)
	run(redis.NewCmd(ctx, "client", "maint_notifications", "on"), errors.New("ERR unknown subcommand 'maint_notifications'"))
	if buf.Len() != 0 {
		t.Errorf("connection handshake must not be logged: %s", buf.String())
	}
	run(redis.NewCmd(ctx, "evalsha", "sha", 1, "k"), errors.New("NOSCRIPT No matching script. Please use EVAL."))
	if strings.Contains(buf.String(), `"level":"WARN"`) || !strings.Contains(buf.String(), "script not cached yet") {
		t.Errorf("NOSCRIPT is normal on first use: %s", buf.String())
	}
}
