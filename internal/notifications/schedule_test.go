package notifications

import (
	"testing"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func sp(s string) *string { return &s }

func TestReminderTimeIs0900WIB(t *testing.T) {
	got := ReminderTime(date("2026-11-30"), 7, wib, nil, nil)
	want := time.Date(2026, 11, 23, 9, 0, 0, 0, wib)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Month/year boundaries and leap day.
	if got := ReminderTime(date("2027-01-03"), 14, wib, nil, nil); !got.Equal(time.Date(2026, 12, 20, 9, 0, 0, 0, wib)) {
		t.Fatalf("year boundary: %v", got)
	}
	if got := ReminderTime(date("2028-03-01"), 1, wib, nil, nil); !got.Equal(time.Date(2028, 2, 29, 9, 0, 0, 0, wib)) {
		t.Fatalf("leap day: %v", got)
	}
}

func TestAdjustForQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 23, h, m, 0, 0, wib) }
	cases := []struct {
		name   string
		t      time.Time
		qs, qe *string
		want   time.Time
	}{
		{"no quiet hours", at(9, 0), nil, nil, at(9, 0)},
		{"outside daytime window", at(9, 0), sp("13:00"), sp("14:00"), at(9, 0)},
		{"inside daytime window", at(13, 30), sp("13:00"), sp("14:00"), at(14, 0)},
		{"window end is exclusive", at(14, 0), sp("13:00"), sp("14:00"), at(14, 0)},
		{"overnight, early morning side", at(6, 0), sp("22:00"), sp("07:00"), at(7, 0)},
		{"overnight, late evening side", at(23, 0), sp("22:00"), sp("07:00"), time.Date(2026, 11, 24, 7, 0, 0, 0, wib)},
		{"overnight, outside", at(9, 0), sp("22:00"), sp("07:00"), at(9, 0)},
		{"overnight, start boundary", at(22, 0), sp("22:00"), sp("07:00"), time.Date(2026, 11, 24, 7, 0, 0, 0, wib)},
		{"reminder hour inside quiet window", at(9, 0), sp("08:00"), sp("10:30"), at(10, 30)},
	}
	for _, c := range cases {
		if got := AdjustForQuietHours(c.t, wib, c.qs, c.qe); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func cand(end string, prefs Prefs) Candidate {
	return Candidate{PurchaseID: "p1", UserID: "u1", ProductName: "Laptop", End: date(end), Prefs: prefs}
}

func TestPlanOnlyWithinWindowAndSkipsMissed(t *testing.T) {
	prefs := DefaultPrefs() // 30,14,7,3,1 @ WIB
	// Now = 2026-11-20 10:00 WIB. End = 2026-11-23 -> offsets: 3 => 11-20 09:00 (1h ago, catch-up),
	// 1 => 11-22 09:00 (within 48h), 7/14/30 => in the past beyond catch-up (skipped).
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, wib)
	got := Plan(cand("2026-11-23", prefs), now)
	if len(got) != 2 {
		t.Fatalf("expected offsets 3 and 1, got %+v", got)
	}
	offs := map[int]bool{}
	for _, p := range got {
		offs[p.OffsetDays] = true
	}
	if !offs[3] || !offs[1] {
		t.Fatalf("offsets: %v", offs)
	}
	// Far-future reminders are not planned yet.
	if got := Plan(cand("2027-06-01", prefs), now); len(got) != 0 {
		t.Fatalf("future planned too early: %+v", got)
	}
	// Disabled -> nothing.
	prefs.Enabled = false
	if got := Plan(cand("2026-11-23", prefs), now); len(got) != 0 {
		t.Fatalf("disabled prefs planned: %+v", got)
	}
}

func TestPlanKeyIsStableAndDistinct(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, wib)
	a := Plan(cand("2026-11-23", DefaultPrefs()), now)
	b := Plan(cand("2026-11-23", DefaultPrefs()), now.Add(time.Minute))
	if len(a) != len(b) {
		t.Fatal("plan not deterministic")
	}
	seen := map[string]bool{}
	for i := range a {
		if a[i].Key != b[i].Key {
			t.Fatalf("key changed between runs: %q vs %q", a[i].Key, b[i].Key)
		}
		if seen[a[i].Key] {
			t.Fatalf("duplicate key %q", a[i].Key)
		}
		seen[a[i].Key] = true
	}
	// The channel is how a reminder is delivered, not which reminder it is: switching push <-> in_app
	// must map to the SAME reminder (otherwise the inbox gets a duplicate and the read state is lost).
	other := DefaultPrefs()
	other.Channel = "in_app"
	c := Plan(cand("2026-11-23", other), now)
	if len(c) != len(a) {
		t.Fatalf("a channel change must not change which reminders are planned: %d vs %d", len(c), len(a))
	}
	for i := range a {
		if c[i].Key != a[i].Key {
			t.Fatalf("channel must not be part of the key: %q vs %q", c[i].Key, a[i].Key)
		}
	}
}

func TestDecide(t *testing.T) {
	end := date("2026-11-30")
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, wib)
	base := func() Due {
		cur := end
		p := DefaultPrefs()
		return Due{Channel: "push", OffsetDays: 7, PlannedEnd: end, CurrentEnd: &cur, Prefs: p}
	}
	if a, _, _ := Decide(base(), now); a != ActionSend {
		t.Fatalf("baseline should send, got %v", a)
	}
	mods := map[string]func(*Due){
		"purchase deleted":      func(d *Due) { d.CurrentEnd = nil },
		"end date changed":      func(d *Due) { e := date("2026-12-15"); d.CurrentEnd = &e },
		"prefs disabled":        func(d *Due) { d.Prefs.Enabled = false },
		"offset removed":        func(d *Due) { d.Prefs.DaysBefore = []int{30, 14} },
		"account pending":       func(d *Due) { d.AccountPending = true },
		"warranty already over": func(d *Due) { e := date("2026-11-20"); d.CurrentEnd, d.PlannedEnd = &e, e },
	}
	for name, mod := range mods {
		d := base()
		mod(&d)
		a, reason, _ := Decide(d, now)
		if a != ActionCancel || reason == "" {
			t.Errorf("%s: action=%v reason=%q", name, a, reason)
		}
	}
	// A channel change after planning is not a reason to cancel: the reminder is sent through the new channel.
	for _, ch := range []string{"in_app", "email", "push"} {
		d := base()
		d.Prefs.Channel = ch
		if a, _, _ := Decide(d, now); a != ActionSend {
			t.Errorf("channel changed to %s: want send, got %v", ch, a)
		}
	}
	// Inside quiet hours at dispatch time -> deferred to the end of the window.
	d := base()
	d.Prefs.QuietStart, d.Prefs.QuietEnd = sp("08:00"), sp("10:30")
	a, _, until := Decide(d, now)
	if a != ActionDefer || !until.Equal(time.Date(2026, 11, 23, 10, 30, 0, 0, wib)) {
		t.Fatalf("quiet hours: action=%v until=%v", a, until)
	}
	// "Today" is evaluated in the user's timezone, not UTC: at 02:00 WIB on 11-30 the UTC date is
	// still 11-29, but the warranty ends today in WIB, so the reminder is still valid.
	d = base()
	if a, _, _ := Decide(d, time.Date(2026, 11, 30, 2, 0, 0, 0, wib)); a != ActionSend {
		t.Fatalf("last day should still send, got %v", a)
	}
	if a, _, _ := Decide(d, time.Date(2026, 12, 1, 0, 30, 0, 0, wib)); a != ActionCancel {
		t.Fatalf("day after end should cancel, got %v", a)
	}
}

func TestPlanReturnDeadline(t *testing.T) {
	// Return deadline 2026-11-30: reminders 3 and 1 days before, at 09:00 WIB, kind "return", own keys.
	c := Candidate{PurchaseID: "p1", UserID: "u1", ProductName: "Headphone", ReturnEnd: date("2026-11-30"), Prefs: DefaultPrefs()}
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, wib) // the 3-day reminder was 23 h ago (still caught up), the 1-day one is 25 h ahead
	got := Plan(c, now)
	if len(got) != 2 {
		t.Fatalf("planned %d, want 2: %+v", len(got), got)
	}
	for _, p := range got {
		if p.Kind != KindReturn || !p.Date.Equal(date("2026-11-30")) {
			t.Fatalf("kind/date: %+v", p)
		}
		if p.Title != "Batas pengembalian segera berakhir" {
			t.Fatalf("title: %q", p.Title)
		}
	}
	if !got[0].ScheduledAt.Equal(time.Date(2026, 11, 27, 9, 0, 0, 0, wib)) {
		t.Fatalf("first at %v", got[0].ScheduledAt)
	}
	if got[0].Key != "return:p1:2026-11-27:3" || got[1].Key != "return:p1:2026-11-29:1" {
		t.Fatalf("keys: %q %q", got[0].Key, got[1].Key)
	}
}

func TestPlanReturnAndWarrantyDoNotClash(t *testing.T) {
	// Warranty ends on the same day as the return deadline: the 1-day reminder of each must be separate rows.
	c := Candidate{PurchaseID: "p1", UserID: "u1", ProductName: "X", End: date("2026-11-30"), ReturnEnd: date("2026-11-30"), Prefs: DefaultPrefs()}
	now := time.Date(2026, 11, 29, 6, 0, 0, 0, wib)
	keys := map[string]string{}
	for _, p := range Plan(c, now) {
		if prev, dup := keys[p.Key]; dup {
			t.Fatalf("key %q used by both %s and %s", p.Key, prev, p.Kind)
		}
		keys[p.Key] = p.Kind
	}
	if keys["p1:2026-11-29:1"] != KindWarranty || keys["return:p1:2026-11-29:1"] != KindReturn {
		t.Fatalf("keys: %v", keys)
	}
}

func TestPlanReturnOnlyPurchaseWithoutWarranty(t *testing.T) {
	// A purchase with no warranty end date but a return deadline still gets return reminders (and no warranty ones).
	c := Candidate{PurchaseID: "p1", UserID: "u1", ProductName: "X", ReturnEnd: date("2026-11-30"), Prefs: DefaultPrefs()}
	for _, p := range Plan(c, time.Date(2026, 11, 28, 6, 0, 0, 0, wib)) {
		if p.Kind != KindReturn {
			t.Fatalf("unexpected %s reminder", p.Kind)
		}
	}
	// reminders switched off: nothing, for either kind
	c.Prefs.Enabled = false
	if got := Plan(c, time.Date(2026, 11, 28, 6, 0, 0, 0, wib)); len(got) != 0 {
		t.Fatalf("disabled: %d planned", len(got))
	}
}

func TestDecideReturn(t *testing.T) {
	end := date("2026-11-30")
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, wib)
	base := func() Due {
		cur := end
		p := DefaultPrefs()
		p.DaysBefore = []int{30} // the warranty offsets must not matter for a return reminder
		return Due{Kind: KindReturn, Channel: "push", OffsetDays: 3, PlannedEnd: end, CurrentEnd: &cur, Prefs: p}
	}
	if a, _, _ := Decide(base(), now); a != ActionSend {
		t.Fatalf("a return reminder must not depend on the warranty offsets, got %v", a)
	}
	mods := map[string]func(*Due){
		"deadline removed":         func(d *Due) { d.CurrentEnd = nil },
		"deadline changed":         func(d *Due) { e := date("2026-12-10"); d.CurrentEnd = &e },
		"reminders disabled":       func(d *Due) { d.Prefs.Enabled = false },
		"offset no longer used":    func(d *Due) { d.OffsetDays = 5 },
		"deadline already passed":  func(d *Due) { e := date("2026-11-20"); d.CurrentEnd, d.PlannedEnd = &e, e },
		"account pending deletion": func(d *Due) { d.AccountPending = true },
	}
	for name, mod := range mods {
		d := base()
		mod(&d)
		if a, reason, _ := Decide(d, now); a != ActionCancel || reason == "" {
			t.Errorf("%s: action=%v reason=%q", name, a, reason)
		}
	}
	// a warranty reminder (kind empty, as for old rows) still follows the warranty offsets
	d := base()
	d.Kind = ""
	d.OffsetDays = 7
	if a, _, _ := Decide(d, now); a != ActionCancel {
		t.Fatalf("warranty reminder with a removed offset must cancel, got %v", a)
	}
}
