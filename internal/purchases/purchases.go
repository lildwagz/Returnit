// Package purchases holds purchase domain rules and persistence.
package purchases

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrNotFound   = errors.New("purchases: not found")
	ErrValidation = errors.New("purchases: validation")
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	dateLayout      = "2006-01-02"
)

type Purchase struct {
	ID                uuid.UUID    `json:"id"`
	ProductName       string       `json:"productName"`
	StoreName         *string      `json:"storeName"`
	PurchaseDate      Date         `json:"purchaseDate"`
	Price             *float64     `json:"price"`
	Currency          string       `json:"currency"`
	WarrantyStartDate *Date        `json:"warrantyStartDate"`
	WarrantyEndDate   *Date        `json:"warrantyEndDate"`
	Notes             *string      `json:"notes"`
	Receipt           *ReceiptInfo `json:"receipt"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}

// ReceiptInfo is the receipt metadata embedded in purchase responses.
type ReceiptInfo struct {
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// Input is a validated create/update payload.
type Input struct {
	ProductName       string
	StoreName         *string
	PurchaseDate      time.Time
	Price             *float64
	Currency          string
	WarrantyStartDate *time.Time
	WarrantyEndDate   *time.Time
	Notes             *string
}

// RawInput is the untrusted payload from the transport layer.
type RawInput struct {
	ProductName       string   `json:"productName"`
	StoreName         *string  `json:"storeName"`
	PurchaseDate      string   `json:"purchaseDate"`
	Price             *float64 `json:"price"`
	Currency          string   `json:"currency"`
	WarrantyStartDate *string  `json:"warrantyStartDate"`
	WarrantyEndDate   *string  `json:"warrantyEndDate"`
	Notes             *string  `json:"notes"`
}

type ListFilter struct {
	Query    string
	Status   string // "", active, expiring, expired, none
	Page     int
	PageSize int
}

type Repository interface {
	Create(ctx context.Context, userID uuid.UUID, in Input) (Purchase, error)
	Get(ctx context.Context, userID, id uuid.UUID) (Purchase, error)
	List(ctx context.Context, userID uuid.UUID, f ListFilter) ([]Purchase, int, error)
	Update(ctx context.Context, userID, id uuid.UUID, in Input) (Purchase, error)
	// Delete removes the purchase and returns the receipt storage key, if it had one.
	Delete(ctx context.Context, userID, id uuid.UUID) (receiptKey *string, err error)
}

// ObjectPurger asks the worker to delete receipt files after their purchase is deleted.
type ObjectPurger interface {
	RequestPurge(ctx context.Context)
}

type Service struct {
	repo   Repository
	purger ObjectPurger
}

func NewService(r Repository, p ObjectPurger) *Service { return &Service{repo: r, purger: p} }

func (s *Service) Create(ctx context.Context, userID uuid.UUID, raw RawInput) (Purchase, error) {
	in, err := Validate(raw)
	if err != nil {
		return Purchase{}, err
	}
	return s.repo.Create(ctx, userID, in)
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Purchase, error) {
	return s.repo.Get(ctx, userID, id)
}

func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, raw RawInput) (Purchase, error) {
	in, err := Validate(raw)
	if err != nil {
		return Purchase{}, err
	}
	return s.repo.Update(ctx, userID, id, in)
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	key, err := s.repo.Delete(ctx, userID, id)
	if err != nil {
		return err
	}
	if key != nil && s.purger != nil {
		s.purger.RequestPurge(ctx)
	}
	return nil
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, f ListFilter) ([]Purchase, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = DefaultPageSize
	}
	if f.PageSize > MaxPageSize {
		f.PageSize = MaxPageSize
	}
	switch f.Status {
	case "", "active", "expiring", "expired", "none":
	default:
		return nil, 0, verr("invalid warrantyStatus")
	}
	if utf8.RuneCountInString(f.Query) > 120 {
		return nil, 0, verr("q too long")
	}
	return s.repo.List(ctx, userID, f)
}

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }
func (e validationError) Unwrap() error { return ErrValidation }
func verr(m string) error               { return validationError{m} }

// Validate applies the purchase rules from the technical design notes.
func Validate(r RawInput) (Input, error) {
	name := strings.TrimSpace(r.ProductName)
	if n := utf8.RuneCountInString(name); n < 1 || n > 160 {
		return Input{}, verr("productName must be 1-160 characters")
	}
	pd, err := time.Parse(dateLayout, r.PurchaseDate)
	if err != nil {
		return Input{}, verr("purchaseDate must be YYYY-MM-DD")
	}
	if r.Price != nil && *r.Price < 0 {
		return Input{}, verr("price must be >= 0")
	}
	cur := strings.ToUpper(strings.TrimSpace(r.Currency))
	if cur == "" {
		cur = "IDR"
	}
	if len(cur) != 3 || strings.Trim(cur, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return Input{}, verr("currency must be a 3-letter code")
	}
	ws, err := optDate(r.WarrantyStartDate, "warrantyStartDate")
	if err != nil {
		return Input{}, err
	}
	we, err := optDate(r.WarrantyEndDate, "warrantyEndDate")
	if err != nil {
		return Input{}, err
	}
	if ws != nil && we != nil && we.Before(*ws) {
		return Input{}, verr("warrantyEndDate must be >= warrantyStartDate")
	}
	if r.StoreName != nil && utf8.RuneCountInString(*r.StoreName) > 160 {
		return Input{}, verr("storeName too long")
	}
	if r.Notes != nil && utf8.RuneCountInString(*r.Notes) > 2000 {
		return Input{}, verr("notes too long")
	}
	return Input{name, r.StoreName, pd, r.Price, cur, ws, we, r.Notes}, nil
}

func optDate(s *string, field string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, *s)
	if err != nil {
		return nil, verr(field + " must be YYYY-MM-DD")
	}
	return &t, nil
}
