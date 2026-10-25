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
	// SnoozeUntil lists the "remind me again" choices that still fall before the date the reminder is about
	// (tomorrow, in_3_days, next_week); empty when there is none, then the app offers no button.
	SnoozeUntil []string `json:"snoozeUntil"`
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
SELECT n.id, n.purchase_id, n.kind, n.title, n.body, n.sent_at, n.read_at IS NOT NULL,
       CASE WHEN n.kind = 'return' THEN p.return_deadline ELSE p.warranty_end_date END,
       n.warranty_end_date, n.offset_days,
       COALESCE(rp.timezone, 'Asia/Jakarta'), to_char(rp.quiet_hours_start, 'HH24:MI'), to_char(rp.quiet_hours_end, 'HH24:MI')
FROM notifications n
LEFT JOIN purchases p ON p.id = n.purchase_id
LEFT JOIN reminder_preferences rp ON rp.user_id = n.user_id
WHERE n.user_id = $1 AND n.status = 'sent' AND (NOT $2 OR n.read_at IS NULL)
ORDER BY n.sent_at DESC, n.id LIMIT $3`, userID, unreadOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("notifications: list: %w", err)
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var (
			n                      Notification
			currentEnd, plannedEnd *time.Time
			offset                 *int
			tz                     string
			qs, qe                 *string
		)
		if err := rows.Scan(&n.ID, &n.PurchaseID, &n.Kind, &n.Title, &n.Body, &n.CreatedAt, &n.Read,
			&currentEnd, &plannedEnd, &offset, &tz, &qs, &qe); err != nil {
			return nil, fmt.Errorf("notifications: scan: %w", err)
		}
		n.SnoozeUntil = []string{}
		// Same conditions as Snooze: a purchase, a planned date that is still the current one.
		if n.PurchaseID != nil && offset != nil && currentEnd != nil && plannedEnd != nil && currentEnd.Equal(*plannedEnd) {
			now := time.Now()
			if s.Now != nil {
				now = s.Now()
			}
			n.SnoozeUntil = SnoozeOptions(now, loadLocation(tz), qs, qe, currentEnd)
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
