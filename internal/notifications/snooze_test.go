package notifications

import (
	"errors"
	"testing"
	"time"
)

func TestSnoozeTarget(t *testing.T) {
	now := time.Date(2026, 11, 28, 14, 30, 0, 0, wib) // Saturday afternoon
	for _, c := range []struct {
		choice string
		want   time.Time
	}{
		{"tomorrow", time.Date(2026, 11, 29, 9, 0, 0, 0, wib)},
		{"in_3_days", time.Date(2026, 12, 1, 9, 0, 0, 0, wib)}, // crosses the month end
		{"next_week", time.Date(2026, 12, 5, 9, 0, 0, 0, wib)},
	} {
		got, err := SnoozeTarget(c.choice, now, wib, nil, nil)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s: got %v (%v), want %v", c.choice, got, err, c.want)
		}
	}
	// a request made at 23:50 local time still counts from the local day, not from UTC
	late := time.Date(2026, 11, 28, 23, 50, 0, 0, wib)
	if got, _ := SnoozeTarget("tomorrow", late, wib, nil, nil); !got.Equal(time.Date(2026, 11, 29, 9, 0, 0, 0, wib)) {
		t.Errorf("late evening: %v", got)
	}
	// quiet hours push it to the end of the window
	if got, _ := SnoozeTarget("tomorrow", now, wib, sp("08:00"), sp("10:30")); !got.Equal(time.Date(2026, 11, 29, 10, 30, 0, 0, wib)) {
		t.Errorf("quiet hours: %v", got)
	}
	// anything else is refused
	for _, bad := range []string{"", "today", "in_30_days", "TOMORROW"} {
		if _, err := SnoozeTarget(bad, now, wib, nil, nil); !errors.Is(err, ErrBadSnooze) {
			t.Errorf("%q: err=%v", bad, err)
		}
	}
}
