// Package notifications lists a user's notifications and runs the reminder scheduler.
package notifications

import (
	"fmt"
	"slices"
	"time"
)

const (
	// ReminderHour is the local hour (in the user's timezone) at which reminders are sent.
	ReminderHour = 9
	// PlanAhead is how far ahead notifications are created. Planning only the near future keeps
	// the table small and means edits made earlier than this need no cancellation.
	PlanAhead = 48 * time.Hour
	// CatchUp is how far back a missed reminder is still planned (e.g. after scheduler downtime
	// or a newly added purchase). Older ones are skipped rather than sent late.
	CatchUp = 24 * time.Hour
)

// Kinds of reminder.
const (
	KindWarranty = "warranty" // the end of the warranty
	KindReturn   = "return"   // the end of the return period
)

// ReturnOffsets are the days before the return deadline at which a reminder is sent. They are fixed (a return
// period is short), switched on and off together with the other reminders and sent through the same channel.
var ReturnOffsets = []int{3, 1}

// Prefs is the reminder configuration relevant to scheduling.
type Prefs struct {
	Enabled    bool
	DaysBefore []int
	Channel    string
	QuietStart *string // "HH:MM" or nil
	QuietEnd   *string
	Timezone   string
}

// DefaultPrefs applies to users who never saved preferences.
func DefaultPrefs() Prefs {
	return Prefs{Enabled: true, DaysBefore: []int{30, 14, 7, 3, 1}, Channel: "push", Timezone: "Asia/Jakarta"}
}

func loadLocation(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil && name != "" {
		return loc
	}
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*3600) // tzdata missing: WIB has no DST
	}
	return loc
}

// ReminderTime returns when the reminder for (warrantyEnd - offset days) should fire:
// ReminderHour local time, pushed to the end of the user's quiet hours if it falls inside them.
func ReminderTime(warrantyEnd time.Time, offset int, loc *time.Location, qs, qe *string) time.Time {
	d := warrantyEnd.AddDate(0, 0, -offset)
	t := time.Date(d.Year(), d.Month(), d.Day(), ReminderHour, 0, 0, 0, loc)
	return AdjustForQuietHours(t, loc, qs, qe)
}

// AdjustForQuietHours returns t unchanged unless it falls inside the quiet window, in which case
// it returns the moment the window ends. Windows may span midnight (start > end).
func AdjustForQuietHours(t time.Time, loc *time.Location, qs, qe *string) time.Time {
	if qs == nil || qe == nil {
		return t
	}
	sh, sm, ok1 := parseClock(*qs)
	eh, em, ok2 := parseClock(*qe)
	if !ok1 || !ok2 {
		return t
	}
	lt := t.In(loc)
	y, mo, d := lt.Date()
	mins, start, end := lt.Hour()*60+lt.Minute(), sh*60+sm, eh*60+em
	endToday := time.Date(y, mo, d, eh, em, 0, 0, loc)
	switch {
	case start < end:
		if mins >= start && mins < end {
			return endToday
		}
	case start > end: // overnight
		if mins >= start {
			return time.Date(y, mo, d+1, eh, em, 0, 0, loc)
		}
		if mins < end {
			return endToday
		}
	}
	return t
}

func parseClock(s string) (h, m int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// Candidate is a purchase with a warranty end date plus its owner's preferences.
type Candidate struct {
	PurchaseID  string
	UserID      string
	ProductName string
	End         time.Time // warranty end, date only; the zero value means the purchase has no warranty end date
	ReturnEnd   time.Time // return deadline, date only; the zero value means none
	Prefs       Prefs
}

// Planned is a notification the planner wants to exist.
type Planned struct {
	Kind        string
	Date        time.Time // the warranty end or return deadline it was planned for
	Key         string
	ScheduledAt time.Time
	Channel     string
	OffsetDays  int
	Title       string
	Body        string
}

// Plan returns the notifications to create for a candidate at time now. It is pure and
// deterministic; idempotency comes from Key (purchase + reminder date + offset). The channel is NOT part of
// the key: it is only how a reminder is delivered, so switching push <-> in_app must not make a second
// reminder (a duplicate inbox entry whose read state was lost). A reminder that has not been sent yet is
// delivered through whichever channel is current when it is due (see Dispatch).
func Plan(c Candidate, now time.Time) []Planned {
	if !c.Prefs.Enabled {
		return nil
	}
	loc := loadLocation(c.Prefs.Timezone)
	var out []Planned
	add := func(kind string, end time.Time, offset int, title, body string) {
		at := ReminderTime(end, offset, loc, c.Prefs.QuietStart, c.Prefs.QuietEnd)
		if at.Before(now.Add(-CatchUp)) || at.After(now.Add(PlanAhead)) {
			return
		}
		reminderDate := end.AddDate(0, 0, -offset).Format("2006-01-02")
		key := fmt.Sprintf("%s:%s:%d", c.PurchaseID, reminderDate, offset)
		if kind == KindReturn {
			key = "return:" + key // the same date must not collide with a warranty reminder
		}
		out = append(out, Planned{
			Kind:        kind,
			Date:        end,
			Key:         key,
			ScheduledAt: at.UTC(),
			Channel:     c.Prefs.Channel,
			OffsetDays:  offset,
			Title:       title,
			Body:        body,
		})
	}
	if !c.End.IsZero() {
		for _, d := range c.Prefs.DaysBefore {
			add(KindWarranty, c.End, d, "Garansi segera berakhir", reminderBody(c.ProductName, d, c.End))
		}
	}
	if !c.ReturnEnd.IsZero() {
		for _, d := range ReturnOffsets {
			add(KindReturn, c.ReturnEnd, d, "Batas pengembalian segera berakhir", returnBody(c.ProductName, d, c.ReturnEnd))
		}
	}
	return out
}

func returnBody(product string, days int, end time.Time) string {
	when := fmt.Sprintf("dalam %d hari", days)
	if days == 1 {
		when = "besok"
	}
	return fmt.Sprintf("Batas pengembalian %s berakhir %s (%s).", product, when, end.Format("2006-01-02"))
}

func reminderBody(product string, days int, end time.Time) string {
	when := fmt.Sprintf("dalam %d hari", days)
	if days == 1 {
		when = "besok"
	}
	return fmt.Sprintf("Garansi %s berakhir %s (%s).", product, when, end.Format("2006-01-02"))
}

// Action is the outcome of re-validating a due notification.
type Action int

const (
	ActionSend Action = iota
	ActionCancel
	ActionDefer
)

// Due is a scheduled notification joined with the current state of its purchase and prefs.
type Due struct {
	Kind       string // KindWarranty (also when empty) or KindReturn
	Channel    string
	OffsetDays int
	PlannedEnd time.Time  // the warranty end or return deadline when planned
	CurrentEnd *time.Time // that same date today; nil if the purchase is gone or the date was cleared
	// AccountPending is true when the owner has requested account deletion (grace period).
	AccountPending bool
	Prefs          Prefs
}

// Decide re-checks everything that may have changed since planning (the design notes require
// this before dispatch). reason explains a cancellation; until is set for ActionDefer.
func Decide(d Due, now time.Time) (a Action, reason string, until time.Time) {
	switch {
	case d.AccountPending:
		return ActionCancel, "account pending deletion", time.Time{}
	case d.CurrentEnd == nil:
		return ActionCancel, "purchase removed or date cleared", time.Time{}
	case !d.CurrentEnd.Equal(d.PlannedEnd):
		return ActionCancel, "date changed", time.Time{}
	case !d.Prefs.Enabled:
		return ActionCancel, "reminders disabled", time.Time{}
	case d.Kind == KindReturn && !slices.Contains(ReturnOffsets, d.OffsetDays):
		return ActionCancel, "return reminder offset no longer used", time.Time{}
	case d.Kind != KindReturn && !slices.Contains(d.Prefs.DaysBefore, d.OffsetDays):
		return ActionCancel, "reminder offset removed from preferences", time.Time{}
	}
	loc := loadLocation(d.Prefs.Timezone)
	ln := now.In(loc)
	today := time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, time.UTC)
	if today.After(*d.CurrentEnd) {
		return ActionCancel, "date already passed", time.Time{}
	}
	if adj := AdjustForQuietHours(now, loc, d.Prefs.QuietStart, d.Prefs.QuietEnd); adj.After(now) {
		return ActionDefer, "", adj.UTC()
	}
	return ActionSend, "", time.Time{}
}
