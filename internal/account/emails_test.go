package account

import (
	"testing"
	"time"
)

func TestZoneFallsBackToWIB(t *testing.T) {
	at := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC) // 10:00 in WIB
	if got := at.In(zone("Asia/Jakarta")).Format("15:04 MST"); got != "10:00 WIB" {
		t.Errorf("WIB: %s", got)
	}
	if got := at.In(zone("no/such-zone")).Format("15:04 MST"); got != "10:00 WIB" { // a bad zone never holds a notice back
		t.Errorf("fallback: %s", got)
	}
	if got := at.In(zone("Asia/Makassar")).Format("15:04 MST"); got != "11:00 WITA" {
		t.Errorf("WITA: %s", got)
	}
}
