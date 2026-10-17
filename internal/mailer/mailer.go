// Package mailer sends plain-text e-mail over SMTP.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// ErrPermanent means the server refused the message or recipient for good (SMTP 5xx); retrying
// will not help.
var ErrPermanent = errors.New("mailer: permanent failure")

type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends one message. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// TLS modes.
const (
	StartTLS    = "starttls" // upgrade the connection, refuse to continue if that fails (default)
	ImplicitTLS = "tls"      // TLS from the first byte (port 465)
	NoTLS       = "none"     // plaintext: local development only
)

type SMTP struct {
	Addr     string // host:port
	From     string // address only, e.g. noreply@example.com
	Username string
	Password string
	TLS      string
	Timeout  time.Duration // whole exchange; default 15s
}

// ValidAddress accepts a plain mailbox only: no display name, no line breaks.
func ValidAddress(a string) bool {
	if a == "" || len(a) > 254 || strings.ContainsAny(a, "\r\n<>,;\"") {
		return false
	}
	p, err := mail.ParseAddress(a)
	return err == nil && p.Address == a
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	if !ValidAddress(m.To) {
		return fmt.Errorf("%w: invalid recipient address", ErrPermanent)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("%w: invalid subject", ErrPermanent)
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("mailer: bad SMTP address: %w", err)
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("mailer: connect: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if s.TLS == ImplicitTLS {
		conn = tls.Client(conn, tlsCfg)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer: greeting: %w", err)
	}
	defer c.Close()

	if s.TLS == "" || s.TLS == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mailer: server does not offer STARTTLS (set SMTP_TLS=none only for local development)")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return classify("auth", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return classify("sender", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return classify("recipient", err)
	}
	w, err := c.Data()
	if err != nil {
		return classify("data", err)
	}
	if _, err := io.WriteString(w, s.format(m)); err != nil {
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return classify("deliver", err)
	}
	return c.Quit()
}

// classify turns 5xx replies into ErrPermanent. The error text never includes the address.
func classify(stage string, err error) error {
	if code := smtpCode(err); code >= 500 && code < 600 && stage != "auth" {
		return fmt.Errorf("%w: %s rejected (%d)", ErrPermanent, stage, code)
	}
	return fmt.Errorf("mailer: %s: %s", stage, scrubCode(err))
}

func (s *SMTP) format(m Message) string {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "localhost"
	if i := strings.LastIndex(s.From, "@"); i >= 0 {
		domain = s.From[i+1:]
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", s.From)
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `text/plain; charset="utf-8"`)
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n")))
	_ = qp.Close()
	return b.String()
}
