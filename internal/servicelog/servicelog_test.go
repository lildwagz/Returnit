package servicelog

import (
	"errors"
	"strings"
	"testing"
)

func sp(s string) *string   { return &s }
func fp(f float64) *float64 { return &f }

func TestValidate(t *testing.T) {
	ok := []RawInput{
		{ServiceDate: "2026-11-15"},
		{ServiceDate: "2026-11-15", Place: sp("  Samsung Service Center  "), Problem: sp("Layar mati"), Result: sp("Ganti LCD"), Cost: fp(0)},
		{ServiceDate: "2026-11-15", Place: sp(strings.Repeat("a", 160)), Problem: sp(strings.Repeat("b", 1000))},
	}
	for i, r := range ok {
		if _, err := Validate(r); err != nil {
			t.Errorf("ok case %d: %v", i, err)
		}
	}
	bad := []RawInput{
		{},
		{ServiceDate: "15-11-2026"},
		{ServiceDate: "2026-11-15", Place: sp(strings.Repeat("a", 161))},
		{ServiceDate: "2026-11-15", Problem: sp(strings.Repeat("b", 1001))},
		{ServiceDate: "2026-11-15", Result: sp(strings.Repeat("c", 1001))},
		{ServiceDate: "2026-11-15", Cost: fp(-1)},
		{ServiceDate: "2026-11-15", Cost: fp(1e10)},
	}
	for i, r := range bad {
		if _, err := Validate(r); !errors.Is(err, ErrValidation) {
			t.Errorf("bad case %d: got %v, want a validation error", i, err)
		}
	}
}

func TestValidateTrimsAndDropsEmpty(t *testing.T) {
	in, err := Validate(RawInput{ServiceDate: "2026-11-15", Place: sp("  Toko A "), Problem: sp("   "), Result: sp("")})
	if err != nil {
		t.Fatal(err)
	}
	if in.Place == nil || *in.Place != "Toko A" {
		t.Errorf("place = %v, want trimmed", in.Place)
	}
	if in.Problem != nil || in.Result != nil {
		t.Errorf("blank texts must become null: problem=%v result=%v", in.Problem, in.Result)
	}
}
