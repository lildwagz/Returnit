package purchases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(p *pgxpool.Pool) *PGRepo { return &PGRepo{pool: p} }

const cols = `p.id, p.product_name, p.store_name, p.purchase_date, p.price::float8, p.currency,
 p.warranty_start_date, p.warranty_end_date, p.return_deadline, p.category, p.notes, p.created_at, p.updated_at,
 r.file_name, r.content_type, r.size_bytes`

const from = ` FROM purchases p LEFT JOIN receipts r ON r.purchase_id = p.id`

func scan(row pgx.Row) (Purchase, error) {
	var p Purchase
	var pd time.Time
	var ws, we, rd *time.Time
	var rf, rc *string
	var rs *int64
	if err := row.Scan(&p.ID, &p.ProductName, &p.StoreName, &pd, &p.Price, &p.Currency,
		&ws, &we, &rd, &p.Category, &p.Notes, &p.CreatedAt, &p.UpdatedAt, &rf, &rc, &rs); err != nil {
		return Purchase{}, err
	}
	if rf != nil && rc != nil && rs != nil {
		p.Receipt = &ReceiptInfo{FileName: *rf, ContentType: *rc, SizeBytes: *rs}
	}
	p.PurchaseDate = Date{pd}
	if ws != nil {
		p.WarrantyStartDate = &Date{*ws}
	}
	if we != nil {
		p.WarrantyEndDate = &Date{*we}
	}
	if rd != nil {
		p.ReturnDeadline = &Date{*rd}
	}
	return p, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("purchases: %w", err)
}

func (r *PGRepo) Create(ctx context.Context, userID uuid.UUID, in Input) (Purchase, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
INSERT INTO purchases (user_id, product_name, store_name, purchase_date, price, currency,
  warranty_start_date, warranty_end_date, return_deadline, notes, category)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		userID, in.ProductName, in.StoreName, in.PurchaseDate, in.Price, in.Currency,
		in.WarrantyStartDate, in.WarrantyEndDate, in.ReturnDeadline, in.Notes, in.Category.Value).Scan(&id)
	if err != nil {
		return Purchase{}, mapErr(err)
	}
	return r.Get(ctx, userID, id)
}

func (r *PGRepo) Get(ctx context.Context, userID, id uuid.UUID) (Purchase, error) {
	p, err := scan(r.pool.QueryRow(ctx,
		`SELECT `+cols+from+` WHERE p.id = $1 AND p.user_id = $2`, id, userID))
	return p, mapErr(err)
}

func (r *PGRepo) Update(ctx context.Context, userID, id uuid.UUID, in Input) (Purchase, error) {
	tag, err := r.pool.Exec(ctx, `
UPDATE purchases SET product_name=$3, store_name=$4, purchase_date=$5, price=$6, currency=$7,
  warranty_start_date=$8, warranty_end_date=$9, return_deadline=$10, notes=$11,
  category = CASE WHEN $12 THEN $13 ELSE category END, updated_at=now()
WHERE id=$1 AND user_id=$2`,
		id, userID, in.ProductName, in.StoreName, in.PurchaseDate, in.Price, in.Currency,
		in.WarrantyStartDate, in.WarrantyEndDate, in.ReturnDeadline, in.Notes, in.Category.Set, in.Category.Value)
	if err != nil {
		return Purchase{}, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return Purchase{}, ErrNotFound
	}
	return r.Get(ctx, userID, id)
}

func (r *PGRepo) Delete(ctx context.Context, userID, id uuid.UUID) (*string, error) {
	// One statement: capture the receipt key and delete the purchase atomically.
	var deleted uuid.UUID
	var key *string
	err := r.pool.QueryRow(ctx, `
WITH k AS (
  SELECT r.storage_key FROM receipts r JOIN purchases p ON p.id = r.purchase_id
  WHERE p.id = $1 AND p.user_id = $2
), d AS (DELETE FROM purchases WHERE id = $1 AND user_id = $2 RETURNING id)
SELECT d.id, (SELECT storage_key FROM k) FROM d`, id, userID).Scan(&deleted, &key)
	if err != nil {
		return nil, mapErr(err)
	}
	return key, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

const listWhere = `
WHERE p.user_id = $1
  AND ($2::text = '' OR p.product_name ILIKE '%' || $2 || '%' OR p.store_name ILIKE '%' || $2 || '%')
  AND ($3::text = '' OR CASE
        WHEN p.warranty_end_date IS NULL THEN 'none'
        WHEN p.warranty_end_date < CURRENT_DATE THEN 'expired'
        WHEN p.warranty_end_date <= CURRENT_DATE + 30 THEN 'expiring'
        ELSE 'active' END = $3)
  AND ($4::text = '' OR p.category = $4)`

func (r *PGRepo) List(ctx context.Context, userID uuid.UUID, f ListFilter) ([]Purchase, int, error) {
	q := likeEscaper.Replace(f.Query)
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM purchases p`+listWhere, userID, q, f.Status, f.Category).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	rows, err := r.pool.Query(ctx, `SELECT `+cols+from+listWhere+
		` ORDER BY p.purchase_date DESC, p.id LIMIT $5 OFFSET $6`,
		userID, q, f.Status, f.Category, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()
	items := []Purchase{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, 0, mapErr(err)
		}
		items = append(items, p)
	}
	return items, total, mapErr(rows.Err())
}

func (r *PGRepo) Summary(ctx context.Context, userID uuid.UUID) (Summary, error) {
	var s Summary
	s.CoveredValue = []CurrencyAmount{}
	// Same boundaries as listWhere: expired before today, expiring within 30 days (today included), otherwise active.
	if err := r.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE warranty_end_date > CURRENT_DATE + 30),
       count(*) FILTER (WHERE warranty_end_date BETWEEN CURRENT_DATE AND CURRENT_DATE + 30),
       count(*) FILTER (WHERE warranty_end_date < CURRENT_DATE),
       count(*) FILTER (WHERE warranty_end_date IS NULL),
       count(*)
FROM purchases WHERE user_id = $1`, userID).
		Scan(&s.Counts.Active, &s.Counts.Expiring, &s.Counts.Expired, &s.Counts.Unknown, &s.Counts.Total); err != nil {
		return Summary{}, mapErr(err)
	}

	rows, err := r.pool.Query(ctx, `
SELECT currency, sum(price)::float8 FROM purchases
WHERE user_id = $1 AND price IS NOT NULL AND warranty_end_date >= CURRENT_DATE
GROUP BY currency ORDER BY currency`, userID)
	if err != nil {
		return Summary{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a CurrencyAmount
		if err := rows.Scan(&a.Currency, &a.Amount); err != nil {
			return Summary{}, mapErr(err)
		}
		s.CoveredValue = append(s.CoveredValue, a)
	}
	if err := rows.Err(); err != nil {
		return Summary{}, mapErr(err)
	}

	next := func(column string) (*SummaryItem, error) {
		var it SummaryItem
		var d time.Time
		err := r.pool.QueryRow(ctx, `SELECT id, product_name, `+column+` FROM purchases
WHERE user_id = $1 AND `+column+` >= CURRENT_DATE ORDER BY `+column+`, id LIMIT 1`, userID).Scan(&it.ID, &it.ProductName, &d)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, mapErr(err)
		}
		it.Date = Date{d}
		return &it, nil
	}
	var err2 error
	if s.NextExpiring, err2 = next("warranty_end_date"); err2 != nil {
		return Summary{}, err2
	}
	if s.NextReturn, err2 = next("return_deadline"); err2 != nil {
		return Summary{}, err2
	}
	return s, nil
}
