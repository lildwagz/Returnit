package purchases

import (
	"context"

	"github.com/google/uuid"
)

// Summary is what the Home screen shows in one call: how many purchases are in each warranty state, how much is still
// covered, and which date comes up next. The states and the date use the database's calendar day (UTC), the same
// rule as the list filter, so the numbers agree with the lists.
type Summary struct {
	Counts SummaryCounts `json:"counts"`
	// CoveredValue is the total price of purchases whose warranty has not ended (active or expiring), per currency.
	CoveredValue []CurrencyAmount `json:"coveredValue"`
	// NextExpiring is the purchase whose warranty ends soonest (today or later); nil when there is none.
	NextExpiring *SummaryItem `json:"nextExpiring"`
	// NextReturn is the purchase whose return deadline comes soonest (today or later); nil when there is none.
	NextReturn *SummaryItem `json:"nextReturn"`
}

type SummaryCounts struct {
	Active   int `json:"active"`
	Expiring int `json:"expiring"`
	Expired  int `json:"expired"`
	Unknown  int `json:"unknown"` // no warranty end date
	Total    int `json:"total"`
}

type CurrencyAmount struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type SummaryItem struct {
	ID          uuid.UUID `json:"id"`
	ProductName string    `json:"productName"`
	Date        Date      `json:"date"`
}

// Summary returns the Home overview of one user's purchases.
func (s *Service) Summary(ctx context.Context, userID uuid.UUID) (Summary, error) {
	return s.repo.Summary(ctx, userID)
}
