package purchases

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPastDays = 30
	MaxPastDays     = 90
	maxDeadlines    = 500
)

// Deadline is one date the "Tenggat" tab shows: the end of a purchase's warranty or its return deadline. A purchase with
// both dates gives two items.
type Deadline struct {
	PurchaseID  uuid.UUID `json:"purchaseId"`
	ProductName string    `json:"productName"`
	Category    *string   `json:"category"`
	Kind        string    `json:"kind"` // "warranty" or "return"
	Date        Date      `json:"date"`
}

// Deadlines lists the user's warranty ends and return deadlines from `pastDays` days ago onward (so recent ones that have
// just passed are included), soonest first. "Today" is the database's calendar day (UTC), the same rule as the list
// filter and the summary; the app groups the items by its own local date.
func (s *Service) Deadlines(ctx context.Context, userID uuid.UUID, pastDays int) ([]Deadline, error) {
	if pastDays < 0 || pastDays > MaxPastDays {
		return nil, verr(fmt.Sprintf("pastDays must be between 0 and %d", MaxPastDays))
	}
	return s.repo.Deadlines(ctx, userID, pastDays)
}

func (r *PGRepo) Deadlines(ctx context.Context, userID uuid.UUID, pastDays int) ([]Deadline, error) {
	rows, err := r.pool.Query(ctx, `
SELECT p.id, p.product_name, p.category, d.kind, d.day
FROM purchases p
CROSS JOIN LATERAL (VALUES ('warranty'::text, p.warranty_end_date), ('return'::text, p.return_deadline)) AS d(kind, day)
WHERE p.user_id = $1 AND d.day IS NOT NULL AND d.day >= CURRENT_DATE - $2::int
ORDER BY d.day, p.id, d.kind
LIMIT $3`, userID, pastDays, maxDeadlines)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	items := []Deadline{}
	for rows.Next() {
		var d Deadline
		var day time.Time
		if err := rows.Scan(&d.PurchaseID, &d.ProductName, &d.Category, &d.Kind, &day); err != nil {
			return nil, mapErr(err)
		}
		d.Date = Date{day}
		items = append(items, d)
	}
	return items, mapErr(rows.Err())
}
