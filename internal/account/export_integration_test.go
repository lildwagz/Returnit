//go:build integration

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/users"
)

type failingWriter struct{ left int }

func (f *failingWriter) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errors.New("client went away")
	}
	f.left -= len(p)
	return len(p), nil
}

func TestExport(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	us := users.NewStore(pool)
	sfx := uuid.NewString()
	aliceSub, bobSub := "idp-subject-a-"+sfx, "idp-subject-b-"+sfx
	alice, _ := us.EnsureBySubject(ctx, aliceSub, "xa-"+sfx+"@example.com", "Alice")
	bob, _ := us.EnsureBySubject(ctx, bobSub, "xb-"+sfx+"@example.com", "Bob")
	empty, _ := us.EnsureBySubject(ctx, "xe-"+sfx, "xe-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{alice, bob, empty} {
			pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
		}
	})

	var laptop, phone, bobItem uuid.UUID
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, store_name, purchase_date, price, currency, warranty_start_date, warranty_end_date, notes)
		VALUES ($1,'Laptop','Toko A','2026-03-01',15000000.5,'IDR','2026-03-01','2028-03-01','serial 123') RETURNING id`, alice).Scan(&laptop)
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date) VALUES ($1,'Phone','2026-05-01') RETURNING id`, alice).Scan(&phone)
	pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date) VALUES ($1,'BOBS SECRET ITEM','2026-05-01') RETURNING id`, bob).Scan(&bobItem)
	storageKey := "receipts/" + alice.String() + "/" + uuid.NewString() + ".pdf"
	pool.Exec(ctx, `INSERT INTO receipts (purchase_id, storage_key, file_name, content_type, size_bytes) VALUES ($1,$2,'invoice.pdf','application/pdf',2048)`, laptop, storageKey)
	pool.Exec(ctx, `INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, quiet_hours_start, quiet_hours_end, timezone)
		VALUES ($1,true,'[30,7]','email','22:00','07:00','Asia/Jakarta')`, alice)
	// One delivered notification (read), one still scheduled and one cancelled (internal, must not appear).
	pool.Exec(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, scheduled_at, sent_at, read_at, status, idempotency_key, channel, offset_days, warranty_end_date)
		VALUES ($1,$2,'Garansi segera berakhir','Garansi Laptop berakhir besok','2026-11-22T02:00:00Z','2026-11-22T02:00:05Z',now(),'sent','exp-sent-'||$3,'push',1,'2028-03-01')`, alice, laptop, sfx)
	pool.Exec(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, scheduled_at, status, idempotency_key, channel, offset_days, warranty_end_date)
		VALUES ($1,$2,'future','future body','2027-01-01T02:00:00Z','scheduled','exp-sched-'||$3,'push',1,'2028-03-01')`, alice, laptop, sfx)
	pool.Exec(ctx, `INSERT INTO notifications (user_id, title, body, status, idempotency_key) VALUES ($1,'cancelled','cancelled body','cancelled','exp-canc-'||$2)`, alice, sfx)

	svc := account.NewService(pool, 7*24*time.Hour)
	svc.Now = func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) }

	var buf bytes.Buffer
	if err := svc.Export(ctx, alice, &buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()

	var doc struct {
		Format, ExportedAt string
		Account            struct {
			ID, Email            string
			DisplayName          *string
			DeletionScheduledFor *string
		}
		ReminderPreferences *struct {
			Enabled         bool
			DaysBefore      []int
			Channel         string
			QuietHoursStart string
			Timezone        string
		}
		Purchases []struct {
			ID, ProductName, PurchaseDate, Currency string
			StoreName, Notes                        *string
			Price                                   *float64
			WarrantyEndDate                         *string
			Receipt                                 *struct {
				FileName, ContentType, DownloadPath string
				SizeBytes                           int64
			}
		}
		Notifications []struct {
			Title string
			Read  bool
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("export is not valid JSON: %v\n%s", err, raw)
	}

	if doc.Format != account.ExportFormat || doc.ExportedAt != "2026-11-22T10:00:00Z" {
		t.Errorf("header: %q %q", doc.Format, doc.ExportedAt)
	}
	if doc.Account.ID != alice.String() || doc.Account.Email != "xa-"+sfx+"@example.com" || *doc.Account.DisplayName != "Alice" {
		t.Errorf("account: %+v", doc.Account)
	}
	if p := doc.ReminderPreferences; p == nil || p.Channel != "email" || len(p.DaysBefore) != 2 || p.QuietHoursStart != "22:00" || p.Timezone != "Asia/Jakarta" {
		t.Errorf("preferences: %+v", p)
	}
	if len(doc.Purchases) != 2 || doc.Purchases[0].ProductName != "Phone" || doc.Purchases[1].ProductName != "Laptop" {
		t.Fatalf("purchases (newest purchase date first): %+v", doc.Purchases)
	}
	lp := doc.Purchases[1]
	if lp.PurchaseDate != "2026-03-01" || *lp.WarrantyEndDate != "2028-03-01" || *lp.Price != 15000000.5 || *lp.Notes != "serial 123" || *lp.StoreName != "Toko A" {
		t.Errorf("laptop fields (dates must be date-only): %+v", lp)
	}
	if lp.Receipt == nil || lp.Receipt.FileName != "invoice.pdf" || lp.Receipt.SizeBytes != 2048 ||
		lp.Receipt.DownloadPath != "/api/v1/purchases/"+laptop.String()+"/receipt" {
		t.Errorf("receipt metadata: %+v", lp.Receipt)
	}
	if doc.Purchases[0].Receipt != nil {
		t.Error("a purchase without a receipt must export receipt: null")
	}
	if len(doc.Notifications) != 1 || doc.Notifications[0].Title != "Garansi segera berakhir" || !doc.Notifications[0].Read {
		t.Errorf("only delivered notifications belong in the export: %+v", doc.Notifications)
	}

	// Privacy: nothing that belongs to anyone else, and no internal identifiers.
	for _, forbidden := range []string{"BOBS SECRET ITEM", "xb-" + sfx, bob.String(), bobItem.String(),
		storageKey, "receipts/", aliceSub, "identity_subject", "idempotency", "exp-sent-", "future body", "cancelled body"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("export contains %q", forbidden)
		}
	}

	// A user with no data gets a valid, empty document.
	buf.Reset()
	if err := svc.Export(ctx, empty, &buf); err != nil {
		t.Fatal(err)
	}
	var emptyDoc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &emptyDoc); err != nil {
		t.Fatalf("empty export invalid: %v\n%s", err, buf.String())
	}
	if p, _ := emptyDoc["purchases"].([]any); p == nil || len(p) != 0 {
		t.Errorf("empty purchases should be [], got %v", emptyDoc["purchases"])
	}
	if emptyDoc["reminderPreferences"] != nil {
		t.Errorf("never-saved preferences should export as null, got %v", emptyDoc["reminderPreferences"])
	}

	// Unknown user: ErrNotFound and nothing written.
	buf.Reset()
	if err := svc.Export(ctx, uuid.New(), &buf); !errors.Is(err, account.ErrNotFound) || buf.Len() != 0 {
		t.Errorf("unknown user: err=%v written=%d", err, buf.Len())
	}

	// A failing client leaves an unterminated (invalid) document, never a plausible complete one.
	var partial bytes.Buffer
	fw := &failingWriter{left: 700}
	w := &teeWriter{dst: &partial, fw: fw}
	if err := svc.Export(ctx, alice, w); err == nil {
		t.Fatal("expected an error when the writer fails")
	}
	if json.Valid(partial.Bytes()) || strings.HasSuffix(strings.TrimSpace(partial.String()), "}") {
		t.Errorf("partial export must be invalid JSON: %q", partial.String())
	}
}

// teeWriter records what was accepted before the failing writer gave up.
type teeWriter struct {
	dst interface{ Write([]byte) (int, error) }
	fw  *failingWriter
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.fw.Write(p)
	if err == nil {
		t.dst.Write(p[:n])
	}
	return n, err
}
