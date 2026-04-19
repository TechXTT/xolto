package format

import "fmt"

func Euro(cents int) string {
	return fmt.Sprintf("EUR %.2f", float64(cents)/100)
}

// BGN formats a EUR-cents amount for Bulgarian listings.
// Bulgaria adopted EUR on 2026-01-01; EUR is shown primary with BGN in parentheses for familiarity.
// Uses the fixed EUR/BGN peg of 1.95583.
func BGN(eurCents int) string {
	eur := float64(eurCents) / 100
	return fmt.Sprintf("€%.2f (%.0f лв.)", eur, eur*1.95583)
}
