package account

import (
	"strings"
	"testing"
	"time"
)

func TestComposeUsesUserTimezone(t *testing.T) {
	at := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC) // 10:00 in WIB
	_, body := Compose(KindRequested, at, "Asia/Jakarta")
	if !strings.Contains(body, "27 November 2026, 10:00 WIB") {
		t.Errorf("date not shown in WIB:\n%s", body)
	}
	_, body = Compose(KindReminder, at, "no/such-zone") // bad zone falls back to WIB, never fails
	if !strings.Contains(body, "10:00 WIB") {
		t.Errorf("fallback timezone:\n%s", body)
	}
	for _, k := range []string{KindRequested, KindReminder, KindErased} {
		sub, body := Compose(k, at, "Asia/Jakarta")
		if sub == "" || body == "" || strings.ContainsAny(sub, "\r\n") {
			t.Errorf("%s: bad subject/body", k)
		}
	}
	// The "erased" notice must not promise a date or contain a link.
	_, erased := Compose(KindErased, at, "Asia/Jakarta")
	if strings.Contains(erased, "http") || strings.Contains(erased, "2026") {
		t.Errorf("erased notice:\n%s", erased)
	}
}
