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

// A purchase has up to MaxPerPurchase receipts. Where an operation names no receipt (the original, single-receipt API),
// it means the oldest one: ORDER BY uploaded_at, id.

func (r *PGRepo) Owns(ctx context.Context, userID, purchaseID uuid.UUID) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM purchases WHERE id = $1 AND user_id = $2)`, purchaseID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("receipts: owns: %w", err)
	}
	return ok, nil
}

// lockPurchase locks the owned purchase row so concurrent uploads for it serialise (the count limit relies on it).
func lockPurchase(ctx context.Context, tx pgx.Tx, userID, purchaseID uuid.UUID) error {
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM purchases WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		purchaseID, userID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("receipts: lock: %w", err)
	}
	return nil
}

const insertReceipt = `
INSERT INTO receipts (purchase_id, storage_key, file_name, content_type, size_bytes, uploaded_at)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`

// Add stores one more receipt, refusing with ErrLimit when the purchase already has `limit`.
func (r *PGRepo) Add(ctx context.Context, userID, purchaseID uuid.UUID, rec Record, limit int) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("receipts: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockPurchase(ctx, tx, userID, purchaseID); err != nil {
		return uuid.Nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM receipts WHERE purchase_id = $1`, purchaseID).Scan(&n); err != nil {
		return uuid.Nil, fmt.Errorf("receipts: count: %w", err)
	}
	if n >= limit {
		return uuid.Nil, ErrLimit
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, insertReceipt, purchaseID, rec.StorageKey, rec.FileName, rec.ContentType, rec.SizeBytes, rec.UploadedAt).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("receipts: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("receipts: commit: %w", err)
	}
	return id, nil
}

// Upsert is the original single-receipt upload: it replaces the oldest receipt, or adds the first one.
// It returns the replaced storage key (nil when nothing was replaced) and the id of the stored receipt.
func (r *PGRepo) Upsert(ctx context.Context, userID, purchaseID uuid.UUID, rec Record) (*string, uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("receipts: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockPurchase(ctx, tx, userID, purchaseID); err != nil {
		return nil, uuid.Nil, err
	}
	var id uuid.UUID
	var old string
	err = tx.QueryRow(ctx, `SELECT id, storage_key FROM receipts WHERE purchase_id = $1 ORDER BY uploaded_at, id LIMIT 1`, purchaseID).Scan(&id, &old)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, insertReceipt, purchaseID, rec.StorageKey, rec.FileName, rec.ContentType, rec.SizeBytes, rec.UploadedAt).Scan(&id); err != nil {
			return nil, uuid.Nil, fmt.Errorf("receipts: insert: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, uuid.Nil, fmt.Errorf("receipts: commit: %w", err)
		}
		return nil, id, nil
	case err != nil:
		return nil, uuid.Nil, fmt.Errorf("receipts: oldest: %w", err)
	}
	// The trigger on storage_key queues the replaced file for deletion in this same transaction.
	if _, err := tx.Exec(ctx, `
UPDATE receipts SET storage_key = $2, file_name = $3, content_type = $4, size_bytes = $5, uploaded_at = $6 WHERE id = $1`,
		id, rec.StorageKey, rec.FileName, rec.ContentType, rec.SizeBytes, rec.UploadedAt); err != nil {
		return nil, uuid.Nil, fmt.Errorf("receipts: replace: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, uuid.Nil, fmt.Errorf("receipts: commit: %w", err)
	}
	return &old, id, nil
}

const selectRecord = `
SELECT r.id, r.storage_key, r.file_name, r.content_type, r.size_bytes, r.uploaded_at
FROM receipts r JOIN purchases p ON p.id = r.purchase_id
WHERE p.id = $1 AND p.user_id = $2`

func (r *PGRepo) scanRecord(ctx context.Context, sql string, args ...any) (Record, error) {
	var rec Record
	err := r.pool.QueryRow(ctx, sql, args...).
		Scan(&rec.ID, &rec.StorageKey, &rec.FileName, &rec.ContentType, &rec.SizeBytes, &rec.UploadedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("receipts: get: %w", err)
	}
	return rec, nil
}

// Get returns the oldest receipt of the purchase.
func (r *PGRepo) Get(ctx context.Context, userID, purchaseID uuid.UUID) (Record, error) {
	return r.scanRecord(ctx, selectRecord+` ORDER BY r.uploaded_at, r.id LIMIT 1`, purchaseID, userID)
}

func (r *PGRepo) GetByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (Record, error) {
	return r.scanRecord(ctx, selectRecord+` AND r.id = $3`, purchaseID, userID, id)
}

func (r *PGRepo) deleteKey(ctx context.Context, sql string, args ...any) (string, error) {
	var key string
	err := r.pool.QueryRow(ctx, sql, args...).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("receipts: delete: %w", err)
	}
	return key, nil
}

// Delete removes the oldest receipt of the purchase.
func (r *PGRepo) Delete(ctx context.Context, userID, purchaseID uuid.UUID) (string, error) {
	return r.deleteKey(ctx, `
DELETE FROM receipts WHERE id = (
  SELECT r.id FROM receipts r JOIN purchases p ON p.id = r.purchase_id
  WHERE p.id = $1 AND p.user_id = $2 ORDER BY r.uploaded_at, r.id LIMIT 1)
RETURNING storage_key`, purchaseID, userID)
}

func (r *PGRepo) DeleteByID(ctx context.Context, userID, purchaseID, id uuid.UUID) (string, error) {
	return r.deleteKey(ctx, `
DELETE FROM receipts r USING purchases p
WHERE r.purchase_id = p.id AND p.id = $1 AND p.user_id = $2 AND r.id = $3
RETURNING r.storage_key`, purchaseID, userID, id)
}
