//go:build integration

package receipts_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/malware"
	"github.com/returnit/returnit/internal/purchases"
	"github.com/returnit/returnit/internal/receipts"
	"github.com/returnit/returnit/internal/users"
)

// Documents of several kinds, and model/serial numbers kept across edits from older apps.
// Run with: TEST_DATABASE_URL=... go test -tags integration ./internal/receipts (database migrated to 19).
func TestDocumentKindsAndCodes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	store := &memStore{objects: map[string][]byte{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rsvc := receipts.NewService(receipts.NewPGRepo(pool), store, malware.Disabled{}, nil, 1<<20, log)
	psvc := purchases.NewService(purchases.NewPGRepo(pool), rsvc)

	sfx := uuid.NewString()
	alice, _ := users.NewStore(pool).EnsureBySubject(ctx, "dk-"+sfx, "dk-"+sfx+"@example.com", "")
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice) })

	pdf := func(tag string) []byte { return append([]byte("%PDF-1.7\n"), []byte(tag)...) }
	add := func(p uuid.UUID, kind, name string) (receipts.Receipt, error) {
		b := pdf(name)
		return rsvc.Add(ctx, alice, p, kind, name, int64(len(b)), bytes.NewReader(b))
	}

	model, serial := "UA43T6500", "0A1B2C3D"
	p, err := psvc.Create(ctx, alice, purchases.RawInput{ProductName: "TV", PurchaseDate: "2026-03-01",
		ModelNumber: purchases.OptString{Set: true, Value: &model}, SerialNumber: purchases.OptString{Set: true, Value: &serial}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ModelNumber == nil || *p.ModelNumber != model || p.SerialNumber == nil || *p.SerialNumber != serial {
		t.Fatalf("created codes: %v %v", p.ModelNumber, p.SerialNumber)
	}

	// An older app edits the purchase without the new fields: they stay.
	p, err = psvc.Update(ctx, alice, p.ID, purchases.RawInput{ProductName: "TV 43", PurchaseDate: "2026-03-01"})
	if err != nil || p.ModelNumber == nil || *p.ModelNumber != model || p.SerialNumber == nil {
		t.Fatalf("update without codes: %v %v %v", p.ModelNumber, p.SerialNumber, err)
	}
	// Searching finds a serial number.
	items, total, err := psvc.List(ctx, alice, purchases.ListFilter{Query: "1b2c"})
	if err != nil || total != 1 || items[0].ID != p.ID {
		t.Fatalf("search by serial: total=%d err=%v", total, err)
	}

	// A warranty card first, then a receipt: the list keeps both with their kinds, and "receipt" (the original API)
	// is the receipt, not the older warranty card.
	card, err := add(p.ID, "warranty_card", "kartu.pdf")
	if err != nil || card.Kind != "warranty_card" {
		t.Fatalf("add card: %+v %v", card, err)
	}
	time.Sleep(5 * time.Millisecond)
	rcpt, err := add(p.ID, "", "nota.pdf") // no kind means a receipt
	if err != nil || rcpt.Kind != receipts.KindReceipt {
		t.Fatalf("add receipt: %+v %v", rcpt, err)
	}
	got, _ := psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != 2 || got.Receipts[0].Kind != "warranty_card" || got.Receipts[1].Kind != "receipt" {
		t.Fatalf("kinds: %+v", got.Receipts)
	}
	if got.Receipt == nil || got.Receipt.ID != rcpt.ID {
		t.Fatalf("receipt field = %+v, want the receipt", got.Receipt)
	}
	if _, _, rec, err := rsvc.SignedURL(ctx, alice, p.ID); err != nil || rec.ID != rcpt.ID {
		t.Fatalf("original download picked %v (%v), want the receipt", rec.ID, err)
	}
	// The original upload replaces the receipt and never touches the warranty card.
	b := pdf("nota-baru.pdf")
	if _, err := rsvc.Upload(ctx, alice, p.ID, "nota-baru.pdf", int64(len(b)), bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	got, _ = psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != 2 || got.Receipts[0].ID != card.ID || got.Receipts[1].FileName != "nota-baru.pdf" {
		t.Fatalf("after original upload: %+v", got.Receipts)
	}
	// The original delete removes the receipt, not the card.
	if err := rsvc.Delete(ctx, alice, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = psvc.Get(ctx, alice, p.ID)
	if len(got.Receipts) != 1 || got.Receipts[0].ID != card.ID || got.Receipt != nil {
		t.Fatalf("after original delete: %+v receipt=%v", got.Receipts, got.Receipt)
	}

	// An unknown kind is refused before anything is stored.
	before := len(store.objects)
	if _, err := add(p.ID, "photo", "x.pdf"); !errors.Is(err, receipts.ErrKind) {
		t.Errorf("unknown kind: %v", err)
	}
	if len(store.objects) != before {
		t.Errorf("a refused file was stored")
	}
}
