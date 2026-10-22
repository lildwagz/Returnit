package database

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/logging"
)

const maxSQLLogBytes = 600

// WithQueryLog logs every statement the pool runs (LOG_SQL=on): the SQL text with its $1, $2 placeholders,
// the NUMBER of arguments, how long it took and what it returned. Argument VALUES are never logged: they
// hold e-mail addresses, product names, push tokens. The line uses the request-scoped logger when the
// statement runs inside a request, so it carries the same request_id as the request line.
func WithQueryLog(l *slog.Logger) Option {
	return func(c *pgxpool.Config) { c.ConnConfig.Tracer = &queryTracer{log: l} }
}

type queryTracer struct{ log *slog.Logger }

type queryStart struct {
	at   time.Time
	sql  string
	args int
}

type startKey struct{}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, startKey{}, queryStart{at: time.Now(), sql: d.SQL, args: len(d.Args)})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	s, _ := ctx.Value(startKey{}).(queryStart)
	l := logging.FromContext(ctx)
	if l == slog.Default() {
		l = t.log
	}
	attrs := []any{
		"sql", squash(s.sql), "args", s.args,
		"duration_ms", float64(time.Since(s.at).Microseconds()) / 1000,
		"result", d.CommandTag.String(),
	}
	if err := d.Err; err != nil {
		attrs = append(attrs, "err", safeErr(err))
		if errors.Is(err, pgx.ErrNoRows) {
			l.Info("sql", attrs...) // "not found" is an ordinary answer, not a failure
			return
		}
		l.Warn("sql", attrs...)
		return
	}
	l.Info("sql", attrs...)
}

// squash puts the statement on one line and cuts it, so a log line stays one readable line.
func squash(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxSQLLogBytes {
		s = s[:maxSQLLogBytes] + "..."
	}
	return s
}

// safeErr describes a failure without the row data Postgres puts in its error detail
// (for example the e-mail address in "Key (email)=(...) already exists").
func safeErr(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return "postgres " + pe.Code + ": " + pe.Message
	}
	return err.Error()
}
