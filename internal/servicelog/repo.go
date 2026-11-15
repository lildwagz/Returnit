package servicelog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/buckets"
)

type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(p *pgxpool.Pool) *PGRepo { return &PGRepo{pool: p} }

// The caller's id is $2 in every query; visibility follows the purchase's bucket.
const selectCols = `SELECT s.id, s.purchase_id, s.service_date, s.place, s.problem, s.result, s.cost::float8,
  COALESCE(NULLIF(trim(u.display_name), ''), split_part(u.email, '@', 1)), s.added_by, s.created_at, s.updated_at
FROM service_records s JOIN purchases p ON p.id = s.purchase_id LEFT JOIN users u ON u.id = s.added_by`

func scan(row pgx.Row, viewer uuid.UUID) (Record, error) {
	var r Record
	var adder *uuid.UUID
	if err := row.Scan(&r.ID, &r.PurchaseID, &r.ServiceDate.Time, &r.Place, &r.Problem, &r.Result, &r.Cost,
		&r.AddedBy, &adder, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return Record{}, err
	}
	r.AddedByMe = adder != nil && *adder == viewer
	return r, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("servicelog: %w", err)
}

func (r *PGRepo) visible(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, purchaseID uuid.UUID, lock bool) error {
	sql := `SELECT id FROM purchases WHERE id = $1 AND ` + buckets.Visible("bucket_id", "$2")
	if lock {
		sql += ` FOR UPDATE`
	}
	var id uuid.UUID
	return mapErr(q.QueryRow(ctx, sql, purchaseID, userID).Scan(&id))
}

func (r *PGRepo) List(ctx context.Context, userID, purchaseID uuid.UUID) ([]Record, error) {
	if err := r.visible(ctx, r.pool, userID, purchaseID, false); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, selectCols+` WHERE s.purchase_id = $1 AND `+buckets.Visible("p.bucket_id", "$2")+`
ORDER BY s.service_date DESC, s.created_at DESC, s.id`, purchaseID, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		rec, err := scan(rows, userID)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, rec)
	}
	return out, mapErr(rows.Err())
}

func (r *PGRepo) get(ctx context.Context, userID, purchaseID, id uuid.UUID) (Record, error) {
	rec, err := scan(r.pool.QueryRow(ctx, selectCols+` WHERE s.id = $3 AND s.purchase_id = $1 AND `+
		buckets.Visible("p.bucket_id", "$2"), purchaseID, userID, id), userID)
	return rec, mapErr(err)
}

func (r *PGRepo) Create(ctx context.Context, userID, purchaseID uuid.UUID, in Input, limit int) (Record, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Record{}, mapErr(err)
	}
	defer tx.Rollback(ctx)
	// Locking the purchase serialises concurrent adds, so the limit holds.
	if err := r.visible(ctx, tx, userID, purchaseID, true); err != nil {
		return Record{}, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM service_records WHERE purchase_id = $1`, purchaseID).Scan(&n); err != nil {
		return Record{}, mapErr(err)
	}
	if n >= limit {
		return Record{}, ErrLimit
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
INSERT INTO service_records (purchase_id, added_by, service_date, place, problem, result, cost)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		purchaseID, userID, in.ServiceDate, in.Place, in.Problem, in.Result, in.Cost).Scan(&id); err != nil {
		return Record{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, mapErr(err)
	}
	return r.get(ctx, userID, purchaseID, id)
}

func (r *PGRepo) Update(ctx context.Context, userID, purchaseID, id uuid.UUID, in Input) (Record, error) {
	tag, err := r.pool.Exec(ctx, `
UPDATE service_records s SET service_date = $4, place = $5, problem = $6, result = $7, cost = $8, updated_at = now()
FROM purchases p
WHERE s.id = $3 AND s.purchase_id = $1 AND p.id = s.purchase_id AND `+buckets.Visible("p.bucket_id", "$2"),
		purchaseID, userID, id, in.ServiceDate, in.Place, in.Problem, in.Result, in.Cost)
	if err != nil {
		return Record{}, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return Record{}, ErrNotFound
	}
	return r.get(ctx, userID, purchaseID, id)
}

func (r *PGRepo) Delete(ctx context.Context, userID, purchaseID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
DELETE FROM service_records s USING purchases p
WHERE s.id = $3 AND s.purchase_id = $1 AND p.id = s.purchase_id AND `+buckets.Visible("p.bucket_id", "$2"),
		purchaseID, userID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
