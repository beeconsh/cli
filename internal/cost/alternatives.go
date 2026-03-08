package cost

import "fmt"

// FormatAlternatives returns human-readable strings for cost alternatives.
func FormatAlternatives(alts []Alternative) []string {
	out := make([]string, 0, len(alts))
	for _, a := range alts {
		out = append(out, fmt.Sprintf("%s: %s costs ~$%.0f/mo → %s at ~$%.0f/mo saves $%.0f/mo",
			a.NodeName, a.CurrentInstance, a.CurrentCost,
			a.SuggestedInstance, a.SuggestedCost, a.MonthlySavings))
	}
	return out
}
