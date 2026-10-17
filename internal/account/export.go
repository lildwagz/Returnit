package account

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/reminders"
)

// ExportFormat identifies the document layout so future changes can be told apart.
const ExportFormat = "returnit-export/1"

type exportReceipt struct {
	FileName     string    `json:"fileName"`
	ContentType  string    `json:"contentType"`
	SizeBytes    int64     `json:"sizeBytes"`
	UploadedAt   time.Time `json:"uploadedAt"`
	DownloadPath string    `json:"downloadPath"` // fetch the file with GET (authenticated); see the API docs
}

type exportPurchase struct {
	ID                uuid.UUID       `json:"id"`
	ProductName       string          `json:"productName"`
	StoreName         *string         `json:"storeName"`
	PurchaseDate      purchases.Date  `json:"purchaseDate"`
	Price             *float64        `json:"price"`
	Currency          string          `json:"currency"`
	WarrantyStartDate *purchases.Date `json:"warrantyStartDate"`
	WarrantyEndDate   *purchases.Date `json:"warrantyEndDate"`
	Notes             *string         `json:"notes"`
	Receipt           *exportReceipt  `json:"receipt"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

type exportNotification struct {
	ID         uuid.UUID  `json:"id"`
	PurchaseID *uuid.UUID `json:"purchaseId"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	SentAt     time.Time  `json:"sentAt"`
	Read       bool       `json:"read"`
}

type exportDevice struct {
	Platform     string    `json:"platform"`
	RegisteredAt time.Time `json:"registeredAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

// Export streams everything ReturnIt stores about the user as one JSON document (FR-16):
// profile, reminder preferences, purchases with receipt metadata, delivered notifications and
// registered phones (platform and dates only).
//
// Deliberately excluded: internal identifiers that are not the user's data (storage keys, the
// identity-provider subject, idempotency keys, push tokens) and the receipt files themselves, which stay in
// private storage; each purchase carries a downloadPath for fetching its file.
//
// All reads share one REPEATABLE READ snapshot, so the document is internally consistent. The
// document is written incrementally and the closing brace is only written on success: if an error
// happens mid-stream the output is invalid JSON, so a client can never mistake a truncated export
// for a complete one.
func (s *Service) Export(ctx context.Context, userID uuid.UUID, out io.Writer) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("account: export begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var a Account
	err = tx.QueryRow(ctx, `
SELECT id, email, display_name, created_at, deletion_scheduled_at FROM users WHERE id = $1`, userID).
		Scan(&a.ID, &a.Email, &a.DisplayName, &a.CreatedAt, &a.DeletionScheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound // nothing has been written yet
	}
	if err != nil {
		return fmt.Errorf("account: export profile: %w", err)
	}

	prefs, err := exportPrefs(ctx, tx, userID)
	if err != nil {
		return err
	}

	w := bufio.NewWriterSize(out, 32<<10)
	put := func(format string, args ...any) error { _, err := fmt.Fprintf(w, format, args...); return err }
	enc := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("account: export encode: %w", err)
		}
		_, err = w.Write(b)
		return err
	}

	if err := put(`{"format":%q,"exportedAt":%q,"account":`, ExportFormat, s.now().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := enc(a); err != nil {
		return err
	}
	if err := put(`,"reminderPreferences":`); err != nil {
		return err
	}
	if err := enc(prefs); err != nil { // null when the user never saved any
		return err
	}

	// Purchases, streamed row by row.
	if err := put(`,"purchases":[`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
SELECT p.id, p.product_name, p.store_name, p.purchase_date, p.price::float8, p.currency,
       p.warranty_start_date, p.warranty_end_date, p.notes, p.created_at, p.updated_at,
       r.file_name, r.content_type, r.size_bytes, r.uploaded_at
FROM purchases p LEFT JOIN receipts r ON r.purchase_id = p.id
WHERE p.user_id = $1
ORDER BY p.purchase_date DESC, p.id`, userID)
	if err != nil {
		return fmt.Errorf("account: export purchases: %w", err)
	}
	first := true
	for rows.Next() {
		var p exportPurchase
		var pd time.Time
		var ws, we *time.Time
		var rf, rc *string
		var rs *int64
		var ru *time.Time
		if err := rows.Scan(&p.ID, &p.ProductName, &p.StoreName, &pd, &p.Price, &p.Currency, &ws, &we,
			&p.Notes, &p.CreatedAt, &p.UpdatedAt, &rf, &rc, &rs, &ru); err != nil {
			rows.Close()
			return fmt.Errorf("account: export purchases scan: %w", err)
		}
		p.PurchaseDate = purchases.Date{Time: pd}
		if ws != nil {
			p.WarrantyStartDate = &purchases.Date{Time: *ws}
		}
		if we != nil {
			p.WarrantyEndDate = &purchases.Date{Time: *we}
		}
		if rf != nil && rc != nil && rs != nil && ru != nil {
			p.Receipt = &exportReceipt{FileName: *rf, ContentType: *rc, SizeBytes: *rs, UploadedAt: *ru,
				DownloadPath: "/api/v1/purchases/" + p.ID.String() + "/receipt"}
		}
		if !first {
			if err := put(","); err != nil {
				rows.Close()
				return err
			}
		}
		first = false
		if err := enc(p); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("account: export purchases rows: %w", err)
	}

	// Delivered notifications (scheduled/cancelled/failed rows are internal bookkeeping).
	if err := put(`],"notifications":[`); err != nil {
		return err
	}
	nrows, err := tx.Query(ctx, `
SELECT id, purchase_id, title, body, sent_at, read_at IS NOT NULL
FROM notifications WHERE user_id = $1 AND status = 'sent' ORDER BY sent_at DESC, id`, userID)
	if err != nil {
		return fmt.Errorf("account: export notifications: %w", err)
	}
	first = true
	for nrows.Next() {
		var n exportNotification
		if err := nrows.Scan(&n.ID, &n.PurchaseID, &n.Title, &n.Body, &n.SentAt, &n.Read); err != nil {
			nrows.Close()
			return fmt.Errorf("account: export notifications scan: %w", err)
		}
		if !first {
			if err := put(","); err != nil {
				nrows.Close()
				return err
			}
		}
		first = false
		if err := enc(n); err != nil {
			nrows.Close()
			return err
		}
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return fmt.Errorf("account: export notifications rows: %w", err)
	}

	// Registered phones: which platforms, never the push tokens.
	if err := put(`],"devices":[`); err != nil {
		return err
	}
	drows, err := tx.Query(ctx, `SELECT platform, created_at, last_seen_at FROM devices WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return fmt.Errorf("account: export devices: %w", err)
	}
	first = true
	for drows.Next() {
		var d exportDevice
		if err := drows.Scan(&d.Platform, &d.RegisteredAt, &d.LastSeenAt); err != nil {
			drows.Close()
			return fmt.Errorf("account: export devices scan: %w", err)
		}
		if !first {
			if err := put(","); err != nil {
				drows.Close()
				return err
			}
		}
		first = false
		if err := enc(d); err != nil {
			drows.Close()
			return err
		}
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return fmt.Errorf("account: export devices rows: %w", err)
	}

	if err := put("]}\n"); err != nil { // the document is only complete once this is written
		return err
	}
	return w.Flush()
}

func exportPrefs(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*reminders.Preferences, error) {
	var p reminders.Preferences
	var raw []byte
	err := tx.QueryRow(ctx, `
SELECT enabled, days_before, channel, to_char(quiet_hours_start, 'HH24:MI'), to_char(quiet_hours_end, 'HH24:MI'), timezone
FROM reminder_preferences WHERE user_id = $1`, userID).
		Scan(&p.Enabled, &raw, &p.Channel, &p.QuietHoursStart, &p.QuietHoursEnd, &p.Timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("account: export preferences: %w", err)
	}
	if err := json.Unmarshal(raw, &p.DaysBefore); err != nil {
		return nil, fmt.Errorf("account: export preferences decode: %w", err)
	}
	return &p, nil
}
