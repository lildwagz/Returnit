package notifications

import (
	"strings"
	"testing"
	"time"
)

func TestTextsByLanguage(t *testing.T) {
	end := date("2026-11-30")
	cases := []struct {
		lang, kind string
		days       int
		title      string
		body       string
	}{
		{"", KindWarranty, 7, "Garansi segera berakhir", "Garansi Laptop berakhir dalam 7 hari (2026-11-30)."},
		{"id", KindWarranty, 1, "Garansi segera berakhir", "Garansi Laptop berakhir besok (2026-11-30)."},
		{"en", KindWarranty, 7, "Warranty ending soon", "The warranty for Laptop ends in 7 days (2026-11-30)."},
		{"en", KindWarranty, 1, "Warranty ending soon", "The warranty for Laptop ends tomorrow (2026-11-30)."},
		{"en", KindReturn, 3, "Return deadline ending soon", "The return period for Laptop ends in 3 days (2026-11-30)."},
		{"fr", KindWarranty, 3, "Garansi segera berakhir", "Garansi Laptop berakhir dalam 3 hari (2026-11-30)."}, // unknown: Indonesian
	}
	for _, c := range cases {
		title, body := Texts(c.lang, c.kind, "Laptop", c.days, end)
		if title != c.title || body != c.body {
			t.Errorf("%q/%s/%d: got %q / %q", c.lang, c.kind, c.days, title, body)
		}
	}
	// Arabic: the product and the date are kept as written, the sentence is Arabic.
	title, body := Texts("ar", KindWarranty, "Laptop", 1, end)
	if title == "" || !strings.Contains(body, "Laptop") || !strings.Contains(body, "2026-11-30") || !strings.Contains(body, "غدًا") {
		t.Errorf("arabic: %q / %q", title, body)
	}
}

func TestPlanWritesInTheOwnersLanguage(t *testing.T) {
	c := Candidate{PurchaseID: "p1", UserID: "u1", ProductName: "Laptop", End: date("2026-11-30"), Language: "en", Prefs: DefaultPrefs()}
	got := Plan(c, time.Date(2026, 11, 28, 8, 0, 0, 0, wib))
	if len(got) == 0 {
		t.Fatal("nothing planned")
	}
	for _, p := range got {
		if p.Title != "Warranty ending soon" || !strings.HasPrefix(p.Body, "The warranty for Laptop ends") {
			t.Fatalf("not English: %q / %q", p.Title, p.Body)
		}
	}
}
