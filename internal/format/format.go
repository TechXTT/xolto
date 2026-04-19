package format

import "fmt"

func Euro(cents int) string {
	return fmt.Sprintf("EUR %.2f", float64(cents)/100)
}

// BGN converts EUR cents to Bulgarian lev and formats as "NNN лв."
// Uses the fixed EUR/BGN peg of 1.95583.
func BGN(eurCents int) string {
	return fmt.Sprintf("%.0f лв.", float64(eurCents)/100*1.95583)
}
