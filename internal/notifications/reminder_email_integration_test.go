//go:build integration

package notifications_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/mailer"
	"github.com/returnit/returnit/internal/notifications"
	"github.com/returnit/returnit/internal/users"
)

// A reminder sent by e-mail is written in the person's language, carries an HTML version with the product, and
// escapes what the person typed. Needs Mailpit (TEST_SMTP_ADDR, TEST_MAILPIT_API).
func TestReminderEmail(t *testing.T) {
	dbURL, smtpAddr, api := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_SMTP_ADDR"), os.Getenv("TEST_MAILPIT_API")
	if dbURL == "" || smtpAddr == "" || api == "" {
		t.Skip("integration env not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	addr := "rem-" + sfx + "@example.com"
	uid, err := users.NewStore(pool).EnsureBySubject(ctx, "rem-"+sfx, addr, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid) })
	if _, err := pool.Exec(ctx, `UPDATE users SET language = 'en' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	var pid, nid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO purchases (user_id, product_name, purchase_date, warranty_end_date)
		VALUES ($1,'<b>Laptop</b>','2026-01-01','2026-12-01') RETURNING id`, uid).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO notifications (user_id, purchase_id, title, body, status, idempotency_key, channel, offset_days, warranty_end_date, kind, scheduled_at)
		VALUES ($1,$2,'Warranty ending soon','The warranty for <b>Laptop</b> ends in 7 days (2026-12-01).','scheduled',$3,'email',7,'2026-12-01','warranty', now()) RETURNING id`,
		uid, pid, "rem-"+sfx).Scan(&nid); err != nil {
		t.Fatal(err)
	}

	d := &notifications.Delivery{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AssetsURL: "https://dl.example.com/assets",
		Mailer: &mailer.SMTP{Addr: smtpAddr, From: "noreply@returnit.local", TLS: mailer.NoTLS}}
	if err := d.Send(ctx, uid, "email", notifications.Message{ID: nid, Title: "Warranty ending soon", Body: "The warranty for <b>Laptop</b> ends in 7 days (2026-12-01)."}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(api + "/api/v1/search?query=to:" + addr)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Messages []struct{ ID, Subject string }
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Messages) != 1 || list.Messages[0].Subject != "Warranty ending soon" {
		t.Fatalf("messages: %+v", list.Messages)
	}
	resp, err = http.Get(api + "/api/v1/message/" + list.Messages[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var msg struct{ Text, HTML string }
	json.NewDecoder(resp.Body).Decode(&msg)
	resp.Body.Close()
	for _, want := range []string{`lang="en"`, "Warranty ends", "in 7 days", "Tuesday, 1 December 2026", "email-logo-dark.png", "&lt;b&gt;Laptop&lt;/b&gt;"} {
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(msg.HTML, "<b>Laptop</b>") {
		t.Error("the product name was not escaped")
	}
	if !strings.Contains(msg.Text, "Warranty ends: <b>Laptop</b>") || strings.Contains(msg.Text, "automatic reminder from ReturnIt. You can") {
		t.Errorf("text version:\n%s", msg.Text)
	}
}
