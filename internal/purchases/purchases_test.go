package purchases

import (
	"errors"
	"strings"
	"testing"
)

func s(v string) *string { return &v }

func TestValidate(t *testing.T) {
	neg := -1.0
	cases := []struct {
		name string
		in   RawInput
		ok   bool
	}{
		{"minimal", RawInput{ProductName: "A", PurchaseDate: "2024-02-29"}, true},
		{"blank name", RawInput{ProductName: "   ", PurchaseDate: "2024-01-01"}, false},
		{"name 161", RawInput{ProductName: strings.Repeat("a", 161), PurchaseDate: "2024-01-01"}, false},
		{"name 160", RawInput{ProductName: strings.Repeat("a", 160), PurchaseDate: "2024-01-01"}, true},
		{"invalid leap day", RawInput{ProductName: "A", PurchaseDate: "2023-02-29"}, false},
		{"negative price", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", Price: &neg}, false},
		{"lowercase currency ok", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", Currency: "usd"}, true},
		{"digit currency", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", Currency: "U5D"}, false},
		{"end before start", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", WarrantyStartDate: s("2024-03-01"), WarrantyEndDate: s("2024-02-01")}, false},
		{"end equals start", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", WarrantyStartDate: s("2024-03-01"), WarrantyEndDate: s("2024-03-01")}, true},
		{"bad warranty date", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", WarrantyEndDate: s("nope")}, false},
		{"return deadline after purchase", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", ReturnDeadline: s("2024-01-15")}, true},
		{"return deadline same day", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", ReturnDeadline: s("2024-01-01")}, true},
		{"return deadline before purchase", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", ReturnDeadline: s("2023-12-31")}, false},
		{"bad return deadline", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", ReturnDeadline: s("soon")}, false},
		{"empty return deadline means none", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", ReturnDeadline: s("")}, true},
		{"notes too long", RawInput{ProductName: "A", PurchaseDate: "2024-01-01", Notes: s(strings.Repeat("n", 2001))}, false},
	}
	for _, c := range cases {
		in, err := Validate(c.in)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("%s: error does not wrap ErrValidation", c.name)
		}
		if err == nil && c.name == "minimal" && in.Currency != "IDR" {
			t.Errorf("default currency = %q", in.Currency)
		}
		if err == nil && c.name == "return deadline after purchase" && (in.ReturnDeadline == nil || in.ReturnDeadline.Format("2006-01-02") != "2024-01-15") {
			t.Errorf("return deadline = %v", in.ReturnDeadline)
		}
		if err == nil && c.name == "empty return deadline means none" && in.ReturnDeadline != nil {
			t.Errorf("empty return deadline must be nil, got %v", in.ReturnDeadline)
		}
	}
}
