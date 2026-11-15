// Package servicelog keeps the service history of a purchase: visits to a service centre and warranty claims.
// A record belongs to its purchase (deleted with it) and is seen by everyone who can see the purchase.
package servicelog

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrNotFound   = errors.New("servicelog: not found")
	ErrValidation = errors.New("servicelog: validation")
	ErrLimit      = errors.New("servicelog: too many records for this purchase")
)

// MaxPerPurchase is how many service records one purchase can hold.
const MaxPerPurchase = 100

const dateLayout = "2006-01-02"

// Date marshals as a date-only JSON string.
type Date struct{ time.Time }

func (d Date) MarshalJSON() ([]byte, error) { return []byte(`"` + d.Format(dateLayout) + `"`), nil }

type Record struct {
	ID          uuid.UUID `json:"id"`
	PurchaseID  uuid.UUID `json:"purchaseId"`
	ServiceDate Date      `json:"serviceDate"`
	Place       *string   `json:"place"`   // service centre or shop
	Problem     *string   `json:"problem"` // what was wrong
	Result      *string   `json:"result"`  // what was done
	Cost        *float64  `json:"cost"`    // in the purchase's currency; null when free or unknown
	// AddedBy is the display name of the member who added it (null once that person's account is erased);
	// AddedByMe is true for the caller.
	AddedBy   *string   `json:"addedBy"`
	AddedByMe bool      `json:"addedByMe"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RawInput is the untrusted create/update payload.
type RawInput struct {
	ServiceDate string   `json:"serviceDate"`
	Place       *string  `json:"place"`
	Problem     *string  `json:"problem"`
	Result      *string  `json:"result"`
	Cost        *float64 `json:"cost"`
}

// Input is a validated payload.
type Input struct {
	ServiceDate time.Time
	Place       *string
	Problem     *string
	Result      *string
	Cost        *float64
}

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }
func (e validationError) Unwrap() error { return ErrValidation }
func verr(m string) error               { return validationError{m} }

// Validate checks a payload: a date is required, the texts are optional and trimmed (empty means none).
func Validate(r RawInput) (Input, error) {
	d, err := time.Parse(dateLayout, r.ServiceDate)
	if err != nil {
		return Input{}, verr("serviceDate must be YYYY-MM-DD")
	}
	place, err := optText(r.Place, "place", 160)
	if err != nil {
		return Input{}, err
	}
	problem, err := optText(r.Problem, "problem", 1000)
	if err != nil {
		return Input{}, err
	}
	result, err := optText(r.Result, "result", 1000)
	if err != nil {
		return Input{}, err
	}
	if r.Cost != nil && (*r.Cost < 0 || *r.Cost > 9_999_999_999) {
		return Input{}, verr("cost must be between 0 and 9999999999")
	}
	return Input{ServiceDate: d, Place: place, Problem: problem, Result: result, Cost: r.Cost}, nil
}

func optText(s *string, field string, max int) (*string, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > max {
		return nil, verr(field + " too long")
	}
	return &v, nil
}

type Repository interface {
	// List returns the records of a purchase the user can see, newest service date first; ErrNotFound for a purchase
	// the user cannot see.
	List(ctx context.Context, userID, purchaseID uuid.UUID) ([]Record, error)
	// Create adds a record, refusing with ErrLimit when the purchase already has `limit`.
	Create(ctx context.Context, userID, purchaseID uuid.UUID, in Input, limit int) (Record, error)
	Update(ctx context.Context, userID, purchaseID, id uuid.UUID, in Input) (Record, error)
	Delete(ctx context.Context, userID, purchaseID, id uuid.UUID) error
}

type Service struct{ repo Repository }

func NewService(r Repository) *Service { return &Service{repo: r} }

func (s *Service) List(ctx context.Context, userID, purchaseID uuid.UUID) ([]Record, error) {
	return s.repo.List(ctx, userID, purchaseID)
}

func (s *Service) Create(ctx context.Context, userID, purchaseID uuid.UUID, raw RawInput) (Record, error) {
	in, err := Validate(raw)
	if err != nil {
		return Record{}, err
	}
	return s.repo.Create(ctx, userID, purchaseID, in, MaxPerPurchase)
}

func (s *Service) Update(ctx context.Context, userID, purchaseID, id uuid.UUID, raw RawInput) (Record, error) {
	in, err := Validate(raw)
	if err != nil {
		return Record{}, err
	}
	return s.repo.Update(ctx, userID, purchaseID, id, in)
}

func (s *Service) Delete(ctx context.Context, userID, purchaseID, id uuid.UUID) error {
	return s.repo.Delete(ctx, userID, purchaseID, id)
}
