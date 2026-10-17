// Package reminders holds reminder preference rules and persistence.
package reminders

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

var ErrValidation = errors.New("reminders: validation")

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }
func (e validationError) Unwrap() error { return ErrValidation }
func verr(m string) error               { return validationError{m} }

// AllowedDays is the allow-list of reminder offsets (days before warranty end).
var AllowedDays = []int{1, 3, 7, 14, 30}

var allowedChannels = []string{"push", "email", "in_app"}

// Preferences is the stored/returned form.
type Preferences struct {
	Enabled         bool    `json:"enabled"`
	DaysBefore      []int   `json:"daysBefore"`
	Channel         string  `json:"channel"`
	QuietHoursStart *string `json:"quietHoursStart"`
	QuietHoursEnd   *string `json:"quietHoursEnd"`
	Timezone        string  `json:"timezone"`
}

// DefaultTimezone is WIB. The app targets Indonesia; other IANA zones (e.g. Asia/Makassar for
// WITA, Asia/Jayapura for WIT) are accepted but the server locale is never used.
const DefaultTimezone = "Asia/Jakarta"

// Default is returned for users who have never saved preferences (nothing is written).
func Default() Preferences {
	return Preferences{Enabled: true, DaysBefore: []int{30, 14, 7, 3, 1}, Channel: "push", Timezone: DefaultTimezone}
}

// Input is the untrusted PUT payload. Pointers distinguish "missing" from zero values.
type Input struct {
	Enabled         *bool   `json:"enabled"`
	DaysBefore      *[]int  `json:"daysBefore"`
	Channel         *string `json:"channel"`
	QuietHoursStart *string `json:"quietHoursStart"`
	QuietHoursEnd   *string `json:"quietHoursEnd"`
	Timezone        *string `json:"timezone"`
}

type Repository interface {
	Get(ctx context.Context, userID uuid.UUID) (Preferences, bool, error)
	Upsert(ctx context.Context, userID uuid.UUID, p Preferences) (Preferences, error)
}

type Service struct{ repo Repository }

func NewService(r Repository) *Service { return &Service{repo: r} }

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	p, found, err := s.repo.Get(ctx, userID)
	if err != nil {
		return Preferences{}, err
	}
	if !found {
		return Default(), nil
	}
	return p, nil
}

func (s *Service) Replace(ctx context.Context, userID uuid.UUID, in Input) (Preferences, error) {
	p, err := Validate(in)
	if err != nil {
		return Preferences{}, err
	}
	return s.repo.Upsert(ctx, userID, p)
}

// Validate normalises (sorted descending, de-duplicated check) and validates a replace request.
func Validate(in Input) (Preferences, error) {
	if in.Enabled == nil || in.DaysBefore == nil || in.Channel == nil {
		return Preferences{}, verr("enabled, daysBefore and channel are required")
	}
	days := slices.Clone(*in.DaysBefore)
	slices.SortFunc(days, func(a, b int) int { return b - a })
	for i, d := range days {
		if !slices.Contains(AllowedDays, d) {
			return Preferences{}, verr("daysBefore values must be one of 1, 3, 7, 14, 30")
		}
		if i > 0 && days[i-1] == d {
			return Preferences{}, verr("daysBefore values must be unique")
		}
	}
	if *in.Enabled && len(days) == 0 {
		return Preferences{}, verr("daysBefore must not be empty while reminders are enabled")
	}
	if !slices.Contains(allowedChannels, *in.Channel) {
		return Preferences{}, verr("channel must be one of push, email, in_app")
	}

	p := Preferences{Enabled: *in.Enabled, DaysBefore: days, Channel: *in.Channel}

	qs, qe := blankToNil(in.QuietHoursStart), blankToNil(in.QuietHoursEnd)
	if (qs == nil) != (qe == nil) {
		return Preferences{}, verr("quietHoursStart and quietHoursEnd must be set together")
	}
	if qs != nil {
		a, err := parseClock(*qs)
		if err != nil {
			return Preferences{}, verr("quietHoursStart must be HH:MM (00:00-23:59)")
		}
		b, err := parseClock(*qe)
		if err != nil {
			return Preferences{}, verr("quietHoursEnd must be HH:MM (00:00-23:59)")
		}
		if a.Equal(b) {
			return Preferences{}, verr("quiet hours start and end must differ")
		}
		p.QuietHoursStart, p.QuietHoursEnd = qs, qe // overnight windows (start > end) are allowed
	}

	p.Timezone = DefaultTimezone
	if tz := blankToNil(in.Timezone); tz != nil {
		if len(*tz) > 64 {
			return Preferences{}, verr("timezone too long")
		}
		if _, err := time.LoadLocation(*tz); err != nil || *tz == "Local" || *tz == "UTC0" {
			return Preferences{}, verr("timezone must be a valid IANA name, e.g. Asia/Jakarta")
		}
		p.Timezone = *tz
	}
	return p, nil
}

func blankToNil(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// parseClock accepts only zero-padded 24h HH:MM.
func parseClock(s string) (time.Time, error) {
	if len(s) != 5 { // time.Parse alone would accept "9:00"
		return time.Time{}, errors.New("reminders: clock must be HH:MM")
	}
	return time.Parse("15:04", s)
}
