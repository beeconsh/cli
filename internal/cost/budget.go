package cost

import (
	"fmt"
	"strconv"
	"strings"
)

// Budget represents a parsed budget constraint.
type Budget struct {
	Amount   float64 // dollar amount
	Period   string  // "mo" (monthly) or "yr" (yearly)
	Raw      string  // original string
}

// MonthlyAmount returns the budget as a monthly dollar amount.
func (b *Budget) MonthlyAmount() float64 {
	if b.Period == "yr" {
		return b.Amount / 12
	}
	return b.Amount
}

// ParseBudget parses a budget string like "5000/mo" or "60000/yr".
// Returns nil for empty strings.
func ParseBudget(raw string) (*Budget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// Strip leading $ if present
	raw = strings.TrimPrefix(raw, "$")

	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid budget format %q (expected amount/period, e.g. 5000/mo)", raw)
	}

	amount, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid budget amount %q: %w", parts[0], err)
	}
	if amount < 0 {
		return nil, fmt.Errorf("budget amount must be positive, got %v", amount)
	}

	period := strings.ToLower(strings.TrimSpace(parts[1]))
	switch period {
	case "mo", "month", "monthly":
		period = "mo"
	case "yr", "year", "yearly", "annual":
		period = "yr"
	default:
		return nil, fmt.Errorf("invalid budget period %q (expected mo or yr)", period)
	}

	return &Budget{
		Amount: amount,
		Period: period,
		Raw:    raw,
	}, nil
}
