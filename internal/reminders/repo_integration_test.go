//go:build integration

package reminders_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/reminders"
	"github.com/returnit/returnit/internal/users"
)

func TestPGRepo(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sfx := uuid.NewString()
	us := users.NewStore(pool)
	uid, _ := us.EnsureBySubject(ctx, "rp-"+sfx, "rp-"+sfx+"@example.com", "")
	other, _ := us.EnsureBySubject(ctx, "rq-"+sfx, "rq-"+sfx+"@example.com", "")
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{uid, other} {
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})

	svc := reminders.NewService(reminders.NewPGRepo(pool))
	if got, err := svc.Get(ctx, uid); err != nil || !got.Enabled || !slices.Equal(got.DaysBefore, []int{30, 14, 7, 3, 1}) {
		t.Fatalf("defaults: %+v %v", got, err)
	}

	b := true
	days := []int{1, 14}
	ch, qs, qe, tz := "email", "22:30", "06:15", "Asia/Jakarta"
	saved, err := svc.Replace(ctx, uid, reminders.Input{Enabled: &b, DaysBefore: &days, Channel: &ch,
		QuietHoursStart: &qs, QuietHoursEnd: &qe, Timezone: &tz})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.DaysBefore, []int{14, 1}) || *saved.QuietHoursStart != "22:30" ||
		*saved.QuietHoursEnd != "06:15" || saved.Timezone != tz {
		t.Fatalf("round trip: %+v", saved)
	}

	// A second PUT replaces everything, including clearing quiet hours.
	off, d2, push := false, []int{}, "push"
	again, err := svc.Replace(ctx, uid, reminders.Input{Enabled: &off, DaysBefore: &d2, Channel: &push})
	if err != nil || again.Enabled || len(again.DaysBefore) != 0 || again.QuietHoursStart != nil || again.Timezone != "Asia/Jakarta" {
		t.Fatalf("replace: %+v %v", again, err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM reminder_preferences WHERE user_id = $1`, uid).Scan(&n)
	if n != 1 {
		t.Fatalf("expected a single row, got %d", n)
	}

	// Other users are unaffected.
	if got, _ := svc.Get(ctx, other); !got.Enabled {
		t.Fatal("other user's prefs changed")
	}

	// DB constraints back up the service rules.
	if _, err := pool.Exec(ctx, `UPDATE reminder_preferences SET quiet_hours_start = '22:00' WHERE user_id = $1`, uid); err == nil {
		t.Fatal("DB accepted unpaired quiet hours")
	}
}
