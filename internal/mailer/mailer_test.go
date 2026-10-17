package mailer

import (
	"errors"
	"net/textproto"
	"strings"
	"testing"
)

func TestValidAddress(t *testing.T) {
	good := []string{"a@example.com", "first.last+tag@sub.example.co.id"}
	bad := []string{"", "no-at-sign", "A <a@example.com>", "a@example.com\r\nBcc: x@y.z", "a@example.com,b@example.com",
		"a@example.com\n", "<a@example.com>", `"a b"@example.com`}
	for _, a := range good {
		if !ValidAddress(a) {
			t.Errorf("%q should be valid", a)
		}
	}
	for _, a := range bad {
		if ValidAddress(a) {
			t.Errorf("%q should be rejected", a)
		}
	}
}

func TestSendRejectsInjection(t *testing.T) {
	s := &SMTP{Addr: "127.0.0.1:1", From: "noreply@example.com", TLS: NoTLS}
	for _, m := range []Message{
		{To: "a@example.com\r\nBcc: evil@example.com", Subject: "s", Body: "b"},
		{To: "a@example.com", Subject: "s\r\nBcc: evil@example.com", Body: "b"},
	} {
		if err := s.Send(t.Context(), m); !errors.Is(err, ErrPermanent) {
			t.Errorf("want ErrPermanent before any network use, got %v", err)
		}
	}
}

func TestFormat(t *testing.T) {
	s := &SMTP{From: "noreply@example.com"}
	out := s.format(Message{To: "a@example.com", Subject: "Akun Anda dihapus — ReturnIt", Body: "line one\nline two\n"})
	for _, want := range []string{"From: noreply@example.com\r\n", "To: a@example.com\r\n", "MIME-Version: 1.0\r\n",
		"Content-Transfer-Encoding: quoted-printable\r\n", "Auto-Submitted: auto-generated\r\n", "Subject: =?utf-8?q?"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("non-ASCII subject must be encoded")
	}
}

func TestClassifyNeverLeaksAddress(t *testing.T) {
	perm := classify("recipient", &textproto.Error{Code: 550, Msg: "mailbox victim@example.com not found"})
	if !errors.Is(perm, ErrPermanent) || strings.Contains(perm.Error(), "victim") {
		t.Errorf("5xx: %v", perm)
	}
	temp := classify("recipient", &textproto.Error{Code: 451, Msg: "try later for victim@example.com"})
	if errors.Is(temp, ErrPermanent) || strings.Contains(temp.Error(), "victim") {
		t.Errorf("4xx: %v", temp)
	}
	// A wrong SMTP password (535) is our misconfiguration, not a bad recipient: keep retrying.
	if auth := classify("auth", &textproto.Error{Code: 535, Msg: "bad credentials"}); errors.Is(auth, ErrPermanent) {
		t.Error("auth failure must not be permanent")
	}
}
