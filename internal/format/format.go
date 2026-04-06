package format

import "fmt"

func Euro(cents int) string {
	return fmt.Sprintf("EUR %.2f", float64(cents)/100)
}
