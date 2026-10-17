// Package retention enforces the storage retention policy: queued receipt files are deleted
// (with retry) and objects that no longer have a database row are swept. The worker process
// runs PurgeQueued and SweepOrphans as periodic jobs; the web process can request a purge.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/storage"
)

const (
	receiptPrefix = "receipts/"
	batchSize     = 100
	maxBackoff    = time.Hour
)

type Worker struct {
	Pool  *pgxpool.Pool
	Store storage.Store
	Log   *slog.Logger
	// OrphanGrace protects in-flight uploads (object is stored before its row is committed).
	OrphanGrace time.Duration
}

// PurgeQueued deletes objects listed in object_deletions. Failures are retried with
// exponential backoff (1m, 2m, 4m ... capped at 1h) and never lose the key.
func (w *Worker) PurgeQueued(ctx context.Context) (int, error) {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("retention: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT id, storage_key, attempts FROM object_deletions
WHERE next_attempt_at <= now() ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("retention: select: %w", err)
	}
	type item struct {
		id       int64
		key      string
		attempts int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.key, &it.attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("retention: scan: %w", err)
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("retention: rows: %w", err)
	}

	done := 0
	for _, it := range items {
		// Deleting a missing object is a no-op in S3, so retries and the immediate
		// best-effort delete in the request path are safe.
		if err := w.Store.Delete(ctx, it.key); err != nil {
			backoff := min(time.Minute<<min(it.attempts, 10), maxBackoff)
			if _, e := tx.Exec(ctx, `
UPDATE object_deletions SET attempts = attempts + 1, last_error = $2,
  next_attempt_at = now() + $3::interval WHERE id = $1`,
				it.id, truncate(err.Error(), 500), fmt.Sprintf("%d seconds", int(backoff.Seconds()))); e != nil {
				return done, fmt.Errorf("retention: reschedule: %w", e)
			}
			w.Log.Warn("object deletion failed, will retry", "attempts", it.attempts+1, "err", err)
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM object_deletions WHERE id = $1`, it.id); err != nil {
			return done, fmt.Errorf("retention: dequeue: %w", err)
		}
		done++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("retention: commit: %w", err)
	}
	return done, nil
}

// SweepOrphans removes objects older than OrphanGrace that no receipt row references and
// that are not already queued for deletion (e.g. left behind by a crash mid-upload).
func (w *Worker) SweepOrphans(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-w.OrphanGrace)
	removed := 0
	err := w.Store.List(ctx, receiptPrefix, func(key string, modified time.Time) error {
		if modified.After(cutoff) {
			return nil
		}
		var referenced bool
		if err := w.Pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM receipts WHERE storage_key = $1)
    OR EXISTS (SELECT 1 FROM object_deletions WHERE storage_key = $1)`, key).Scan(&referenced); err != nil {
			return fmt.Errorf("retention: check key: %w", err)
		}
		if referenced {
			return nil
		}
		if err := w.Store.Delete(ctx, key); err != nil {
			w.Log.Warn("orphan deletion failed", "err", err)
			return nil
		}
		removed++
		return nil
	})
	return removed, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
