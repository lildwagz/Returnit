package receipts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(p *pgxpool.Pool) *PGRepo { return &PGRepo{pool: p} }

func (r *PGRepo) Owns(ctx context.Context, userID, purchaseID uuid.UUID) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM purchases WHERE id = $1 AND user_id = $2)`, purchaseID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("receipts: owns: %w", err)
	}
	return ok, nil
}

func (r *PGRepo) Upsert(ctx context.Context, userID, purchaseID uuid.UUID, rec Record) (*string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("receipts: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the owned purchase row so concurrent uploads serialise.
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM purchases WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		purchaseID, userID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("receipts: lock: %w", err)
	}
	var old *string
	if err := tx.QueryRow(ctx, `SELECT storage_key FROM receipts WHERE purchase_id = $1`, purchaseID).Scan(&old); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("receipts: old key: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO receipts (purchase_id, storage_key, file_name, content_type, size_bytes, uploaded_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (purchase_id) DO UPDATE SET storage_key = EXCLUDED.storage_key,
  file_name = EXCLUDED.file_name, content_type = EXCLUDED.content_type,
  size_bytes = EXCLUDED.size_bytes, uploaded_at = EXCLUDED.uploaded_at`,
		purchaseID, rec.StorageKey, rec.FileName, rec.ContentType, rec.SizeBytes, rec.UploadedAt); err != nil {
		return nil, fmt.Errorf("receipts: upsert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("receipts: commit: %w", err)
	}
	return old, nil
}

func (r *PGRepo) Get(ctx context.Context, userID, purchaseID uuid.UUID) (Record, error) {
	var rec Record
	err := r.pool.QueryRow(ctx, `
SELECT r.storage_key, r.file_name, r.content_type, r.size_bytes, r.uploaded_at
FROM receipts r JOIN purchases p ON p.id = r.purchase_id
WHERE p.id = $1 AND p.user_id = $2`, purchaseID, userID).
		Scan(&rec.StorageKey, &rec.FileName, &rec.ContentType, &rec.SizeBytes, &rec.UploadedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("receipts: get: %w", err)
	}
	return rec, nil
}

func (r *PGRepo) Delete(ctx context.Context, userID, purchaseID uuid.UUID) (string, error) {
	var key string
	err := r.pool.QueryRow(ctx, `
DELETE FROM receipts r USING purchases p
WHERE r.purchase_id = p.id AND p.id = $1 AND p.user_id = $2
RETURNING r.storage_key`, purchaseID, userID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("receipts: delete: %w", err)
	}
	return key, nil
}
