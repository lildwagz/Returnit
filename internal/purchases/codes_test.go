package purchases

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Model and serial numbers: trimmed, empty means none, at most 100 characters on one line, and an update that does
// not mention them keeps the stored value (Set false).
func TestModelAndSerialNumbers(t *testing.T) {
	parse := func(body string) (Input, error) {
		var raw RawInput
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		return Validate(raw)
	}
	base := `"productName":"TV","purchaseDate":"2026-11-01"`

	in, err := parse(`{` + base + `,"modelNumber":"  UA43T6500  ","serialNumber":"0A1B2C"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !in.ModelNumber.Set || in.ModelNumber.Value == nil || *in.ModelNumber.Value != "UA43T6500" {
		t.Errorf("model = %+v, want trimmed and set", in.ModelNumber)
	}
	if in.SerialNumber.Value == nil || *in.SerialNumber.Value != "0A1B2C" {
		t.Errorf("serial = %+v", in.SerialNumber)
	}

	in, err = parse(`{` + base + `}`) // an older app: neither field
	if err != nil {
		t.Fatal(err)
	}
	if in.ModelNumber.Set || in.SerialNumber.Set {
		t.Errorf("absent fields must not be Set: %+v %+v", in.ModelNumber, in.SerialNumber)
	}

	in, err = parse(`{` + base + `,"modelNumber":"   ","serialNumber":null}`) // clearing both
	if err != nil {
		t.Fatal(err)
	}
	if !in.ModelNumber.Set || in.ModelNumber.Value != nil || !in.SerialNumber.Set || in.SerialNumber.Value != nil {
		t.Errorf("blank/null must clear: %+v %+v", in.ModelNumber, in.SerialNumber)
	}

	for _, body := range []string{
		`{` + base + `,"modelNumber":"` + strings.Repeat("x", 101) + `"}`,
		`{` + base + `,"serialNumber":"AB\nCD"}`,
	} {
		if _, err := parse(body); !errors.Is(err, ErrValidation) {
			t.Errorf("%.60s...: got %v, want a validation error", body, err)
		}
	}
}
