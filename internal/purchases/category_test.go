package purchases

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestOptStringTellsAbsentFromNull(t *testing.T) {
	cases := []struct {
		name, body string
		set        bool
		value      string // "" with set means nil
		isNil      bool
	}{
		{"absent", `{"productName":"A"}`, false, "", true},
		{"null", `{"category":null}`, true, "", true},
		{"value", `{"category":"phone"}`, true, "phone", false},
		{"empty string", `{"category":""}`, true, "", false},
	}
	for _, c := range cases {
		var raw RawInput
		if err := json.Unmarshal([]byte(c.body), &raw); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if raw.Category.Set != c.set {
			t.Errorf("%s: Set=%v want %v", c.name, raw.Category.Set, c.set)
		}
		if (raw.Category.Value == nil) != c.isNil || (!c.isNil && *raw.Category.Value != c.value) {
			t.Errorf("%s: Value=%v", c.name, raw.Category.Value)
		}
	}
	var raw RawInput
	if err := json.Unmarshal([]byte(`{"category":5}`), &raw); err == nil {
		t.Error("a number must not be accepted as a category")
	}
}

func TestValidateCategory(t *testing.T) {
	base := RawInput{ProductName: "A", PurchaseDate: "2024-01-01"}
	for _, key := range Categories {
		r := base
		r.Category = OptString{Set: true, Value: s(key)}
		in, err := Validate(r)
		if err != nil || in.Category.Value == nil || *in.Category.Value != key || !in.Category.Set {
			t.Errorf("%s: %+v %v", key, in.Category, err)
		}
	}
	for _, bad := range []string{"Phone", "phones", " phone", "none", "x"} {
		r := base
		r.Category = OptString{Set: true, Value: s(bad)}
		if _, err := Validate(r); !errors.Is(err, ErrValidation) {
			t.Errorf("%q must be rejected, got %v", bad, err)
		}
	}
	// Empty string clears; null clears; absent keeps (Set false).
	r := base
	r.Category = OptString{Set: true, Value: s("")}
	if in, err := Validate(r); err != nil || in.Category.Value != nil || !in.Category.Set {
		t.Errorf("empty: %+v %v", in.Category, err)
	}
	if in, err := Validate(base); err != nil || in.Category.Set {
		t.Errorf("absent: %+v %v", in.Category, err)
	}
}

func TestListRejectsUnknownCategory(t *testing.T) {
	svc := NewService(nil, nil)
	if _, _, err := svc.List(nil, [16]byte{}, ListFilter{Category: "bogus"}); !errors.Is(err, ErrValidation) { //nolint:staticcheck
		t.Errorf("got %v", err)
	}
}
