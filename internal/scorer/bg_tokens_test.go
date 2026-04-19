package scorer

import (
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
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

// TestCarrierLockedRiskFlag verifies that carrier_locked fires on BG Cyrillic and EN
// carrier-locking signals on phone listings, and does not fire on clean listings (XOL-98).
func TestCarrierLockedRiskFlag(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		description string
		wantFlag    bool
	}{
		{
			name:        "bg_zaluchen_za_mtel",
			title:       "телефон Samsung Galaxy заключен за мтел",
			description: "",
			wantFlag:    true,
		},
		{
			name:        "en_carrier_locked",
			title:       "iPhone 13 carrier locked AT&T",
			description: "",
			wantFlag:    true,
		},
		{
			name:        "en_network_locked_in_desc",
			title:       "Samsung Galaxy S22",
			description: "network locked to Vodafone",
			wantFlag:    true,
		},
		{
			name:        "en_sim_locked",
			title:       "iPhone 12 sim locked",
			description: "",
			wantFlag:    true,
		},
		{
			name:        "clean_phone_listing",
			title:       "iPhone 14 Pro 256GB unlocked",
			description: "отлично състояние",
			wantFlag:    false,
		},
		{
			name:        "no_false_positive_lokalen",
			title:       "телефон Xiaomi локален",
			description: "",
			wantFlag:    false, // "локален" must not match " лок " with spaces
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := computeRiskFlags(models.Listing{
				ItemID:      "test-cl-" + tc.name,
				Title:       tc.title,
				Description: tc.description,
				Price:       50000,
				PriceType:   "fixed",
				Condition:   "good",
				ImageURLs:   []string{"a", "b", "c"},
			}, 0)
			got := containsFlag(flags, "carrier_locked")
			if got != tc.wantFlag {
				t.Errorf("carrier_locked for title=%q desc=%q: got %v, want %v (flags=%v)",
					tc.title, tc.description, got, tc.wantFlag, flags)
			}
		})
	}
}

// TestScreenReplacedRiskFlag verifies that screen_replaced fires on BG Cyrillic and EN
// display-replacement signals on phone listings, and does not fire on clean listings (XOL-98).
func TestScreenReplacedRiskFlag(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		description string
		wantFlag    bool
	}{
		{
			name:        "bg_smenen_displej",
			title:       "телефон Samsung сменен дисплей",
			description: "",
			wantFlag:    true,
		},
		{
			name:        "bg_nov_ekran_in_desc",
			title:       "iPhone 11",
			description: "нов екран, батерия 89%",
			wantFlag:    true,
		},
		{
			name:        "en_screen_replaced",
			title:       "iPhone 12 screen replaced 64GB",
			description: "",
			wantFlag:    true,
		},
		{
			name:        "en_aftermarket_screen",
			title:       "Samsung Galaxy S21",
			description: "aftermarket screen but works perfectly",
			wantFlag:    true,
		},
		{
			name:        "clean_phone_no_screen_mention",
			title:       "iPhone 13 128GB like new",
			description: "оригинален, без забележки",
			wantFlag:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := computeRiskFlags(models.Listing{
				ItemID:      "test-sr-" + tc.name,
				Title:       tc.title,
				Description: tc.description,
				Price:       50000,
				PriceType:   "fixed",
				Condition:   "good",
				ImageURLs:   []string{"a", "b", "c"},
			}, 0)
			got := containsFlag(flags, "screen_replaced")
			if got != tc.wantFlag {
				t.Errorf("screen_replaced for title=%q desc=%q: got %v, want %v (flags=%v)",
					tc.title, tc.description, got, tc.wantFlag, flags)
			}
		})
	}
}

// TestIsPhoneOrLaptopCyrillicBrands verifies that XOL-98 BG Cyrillic brand terms
// are recognised by isPhoneOrLaptop.
func TestIsPhoneOrLaptopCyrillicBrands(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"айфон 13 128gb", true},
		{"самсунг галакси s22", true},
		{"хуауей p40 pro", true},
		{"моторола edge 30", true},
		// regression: existing terms still work
		{"iphone 14 pro", true},
		{"samsung galaxy s23", true},
		{"телефон xiaomi", true},
		// non-phone must not match
		{"диван ikea malm", false},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := isPhoneOrLaptop(strings.ToLower(tc.text))
			if got != tc.want {
				t.Errorf("isPhoneOrLaptop(%q): got %v, want %v", tc.text, got, tc.want)
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
