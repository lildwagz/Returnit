package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("notifications: not found")

const (
	DefaultLimit = 50
	MaxLimit     = 100
)

// Notification is the inbox representation. CreatedAt is the delivery time.
type Notification struct {
	ID         uuid.UUID  `json:"id"`
	PurchaseID *uuid.UUID `json:"purchaseId"`
	// Kind is "warranty" (the end of the warranty) or "return" (the end of the return period).
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
}

type Service struct {
	pool *pgxpool.Pool
	// Now is the clock (tests set it); nil means time.Now.
	Now func() time.Time
}

func NewService(p *pgxpool.Pool) *Service { return &Service{pool: p} }

// List returns the user's delivered notifications, newest first. Only status 'sent' rows are
// visible: scheduled, cancelled and failed ones are internal.
func (s *Service) List(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) ([]Notification, error) {
	if limit < 1 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	rows, err := s.pool.Query(ctx, `
SELECT id, purchase_id, kind, title, body, sent_at, read_at IS NOT NULL
FROM notifications
WHERE user_id = $1 AND status = 'sent' AND (NOT $2 OR read_at IS NULL)
ORDER BY sent_at DESC, id LIMIT $3`, userID, unreadOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("notifications: list: %w", err)
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.PurchaseID, &n.Kind, &n.Title, &n.Body, &n.CreatedAt, &n.Read); err != nil {
			return nil, fmt.Errorf("notifications: scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead is idempotent: the first call sets read_at, later calls keep it. Notifications that
// belong to someone else, do not exist, or are not delivered yet all report ErrNotFound.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE notifications SET read_at = COALESCE(read_at, now())
WHERE id = $1 AND user_id = $2 AND status = 'sent'`, id, userID)
	if err != nil {
		return fmt.Errorf("notifications: mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
