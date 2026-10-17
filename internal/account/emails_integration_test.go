//go:build integration

package account_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/account"
	"github.com/returnit/returnit/internal/mailer"
	"github.com/returnit/returnit/internal/users"
)

type mpMessage struct {
	ID      string
	Subject string
	To      []struct{ Address string }
}

// mailpit returns the messages Mailpit (a real SMTP server) received for one address.
func mailpit(t *testing.T, api, to string) []mpMessage {
	t.Helper()
	resp, err := http.Get(api + "/api/v1/search?query=" + "to:" + to)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Messages []mpMessage }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Messages
}

func mailpitBody(t *testing.T, api, id string) string {
	t.Helper()
	resp, err := http.Get(api + "/api/v1/message/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Text string }
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Text
}

// Needs migrated Postgres and Mailpit (TEST_SMTP_ADDR=localhost:1025, TEST_MAILPIT_API=http://localhost:8025).
func TestDeletionEmails(t *testing.T) {
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	us := users.NewStore(pool)
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	mk := func(name string) (uuid.UUID, string) {
		addr := name + "-" + sfx + "@example.com"
		id, err := us.EnsureBySubject(ctx, name+"-"+sfx, addr, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
			pool.Exec(context.Background(), `DELETE FROM account_deletion_log WHERE user_id = $1`, id)
			pool.Exec(context.Background(), `DELETE FROM account_emails WHERE user_id = $1`, id)
		})
		return id, addr
	}
	alice, aliceMail := mk("alice")
	bob, bobMail := mk("bob")
	carol, carolMail := mk("carol")

	clock := time.Now().UTC()
	svc := account.NewService(pool, 7*24*time.Hour)
	svc.Now = func() time.Time { return clock }
	smtp := &mailer.SMTP{Addr: smtpAddr, From: "noreply@returnit.local", TLS: mailer.NoTLS}
	sender := &account.EmailSender{Pool: pool, Mailer: smtp, Log: log, Now: func() time.Time { return clock.Add(time.Second) }}
	rows := func(id uuid.UUID) int {
		return count(t, pool, `SELECT count(*) FROM account_emails WHERE user_id = $1`, id)
	}

	// 1. Requesting deletion queues the notice and the 24h reminder; the notice is delivered.
	at, err := svc.RequestDeletion(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if rows(alice) != 2 {
		t.Fatalf("want requested+reminder queued, got %d", rows(alice))
	}
	if _, err := sender.SendDue(ctx); err != nil {
		t.Fatal(err)
	}
	got := mailpit(t, api, aliceMail)
	if len(got) != 1 || got[0].Subject != "Your ReturnIt account will be deleted" {
		t.Fatalf("after request: %+v", got)
	}
	if body := mailpitBody(t, api, got[0].ID); !strings.Contains(body, "WIB") || !strings.Contains(body, at.In(wib).Format("2 January 2006")) {
		t.Errorf("body should state the erasure date in WIB:\n%s", body)
	}
	if rows(alice) != 1 {
		t.Errorf("sent row must be deleted (address no longer kept), rows=%d", rows(alice))
	}

	// 2. Asking again must not send another notice, even though the first one is long gone.
	svc.RequestDeletion(ctx, alice)
	sender.SendDue(ctx)
	if n := len(mailpit(t, api, aliceMail)); n != 1 {
		t.Errorf("repeat request re-sent the notice: %d messages", n)
	}

	// 3. The reminder is held back until 24h before erasure.
	if n := len(mailpit(t, api, aliceMail)); n != 1 {
		t.Fatal("reminder sent too early")
	}
	late := &account.EmailSender{Pool: pool, Mailer: smtp, Log: log, Now: func() time.Time { return at.Add(-23 * time.Hour) }}
	if _, err := late.SendDue(ctx); err != nil {
		t.Fatal(err)
	}
	if got = mailpit(t, api, aliceMail); len(got) != 2 {
		t.Fatalf("reminder not delivered: %+v", got)
	}

	// 4. Cancelling drops notices that were not sent yet.
	if _, err := svc.RequestDeletion(ctx, bob); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelDeletion(ctx, bob); err != nil {
		t.Fatal(err)
	}
	if rows(bob) != 0 {
		t.Errorf("cancel left %d queued e-mails", rows(bob))
	}
	sender.SendDue(ctx)
	if n := len(mailpit(t, api, bobMail)); n != 0 {
		t.Errorf("cancelled request still e-mailed: %d", n)
	}
	// A notice already queued for a schedule that no longer exists is dropped, not sent.
	pool.Exec(ctx, `INSERT INTO account_emails (user_id, kind, to_address, erasure_at, next_attempt_at) VALUES ($1,'requested',$2,$3,$4)`,
		bob, bobMail, clock.Add(time.Hour), clock)
	sender.SendDue(ctx)
	if n := len(mailpit(t, api, bobMail)); n != 0 || rows(bob) != 0 {
		t.Errorf("stale notice: mails=%d rows=%d", n, rows(bob))
	}

	// 5. After erasure the user row is gone, yet the confirmation still reaches the old address,
	// and no address remains in the database afterwards.
	clock = at.Add(time.Minute)
	if n, err := svc.PurgeDue(ctx); err != nil || n < 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, alice) != 0 {
		t.Fatal("alice not erased")
	}
	if count(t, pool, `SELECT count(*) FROM account_emails WHERE user_id = $1 AND kind='erased'`, alice) != 1 {
		t.Fatal("erased notice not queued")
	}
	sender.Now = func() time.Time { return clock.Add(time.Minute) }
	sender.SendDue(ctx)
	got = mailpit(t, api, aliceMail)
	if len(got) != 3 || !hasSubject(got, "Your ReturnIt account has been erased") {
		t.Fatalf("erasure confirmation missing: %+v", got)
	}
	if rows(alice) != 0 {
		t.Error("e-mail address still stored after the confirmation was sent")
	}

	// 6. A mail server outage: the message stays queued with backoff, nothing leaks into the
	// error text, and it is delivered once the server is back.
	clock = time.Now().UTC()
	svc.Now = func() time.Time { return clock }
	if _, err := svc.RequestDeletion(ctx, carol); err != nil {
		t.Fatal(err)
	}
	down := &account.EmailSender{Pool: pool, Log: log, Now: func() time.Time { return clock.Add(time.Second) },
		Mailer: &mailer.SMTP{Addr: "127.0.0.1:1", From: "noreply@returnit.local", TLS: mailer.NoTLS}}
	down.SendDue(ctx)
	var attempts int
	var lastErr *string
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error, next_attempt_at FROM account_emails WHERE user_id=$1 AND kind='requested'`, carol).
		Scan(&attempts, &lastErr, &next); err != nil {
		t.Fatalf("message must stay queued during an outage: %v", err)
	}
	if attempts != 1 || lastErr == nil || strings.Contains(*lastErr, "carol") || !next.After(clock.Add(time.Minute)) {
		t.Errorf("outage bookkeeping: attempts=%d err=%v next=%v", attempts, lastErr, next)
	}
	back := &account.EmailSender{Pool: pool, Mailer: smtp, Log: log, Now: func() time.Time { return next.Add(time.Second) }}
	back.SendDue(ctx)
	if n := len(mailpit(t, api, carolMail)); n != 1 {
		t.Errorf("not delivered after recovery: %d", n)
	}
}

func hasSubject(ms []mpMessage, s string) bool {
	for _, m := range ms {
		if m.Subject == s {
			return true
		}
	}
	return false
}
