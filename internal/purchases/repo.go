package purchases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/buckets"
)

type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(p *pgxpool.Pool) *PGRepo { return &PGRepo{pool: p} }

const cols = `p.id, p.product_name, p.store_name, p.purchase_date, p.price::float8, p.currency,
 p.warranty_start_date, p.warranty_end_date, p.return_deadline, p.category, p.notes, p.created_at, p.updated_at,
 rc.items, p.bucket_id, COALESCE(NULLIF(trim(au.display_name), ''), split_part(au.email, '@', 1)), p.user_id`

// Receipts as one JSON array per purchase (oldest first), so a purchase is still one row however many it has.
const from = ` FROM purchases p LEFT JOIN users au ON au.id = p.user_id LEFT JOIN LATERAL (
  SELECT jsonb_agg(jsonb_build_object('id', r.id, 'fileName', r.file_name, 'contentType', r.content_type,
                                      'sizeBytes', r.size_bytes, 'uploadedAt', r.uploaded_at) ORDER BY r.uploaded_at, r.id) AS items
  FROM receipts r WHERE r.purchase_id = p.id) rc ON true`

// scan reads one purchase row; viewer is the person asking, to tell whether they added it.
func scan(row pgx.Row, viewer uuid.UUID) (Purchase, error) {
	var p Purchase
	var pd time.Time
	var ws, we, rd *time.Time
	var items []byte
	var adder *uuid.UUID
	if err := row.Scan(&p.ID, &p.ProductName, &p.StoreName, &pd, &p.Price, &p.Currency,
		&ws, &we, &rd, &p.Category, &p.Notes, &p.CreatedAt, &p.UpdatedAt, &items, &p.BucketID, &p.AddedBy, &adder); err != nil {
		return Purchase{}, err
	}
	p.AddedByMe = adder != nil && *adder == viewer
	p.Receipts = []ReceiptInfo{}
	if len(items) > 0 {
		if err := json.Unmarshal(items, &p.Receipts); err != nil {
			return Purchase{}, fmt.Errorf("purchases: receipts: %w", err)
		}
		if len(p.Receipts) > 0 {
			first := p.Receipts[0]
			p.Receipt = &first
		}
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

// requireMember rejects a bucket the person is not in. It is a validation error (the person named a bucket), and it
// stops a purchase from being put into, or moved into, someone else's bucket.
func (r *PGRepo) requireMember(ctx context.Context, userID uuid.UUID, bucket *uuid.UUID) error {
	if bucket == nil {
		return nil
	}
	var ok bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bucket_members WHERE bucket_id = $1 AND user_id = $2)`,
		*bucket, userID).Scan(&ok); err != nil {
		return mapErr(err)
	}
	if !ok {
		return verr("bucketId is not one of your buckets")
	}
	return nil
}

func (r *PGRepo) Create(ctx context.Context, userID uuid.UUID, in Input) (Purchase, error) {
	if err := r.requireMember(ctx, userID, in.BucketID); err != nil {
		return Purchase{}, err
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
INSERT INTO purchases (user_id, product_name, store_name, purchase_date, price, currency,
  warranty_start_date, warranty_end_date, return_deadline, notes, category, bucket_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, // no bucket: the trigger uses the personal bucket
		userID, in.ProductName, in.StoreName, in.PurchaseDate, in.Price, in.Currency,
		in.WarrantyStartDate, in.WarrantyEndDate, in.ReturnDeadline, in.Notes, in.Category.Value, in.BucketID).Scan(&id)
	if err != nil {
		return Purchase{}, mapErr(err)
	}
	return r.Get(ctx, userID, id)
}

func (r *PGRepo) Get(ctx context.Context, userID, id uuid.UUID) (Purchase, error) {
	p, err := scan(r.pool.QueryRow(ctx,
		`SELECT `+cols+from+` WHERE p.id = $1 AND `+buckets.Visible("p.bucket_id", "$2"), id, userID), userID)
	return p, mapErr(err)
}

func (r *PGRepo) Update(ctx context.Context, userID, id uuid.UUID, in Input) (Purchase, error) {
	if err := r.requireMember(ctx, userID, in.BucketID); err != nil {
		return Purchase{}, err
	}
	tag, err := r.pool.Exec(ctx, `
UPDATE purchases SET product_name=$3, store_name=$4, purchase_date=$5, price=$6, currency=$7,
  warranty_start_date=$8, warranty_end_date=$9, return_deadline=$10, notes=$11,
  category = CASE WHEN $12 THEN $13 ELSE category END, bucket_id = COALESCE($14, bucket_id), updated_at=now()
WHERE id=$1 AND `+buckets.Visible("bucket_id", "$2"),
		id, userID, in.ProductName, in.StoreName, in.PurchaseDate, in.Price, in.Currency,
		in.WarrantyStartDate, in.WarrantyEndDate, in.ReturnDeadline, in.Notes, in.Category.Set, in.Category.Value, in.BucketID)
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
  WHERE p.id = $1 AND `+buckets.Visible("p.bucket_id", "$2")+`
), d AS (DELETE FROM purchases WHERE id = $1 AND `+buckets.Visible("bucket_id", "$2")+` RETURNING id)
SELECT d.id, (SELECT storage_key FROM k LIMIT 1) FROM d`, id, userID).Scan(&deleted, &key)
	if err != nil {
		return nil, mapErr(err)
	}
	return key, nil
}

var visibleToUser = buckets.Visible("p.bucket_id", "$1")

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

var listWhere = `
WHERE ` + visibleToUser + `
  AND ($2::text = '' OR p.product_name ILIKE '%' || $2 || '%' OR p.store_name ILIKE '%' || $2 || '%')
  AND ($3::text = '' OR CASE
        WHEN p.warranty_end_date IS NULL THEN 'none'
        WHEN p.warranty_end_date < CURRENT_DATE THEN 'expired'
        WHEN p.warranty_end_date <= CURRENT_DATE + 30 THEN 'expiring'
        ELSE 'active' END = $3)
  AND ($4::text = '' OR p.category = $4)
  AND ($5::uuid IS NULL OR p.bucket_id = $5)`

func (r *PGRepo) List(ctx context.Context, userID uuid.UUID, f ListFilter) ([]Purchase, int, error) {
	q := likeEscaper.Replace(f.Query)
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM purchases p`+listWhere, userID, q, f.Status, f.Category, f.BucketID).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	order := ` ORDER BY p.purchase_date DESC, p.id`
	if f.Sort == SortWarrantyEnd {
		order = ` ORDER BY CASE WHEN p.warranty_end_date IS NULL THEN 2 WHEN p.warranty_end_date < CURRENT_DATE THEN 1 ELSE 0 END,
		  CASE WHEN p.warranty_end_date >= CURRENT_DATE THEN p.warranty_end_date END ASC,
		  p.warranty_end_date DESC, p.purchase_date DESC, p.id`
	}
	rows, err := r.pool.Query(ctx, `SELECT `+cols+from+listWhere+
		order+` LIMIT $6 OFFSET $7`,
		userID, q, f.Status, f.Category, f.BucketID, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()
	items := []Purchase{}
	for rows.Next() {
		p, err := scan(rows, userID)
		if err != nil {
			return nil, 0, mapErr(err)
		}
		items = append(items, p)
	}
	return items, total, mapErr(rows.Err())
}

func (r *PGRepo) Summary(ctx context.Context, userID uuid.UUID, bucket *uuid.UUID) (Summary, error) {
	var s Summary
	s.CoveredValue = []CurrencyAmount{}
	// Same boundaries as listWhere: expired before today, expiring within 30 days (today included), otherwise active.
	if err := r.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE warranty_end_date > CURRENT_DATE + 30),
       count(*) FILTER (WHERE warranty_end_date BETWEEN CURRENT_DATE AND CURRENT_DATE + 30),
       count(*) FILTER (WHERE warranty_end_date < CURRENT_DATE),
       count(*) FILTER (WHERE warranty_end_date IS NULL),
       count(*)
FROM purchases p WHERE `+visibleToUser+` AND ($2::uuid IS NULL OR p.bucket_id = $2)`, userID, bucket).
		Scan(&s.Counts.Active, &s.Counts.Expiring, &s.Counts.Expired, &s.Counts.Unknown, &s.Counts.Total); err != nil {
		return Summary{}, mapErr(err)
	}

	rows, err := r.pool.Query(ctx, `
SELECT currency, sum(price)::float8 FROM purchases p
WHERE `+visibleToUser+` AND ($2::uuid IS NULL OR p.bucket_id = $2) AND price IS NOT NULL AND warranty_end_date >= CURRENT_DATE
GROUP BY currency ORDER BY currency`, userID, bucket)
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
		err := r.pool.QueryRow(ctx, `SELECT id, product_name, `+column+` FROM purchases p
WHERE `+visibleToUser+` AND ($2::uuid IS NULL OR p.bucket_id = $2) AND `+column+` >= CURRENT_DATE ORDER BY `+column+`, id LIMIT 1`, userID, bucket).Scan(&it.ID, &it.ProductName, &d)
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
