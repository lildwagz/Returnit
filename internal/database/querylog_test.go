package database

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/returnit/returnit/internal/logging"
)

func traced(t *testing.T, ctx context.Context, sql string, args []any, tag string, err error) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	tr := &queryTracer{log: logging.New(&buf, "web", slog.LevelDebug)}
	ctx = tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag(tag), Err: err})
	var m map[string]any
	if e := json.Unmarshal(buf.Bytes(), &m); e != nil {
		t.Fatalf("not one JSON line: %q", buf.String())
	}
	return m
}

func TestQueryLogHasSQLCountAndDurationButNotValues(t *testing.T) {
	m := traced(t, context.Background(),
		"SELECT id, email\n   FROM users\n WHERE email = $1 AND display_name = $2", []any{"alice@example.com", "Alice Secret"}, "SELECT 1", nil)
	if m["msg"] != "sql" || m["level"] != "INFO" || m["args"] != float64(2) || m["result"] != "SELECT 1" {
		t.Fatalf("unexpected line: %v", m)
	}
	if m["sql"] != "SELECT id, email FROM users WHERE email = $1 AND display_name = $2" {
		t.Errorf("sql not squashed to one line: %q", m["sql"])
	}
	if _, ok := m["duration_ms"].(float64); !ok {
		t.Errorf("duration missing: %v", m)
	}
	raw, _ := json.Marshal(m)
	if strings.Contains(string(raw), "alice@example.com") || strings.Contains(string(raw), "Alice Secret") {
		t.Errorf("argument values leaked: %s", raw)
	}
}

func TestQueryLogLongSQLIsCut(t *testing.T) {
	m := traced(t, context.Background(), "SELECT "+strings.Repeat("x, ", 1000)+"1", nil, "SELECT 0", nil)
	if s := m["sql"].(string); len(s) > maxSQLLogBytes+3 || !strings.HasSuffix(s, "...") {
		t.Errorf("not cut: %d bytes", len(s))
	}
}

func TestQueryLogErrors(t *testing.T) {
	pe := &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"users_email_key\"",
		Detail: "Key (email)=(alice@example.com) already exists."}
	m := traced(t, context.Background(), "INSERT INTO users (email) VALUES ($1)", []any{"alice@example.com"}, "", pe)
	if m["level"] != "WARN" {
		t.Errorf("a failed statement should be a warning: %v", m["level"])
	}
	raw, _ := json.Marshal(m)
	if !strings.Contains(string(raw), "23505") || strings.Contains(string(raw), "alice@example.com") {
		t.Errorf("error must carry the code but not the row data: %s", raw)
	}

	m = traced(t, context.Background(), "SELECT 1 FROM users WHERE id = $1", []any{1}, "", pgx.ErrNoRows)
	if m["level"] != "INFO" {
		t.Errorf("no rows is not a failure: %v", m["level"])
	}
}

func TestQueryLogUsesTheRequestLoggerWhenThereIsOne(t *testing.T) {
	var buf bytes.Buffer
	req := logging.New(&buf, "web", slog.LevelDebug).With("request_id", "abc-123")
	ctx := logging.WithContext(context.Background(), req)
	tr := &queryTracer{log: logging.New(&bytes.Buffer{}, "web", slog.LevelDebug)}
	ctx = tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	if !strings.Contains(buf.String(), `"request_id":"abc-123"`) {
		t.Errorf("query line lacks the request id: %s", buf.String())
	}
}
