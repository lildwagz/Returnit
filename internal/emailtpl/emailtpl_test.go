package emailtpl

import (
	"strings"
	"testing"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

func TestReminderInThreeLanguages(t *testing.T) {
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC) // a Monday
	cases := []struct {
		lang, title, label, pill, dateText, dir string
	}{
		{"id", "Garansi segera berakhir", "Garansi berakhir", "7 hari lagi", "Senin, 30 November 2026", "ltr"},
		{"en", "Warranty ending soon", "Warranty ends", "in 7 days", "Monday, 30 November 2026", "ltr"},
		{"ar", "اقتراب انتهاء الضمان", "ينتهي الضمان", "بعد 7 أيام", "الاثنين، 30 نوفمبر 2026", "rtl"},
	}
	for _, c := range cases {
		m := Reminder(c.lang, false, c.title, "body", "Laptop", end, 7, "https://dl.example.com/assets/email-logo.png", "https://dl.example.com/assets/email-logo-dark.png")
		if m.Subject != c.title {
			t.Errorf("%s subject %q", c.lang, m.Subject)
		}
		for _, want := range []string{c.label, c.pill, c.dateText, `dir="` + c.dir + `"`, "Laptop", "email-logo-dark.png"} {
			if !strings.Contains(m.HTML, want) {
				t.Errorf("%s: html lacks %q", c.lang, want)
			}
		}
		for _, want := range []string{c.title, c.label + ": Laptop", c.dateText} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("%s: text lacks %q:\n%s", c.lang, want, m.Text)
			}
		}
	}
}

func TestReminderEscapesTheProductName(t *testing.T) {
	m := Reminder("en", false, "Warranty ending soon", "b", `<script>alert(1)</script> & "x"`, time.Now(), 3, "", "")
	if strings.Contains(m.HTML, "<script>") {
		t.Fatal("product name was not escaped")
	}
	if !strings.Contains(m.HTML, "&lt;script&gt;") {
		t.Fatal("escaped form missing")
	}
	if !strings.Contains(m.Text, "<script>") { // plain text is not HTML: stays as typed
		t.Fatal("text version should keep the name as written")
	}
}

func TestReminderToneAndFallbacks(t *testing.T) {
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	soon := Reminder("id", false, "t", "b", "P", end, 7, "", "")
	later := Reminder("id", false, "t", "b", "P", end, 30, "", "")
	if !strings.Contains(soon.HTML, "c-warnbg") || strings.Contains(soon.HTML, `class="c-okbg`) {
		t.Error("7 days or less is amber")
	}
	if !strings.Contains(later.HTML, "c-okbg") {
		t.Error("more than 7 days is blue")
	}
	if strings.Contains(soon.HTML, "<img") || !strings.Contains(soon.HTML, ">ReturnIt<") {
		t.Error("without a logo address the name is drawn as text")
	}
	ret := Reminder("en", true, "t", "b", "Shoes", end, 3, "", "")
	if !strings.Contains(ret.HTML, "Return deadline") || !strings.Contains(ret.HTML, "Still want to return it?") {
		t.Error("return reminder wording")
	}
	if unknown := Reminder("fr", false, "t", "b", "P", end, 1, "", ""); !strings.Contains(unknown.HTML, "besok") {
		t.Error("unknown language falls back to Indonesian")
	}
}

func TestAccountNotices(t *testing.T) {
	at := time.Date(2026, 12, 24, 10, 0, 0, 0, wib)
	req := Account("en", "requested", at, "", "")
	if req.Subject != "Your ReturnIt account will be deleted" || !strings.Contains(req.HTML, "24 December 2026, 10:00 WIB") ||
		!strings.Contains(req.Text, "Permanently deleted on: 24 December 2026, 10:00 WIB") {
		t.Errorf("requested: %q\n%s", req.Subject, req.Text)
	}
	if !strings.Contains(req.Text, "cancel the deletion") {
		t.Error("the way out must be in the plain text too")
	}
	rem := Account("id", "reminder", at, "", "")
	if rem.Subject != "Akun ReturnIt Anda akan dihapus besok" || !strings.Contains(rem.HTML, "24 Desember 2026, 10:00 WIB") {
		t.Errorf("reminder: %q", rem.Subject)
	}
	er := Account("ar", "erased", at, "", "")
	if !strings.Contains(er.HTML, `dir="rtl"`) || strings.Contains(er.HTML, "2026") {
		t.Error("erased notice has no date and is right to left")
	}
	if strings.Contains(Account("id", "erased", at, "", "").Text, "Dihapus permanen pada") {
		t.Error("erased notice has no countdown box")
	}
}

func TestAssetURLs(t *testing.T) {
	l, d := AssetURLs(" https://dl.example.com/assets/ ")
	if l != "https://dl.example.com/assets/email-logo.png" || d != "https://dl.example.com/assets/email-logo-dark.png" {
		t.Errorf("%q %q", l, d)
	}
	if l, d := AssetURLs(""); l != "" || d != "" {
		t.Error("empty base means no logo")
	}
}
