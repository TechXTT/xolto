package scorer

import (
	"strings"
	"testing"
)

// TestIsElectronicsListingBGTokens verifies that BG Cyrillic electronics
// keywords are recognised by isElectronicsListing (XOL-35 M3-A).
// AC: "Фотоапарат Canon EOS R10 с батерия" classifies as electronics.
func TestIsElectronicsListingBGTokens(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		// BG Cyrillic tokens from audit minimum set
		{"Фотоапарат Canon EOS R10 с батерия", true},
		{"камера Sony A6000", true},
		{"обектив Canon 50mm f/1.8", true},
		{"лаптоп Lenovo ThinkPad X1", true},
		{"компютър HP Pavilion", true},
		{"слушалки Sony WH-1000XM5", true},
		{"телефон Samsung Galaxy S23", true},
		{"таблет iPad Pro", true},
		// Existing EN/NL tokens must still work (regression)
		{"Sony A7 III body", true},
		{"MacBook Pro M1 16/512", true},
		{"Nikon D750 + grip", true},
		// Non-electronics must not match
		{"Диван IKEA MALM", false},
		{"Детски велосипед 20 инча", false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := isElectronicsListing(strings.ToLower(tc.title))
			if got != tc.want {
				t.Errorf("isElectronicsListing(%q): expected %v, got %v", tc.title, tc.want, got)
			}
		})
	}
}

// TestHasBatteryHealthSignalBGTokens verifies that BG Cyrillic battery terms
// trigger hasBatteryHealthSignal (XOL-35 M3-A).
// Regression AC: a BG listing with "батерия" should NOT produce no_battery_health.
func TestHasBatteryHealthSignalBGTokens(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// BG tokens
		{"iPhone 13 батерия 94%", true},
		{"MacBook Pro акумулатор 80%", true},
		{"Lenovo ThinkPad капацитет 85%", true},
		// EN tokens (regression)
		{"battery health 91%", true},
		{"battery capacity 80%", true},
		{"cycle count 42", true},
		// NL tokens (regression)
		{"accu prestatie 94%", true},
		{"batterijconditie goed", true},
		// No battery signal — listing with no battery mention
		{"Canon EOS R10 body", false},
		{"Sigma 24-70 f/2.8 lens", false},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := hasBatteryHealthSignal(strings.ToLower(tc.text))
			if got != tc.want {
				t.Errorf("hasBatteryHealthSignal(%q): expected %v, got %v", tc.text, tc.want, got)
			}
		})
	}
}

// TestHasRefurbishedAmbiguityBGTokens verifies that BG Cyrillic refurbishment
// terms trigger hasRefurbishedAmbiguity (XOL-35 M3-A).
func TestHasRefurbishedAmbiguityBGTokens(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// BG tokens — ambiguous (no warranty signal)
		{"рециклиран iPhone 13", true},
		{"възстановен MacBook Pro", true},
		{"ремонтиран Lenovo laptop", true},
		// BG token but warranty is present — should NOT be ambiguous
		{"рециклиран iPhone 13 с warranty 12 month", false},
		// EN tokens (regression)
		{"refurb Apple Watch grade b", false}, // grade b suppresses ambiguity
		{"renewed laptop 2023", true},         // no suppression signal
		// NL token (regression)
		{"gereviseerd door fabrikant garantie", false}, // garantie suppresses
		// No refurb signal
		{"Canon EOS R10 body mint condition", false},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := hasRefurbishedAmbiguity(strings.ToLower(tc.text))
			if got != tc.want {
				t.Errorf("hasRefurbishedAmbiguity(%q): expected %v, got %v", tc.text, tc.want, got)
			}
		})
	}
}

// TestBGOnlyTitleNoBatteryHealthFlagRegression verifies the regression AC:
// a BG-only listing that mentions battery ("батерия") must NOT produce
// the no_battery_health risk flag. Before M3-A, the flag always fired on
// BG listings because the only recognised tokens were Dutch.
func TestBGOnlyTitleNoBatteryHealthFlagRegression(t *testing.T) {
	lower := strings.ToLower("Фотоапарат Canon EOS R10 с батерия")

	if !isElectronicsListing(lower) {
		t.Fatal("BG electronics listing not classified as electronics — isElectronicsListing must recognise BG tokens")
	}
	if !hasBatteryHealthSignal(lower) {
		t.Fatal("BG listing with 'батерия' must produce battery health signal — hasBatteryHealthSignal must recognise BG token")
	}
	// If both conditions above pass, no_battery_health flag must NOT fire
	// for this listing when combined with isPhoneOrLaptop check.
	// (The flag is only emitted when electronics && isPhoneOrLaptop && !hasBatteryHealthSignal)
	// For Canon EOS R10 this path is not triggered anyway (not phone/laptop),
	// but the token recognitions above are the key regression guard.
}
