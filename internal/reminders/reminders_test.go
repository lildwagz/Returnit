package reminders

import (
	"errors"
	"slices"
	"testing"
)

func p[T any](v T) *T { return &v }

func valid() Input {
	return Input{Enabled: p(true), DaysBefore: p([]int{7, 30}), Channel: p("push")}
}

func quiet(start, end, tz string) func(*Input) {
	return func(i *Input) { i.QuietHoursStart, i.QuietHoursEnd, i.Timezone = p(start), p(end), p(tz) }
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Input)
		ok   bool
	}{
		{"baseline", func(*Input) {}, true},
		{"all offsets", func(i *Input) { i.DaysBefore = p([]int{1, 3, 7, 14, 30}) }, true},
		{"zero offset", func(i *Input) { i.DaysBefore = p([]int{0}) }, false},
		{"negative", func(i *Input) { i.DaysBefore = p([]int{-7}) }, false},
		{"unlisted 10", func(i *Input) { i.DaysBefore = p([]int{10}) }, false},
		{"duplicate", func(i *Input) { i.DaysBefore = p([]int{3, 3}) }, false},
		{"empty enabled", func(i *Input) { i.DaysBefore = p([]int{}) }, false},
		{"empty disabled", func(i *Input) { i.DaysBefore = p([]int{}); i.Enabled = p(false) }, true},
		{"missing enabled", func(i *Input) { i.Enabled = nil }, false},
		{"missing days", func(i *Input) { i.DaysBefore = nil }, false},
		{"bad channel", func(i *Input) { i.Channel = p("sms") }, false},
		{"quiet ok overnight", quiet("22:00", "07:00", "Asia/Jakarta"), true},
		{"quiet only start", func(i *Input) { i.QuietHoursStart, i.Timezone = p("22:00"), p("UTC") }, false},
		{"quiet bad clock", quiet("25:00", "07:00", "UTC"), false},
		{"quiet unpadded", quiet("9:00", "17:00", "UTC"), false},
		{"quiet equal", quiet("08:00", "08:00", "UTC"), false},
		{"quiet no tz defaults to WIB", func(i *Input) { i.QuietHoursStart, i.QuietHoursEnd = p("22:00"), p("07:00") }, true},
		{"bad tz", func(i *Input) { i.Timezone = p("Mars/Olympus") }, false},
		{"tz Local rejected", func(i *Input) { i.Timezone = p("Local") }, false},
		{"tz without quiet ok", func(i *Input) { i.Timezone = p("Asia/Jakarta") }, true},
		{"blank quiet strings = unset", func(i *Input) { i.QuietHoursStart, i.QuietHoursEnd = p(""), p("") }, true},
	}
	for _, c := range cases {
		in := valid()
		c.mod(&in)
		_, err := Validate(in)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("%s: not ErrValidation", c.name)
		}
	}
}

func TestValidateSortsAndDoesNotMutateInput(t *testing.T) {
	in := valid()
	in.DaysBefore = p([]int{1, 30, 7})
	out, err := Validate(in)
	if err != nil || !slices.Equal(out.DaysBefore, []int{30, 7, 1}) {
		t.Fatalf("%v %v", out.DaysBefore, err)
	}
	if !slices.Equal(*in.DaysBefore, []int{1, 30, 7}) {
		t.Fatal("input slice was mutated")
	}
}

func TestTimezoneDefaultsToWIB(t *testing.T) {
	out, err := Validate(valid())
	if err != nil || out.Timezone != "Asia/Jakarta" {
		t.Fatalf("tz=%q err=%v", out.Timezone, err)
	}
	in := valid()
	in.Timezone = p("")
	if out, _ := Validate(in); out.Timezone != "Asia/Jakarta" {
		t.Fatalf("blank tz should default, got %q", out.Timezone)
	}
	in.Timezone = p("Asia/Makassar") // WITA is allowed
	if out, err := Validate(in); err != nil || out.Timezone != "Asia/Makassar" {
		t.Fatalf("WITA: %q %v", out.Timezone, err)
	}
	if d := Default(); d.Timezone != "Asia/Jakarta" {
		t.Fatalf("Default tz = %q", d.Timezone)
	}
}
