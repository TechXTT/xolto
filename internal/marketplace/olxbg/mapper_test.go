package olxbg

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// TestCurrencyStatusEUR verifies that a EUR-quoted offer is stored as EUR cents
// with no BGN conversion applied. A 1000 EUR listing must NOT be divided by
// 1.95583 — it must land as 100000 EUR cents (within ±1% of truth per XOL-30 AC).
func TestCurrencyStatusEUR(t *testing.T) {
	eurCents, status := CurrencyStatusFromAPI(1000.0, "EUR", "test-offer-1")
	if status != "eur_native" {
		t.Errorf("expected status eur_native, got %q", status)
	}
	want := 100000 // 1000 EUR × 100
	if eurCents != want {
		t.Errorf("EUR listing: expected %d EUR cents, got %d (delta %.2f%%)",
			want, eurCents, math.Abs(float64(eurCents-want))/float64(want)*100)
	}
}

// TestCurrencyStatusBGN verifies that a BGN-quoted offer is converted to EUR
// cents using the fixed 1.95583 peg. 700 BGN → 700/1.95583 EUR ≈ 357.90 EUR
// → 35790 cents.
func TestCurrencyStatusBGN(t *testing.T) {
	eurCents, status := CurrencyStatusFromAPI(700.0, "BGN", "test-offer-2")
	if status != "converted_from_bgn" {
		t.Errorf("expected status converted_from_bgn, got %q", status)
	}
	// 700 BGN in stotinki = 70000; 70000 / 1.95583 ≈ 35790
	wantApprox := 35790
	delta := eurCents - wantApprox
	if delta < -5 || delta > 5 {
		t.Errorf("BGN listing: expected ~%d EUR cents, got %d (delta %+d)", wantApprox, eurCents, delta)
	}
}

// TestCurrencyStatusMissing verifies that a missing/unknown currency falls back
// to the BGN assumption and emits status "unknown".
func TestCurrencyStatusMissing(t *testing.T) {
	eurCents, status := CurrencyStatusFromAPI(500.0, "", "test-offer-3")
	if status != "unknown" {
		t.Errorf("expected status unknown, got %q", status)
	}
	// Should still return a non-zero price so the listing is not silently dropped.
	if eurCents <= 0 {
		t.Errorf("expected non-zero EUR cents for unknown currency fallback, got %d", eurCents)
	}
}

// TestCurrencyStatusUnrecognised verifies that an unrecognised currency code
// also triggers the "unknown" fallback path.
func TestCurrencyStatusUnrecognised(t *testing.T) {
	_, status := CurrencyStatusFromAPI(200.0, "USD", "test-offer-4")
	if status != "unknown" {
		t.Errorf("expected status unknown for USD, got %q", status)
	}
}

// TestMapListingEURPrice verifies the end-to-end mapping path: a EUR-quoted
// offer must emerge with the correct EUR-cents price and currency_status
// attribute set to "eur_native" (no BGN conversion).
func TestMapListingEURPrice(t *testing.T) {
	offer := apiOffer{
		ID:    "ABC123",
		URL:   "https://www.olx.bg/ad/ABC123.html",
		Title: "Canon EOS R10 body + 18-45",
		Params: []apiParam{
			{
				Key: "price",
				Value: paramValue{
					Value:    700,
					Currency: "EUR",
					Type:     "price",
				},
			},
			{
				Key: "state",
				Value: paramValue{
					Key:   "used",
					Label: "Употребявана",
				},
			},
		},
	}
	listing := mapListing(offer)

	// 700 EUR → 70000 EUR cents (no division by 1.95583)
	wantCents := 70000
	if listing.Price != wantCents {
		t.Errorf("EUR offer: expected price %d cents, got %d (bug: %.1f%% off truth)",
			wantCents, listing.Price,
			math.Abs(float64(listing.Price-wantCents))/float64(wantCents)*100,
		)
	}
	if got := listing.Attributes["currency_status"]; got != "eur_native" {
		t.Errorf("expected currency_status=eur_native, got %q", got)
	}
}

// TestMapListingBGNPrice verifies that a BGN-quoted offer is correctly
// converted to EUR cents and tagged with currency_status=converted_from_bgn.
func TestMapListingBGNPrice(t *testing.T) {
	offer := apiOffer{
		ID:    "DEF456",
		URL:   "https://www.olx.bg/ad/DEF456.html",
		Title: "MacBook Air 2019 i5",
		Params: []apiParam{
			{
				Key: "price",
				Value: paramValue{
					Value:    390,
					Currency: "BGN",
					Type:     "price",
				},
			},
		},
	}
	listing := mapListing(offer)

	// 390 BGN → 199.40 EUR → 19940 EUR cents (approx)
	// 390 * 100 / 1.95583 ≈ 19940 cents
	if listing.Price <= 0 {
		t.Fatalf("BGN offer: expected non-zero price, got %d", listing.Price)
	}
	if got := listing.Attributes["currency_status"]; got != "converted_from_bgn" {
		t.Errorf("expected currency_status=converted_from_bgn, got %q", got)
	}
}

// TestMapListingMissingCurrency verifies that a listing with no currency field
// emits status=unknown and still returns a non-zero price.
func TestMapListingMissingCurrency(t *testing.T) {
	offer := apiOffer{
		ID:    "GHI789",
		URL:   "https://www.olx.bg/ad/GHI789.html",
		Title: "Sony WH-1000XM5",
		Params: []apiParam{
			{
				Key: "price",
				Value: paramValue{
					Value: 250,
					// Currency deliberately omitted
				},
			},
		},
	}
	listing := mapListing(offer)

	if listing.Price <= 0 {
		t.Errorf("missing-currency offer: expected non-zero price fallback, got %d", listing.Price)
	}
	if got := listing.Attributes["currency_status"]; got != "unknown" {
		t.Errorf("expected currency_status=unknown, got %q", got)
	}
}

// TestEURPriceAccuracy verifies the ±1% accuracy requirement from XOL-30 AC:
// sampled EUR-quoted listings must map to within ±1% of the OLX-displayed price.
func TestEURPriceAccuracy(t *testing.T) {
	cases := []struct {
		name      string
		quotedEUR float64
		wantCents int
	}{
		{"Canon EOS 5D Mark III", 850, 85000},
		{"Sony FE 24-70 F/2.8 GM", 1800, 180000},
		{"iPhone 13 Pro 256", 720, 72000},
		{"Samsung Galaxy S23 Ultra", 850, 85000},
		{"Sigma 24-70 F/2.8 DG DN", 920, 92000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eurCents, _ := CurrencyStatusFromAPI(tc.quotedEUR, "EUR", "test")
			deltaFrac := math.Abs(float64(eurCents-tc.wantCents)) / float64(tc.wantCents)
			if deltaFrac > 0.01 {
				t.Errorf("%s: got %d cents, want %d cents (%.2f%% off, limit 1%%)",
					tc.name, eurCents, tc.wantCents, deltaFrac*100)
			}
		})
	}
}

// TestNormalizeConditionEnKeys verifies English API key mappings (XOL-40 M3-F).
func TestNormalizeConditionEnKeys(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"new", "new"},
		{"New", "new"}, // case-insensitive
		{"like_new", "like_new"},
		{"likenew", "like_new"},
		{"good", "good"},
		{"fair", "fair"},
		{"for_parts", "for_parts"},
		{"forparts", "for_parts"},
		{"used", "used"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := normalizeCondition(tc.key, "")
			if got != tc.want {
				t.Errorf("key=%q: expected %q, got %q", tc.key, tc.want, got)
			}
		})
	}
}

// TestNormalizeConditionBGLabels verifies Bulgarian Cyrillic label mappings (XOL-40 M3-F).
func TestNormalizeConditionBGLabels(t *testing.T) {
	cases := []struct {
		label string
		want  string
	}{
		{"Нова", "new"},
		{"Ново", "new"},
		{"Като нова", "like_new"},
		{"Като ново", "like_new"},
		{"Добра", "good"},
		{"Добро", "good"},
		{"Приемлива", "fair"},
		{"За части", "for_parts"},
		{"Използвана", "used"},
		{"Употребявана", "used"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			// key="" forces label fallback path
			got := normalizeCondition("", tc.label)
			if got != tc.want {
				t.Errorf("label=%q: expected %q, got %q", tc.label, tc.want, got)
			}
		})
	}
}

// TestNormalizeConditionUnknownKeyEmitsUnknown verifies that an unrecognised key
// emits "unknown" rather than forwarding the raw value (XOL-40 M3-F).
func TestNormalizeConditionUnknownKeyEmitsUnknown(t *testing.T) {
	got := normalizeCondition("damaged", "")
	if got != "unknown" {
		t.Errorf("unrecognised key: expected unknown, got %q", got)
	}
}

// TestNormalizeConditionVagueConditionFlagSuppressed verifies that a listing with a
// valid condition key (e.g. "like_new") does NOT get Condition="" — which would
// previously cause the scorer's vague_condition flag to fire falsely (XOL-40 M3-F).
func TestNormalizeConditionVagueConditionFlagSuppressed(t *testing.T) {
	offer := apiOffer{
		ID:    "VALID1",
		URL:   "https://www.olx.bg/ad/VALID1.html",
		Title: "iPhone 13 Pro",
		Params: []apiParam{
			{
				Key: "price",
				Value: paramValue{Value: 700, Currency: "EUR", Type: "price"},
			},
			{
				Key: "state",
				Value: paramValue{Key: "like_new", Label: "Като нова"},
			},
		},
	}
	listing := mapListing(offer)
	if listing.Condition == "" {
		t.Errorf("like_new condition must not produce empty Condition field; got %q", listing.Condition)
	}
	if listing.Condition != "like_new" {
		t.Errorf("expected condition like_new, got %q", listing.Condition)
	}
}

// TestMapListingFromFixture is a table-driven golden-fixture test for
// mapListing(). Each sub-test loads a real JSON fixture from testdata/, unmarshals
// it into apiOffer, and asserts the full set of output fields on the returned
// models.Listing. The goal is to catch OLX.bg API schema drift immediately rather
// than having it silently produce zero values in production (XOL-87 C-11).
func TestMapListingFromFixture(t *testing.T) {
	// BGN laptop expected EUR cents: 800 BGN → 80000 stotinki → BGNStotinkiToEURCents(80000)
	bgnLaptopEURCents := BGNStotinkiToEURCents(int(math.Round(800.0 * 100)))

	cases := []struct {
		name               string
		fixture            string
		wantTitle          string
		wantURL            string
		wantPriceCents     int
		wantPriceType      string
		wantCondition      string
		wantSellerName     string
		wantCity           string
		wantImageCount     int
		wantCurrency       string
		wantCurrencyStatus string
		wantPriceLocal     string
		wantPriceLocalCcy  string
	}{
		{
			name:               "EUR camera — fixed price, good condition, 2 photos",
			fixture:            "testdata/olxbg_eur_camera.json",
			wantTitle:          "Canon EOS R10 body + 18-45 kit",
			wantURL:            "https://www.olx.bg/ad/canon-eos-r10-CID101.html",
			wantPriceCents:     70000, // 700 EUR × 100
			wantPriceType:      "fixed",
			wantCondition:      "good",
			wantSellerName:     "Иван Петров",
			wantCity:           "София",
			wantImageCount:     2,
			wantCurrency:       "EUR",
			wantCurrencyStatus: CurrencyStatusEURNative,
			wantPriceLocal:     "700.00",
			wantPriceLocalCcy:  "EUR",
		},
		{
			name:               "BGN laptop — negotiable, like_new, 1 photo",
			fixture:            "testdata/olxbg_bgn_laptop.json",
			wantTitle:          "MacBook Air 2019 i5 8GB 256GB",
			wantURL:            "https://www.olx.bg/ad/macbook-air-2019-CID102.html",
			wantPriceCents:     bgnLaptopEURCents,
			wantPriceType:      "negotiable",
			wantCondition:      "like_new",
			wantSellerName:     "Мария Иванова",
			wantCity:           "Пловдив",
			wantImageCount:     1,
			wantCurrency:       "EUR",
			wantCurrencyStatus: CurrencyStatusConvertedFromBGN,
			wantPriceLocal:     "800.00",
			wantPriceLocalCcy:  "BGN",
		},
		{
			name:               "EUR for_parts phone — fixed price, for_parts condition, 1 photo",
			fixture:            "testdata/olxbg_for_parts_phone.json",
			wantTitle:          "iPhone 13 за части счупен екран",
			wantURL:            "https://www.olx.bg/ad/iphone-13-za-chasti-CID103.html",
			wantPriceCents:     30000, // 300 EUR × 100
			wantPriceType:      "fixed",
			wantCondition:      "for_parts",
			wantSellerName:     "Георги Стоянов",
			wantCity:           "Варна",
			wantImageCount:     1,
			wantCurrency:       "EUR",
			wantCurrencyStatus: CurrencyStatusEURNative,
			wantPriceLocal:     "300.00",
			wantPriceLocalCcy:  "EUR",
		},
		{
			name:               "EUR phone — missing photos (empty array)",
			fixture:            "testdata/olxbg_missing_photos.json",
			wantTitle:          "Samsung Galaxy S23 256GB",
			wantURL:            "https://www.olx.bg/ad/samsung-galaxy-s23-CID104.html",
			wantPriceCents:     55000, // 550 EUR × 100
			wantPriceType:      "fixed",
			wantCondition:      "used",
			wantSellerName:     "Петър Димитров",
			wantCity:           "Бургас",
			wantImageCount:     0,
			wantCurrency:       "EUR",
			wantCurrencyStatus: CurrencyStatusEURNative,
			wantPriceLocal:     "550.00",
			wantPriceLocalCcy:  "EUR",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tc.fixture, err)
			}

			var offer apiOffer
			if err := json.Unmarshal(raw, &offer); err != nil {
				t.Fatalf("failed to unmarshal fixture %s: %v", tc.fixture, err)
			}

			listing := mapListing(offer)

			if listing.Title != tc.wantTitle {
				t.Errorf("Title: got %q, want %q", listing.Title, tc.wantTitle)
			}
			if listing.URL != tc.wantURL {
				t.Errorf("URL: got %q, want %q", listing.URL, tc.wantURL)
			}
			if listing.Price != tc.wantPriceCents {
				t.Errorf("Price: got %d cents, want %d cents", listing.Price, tc.wantPriceCents)
			}
			if listing.PriceType != tc.wantPriceType {
				t.Errorf("PriceType: got %q, want %q", listing.PriceType, tc.wantPriceType)
			}
			if listing.Condition != tc.wantCondition {
				t.Errorf("Condition: got %q, want %q", listing.Condition, tc.wantCondition)
			}
			if listing.Seller.Name != tc.wantSellerName {
				t.Errorf("Seller.Name: got %q, want %q", listing.Seller.Name, tc.wantSellerName)
			}
			if got := listing.Attributes["city"]; got != tc.wantCity {
				t.Errorf("Attributes[city]: got %q, want %q", got, tc.wantCity)
			}
			if got := len(listing.ImageURLs); got != tc.wantImageCount {
				t.Errorf("len(ImageURLs): got %d, want %d", got, tc.wantImageCount)
			}
			if got := listing.Attributes["currency"]; got != tc.wantCurrency {
				t.Errorf("Attributes[currency]: got %q, want %q", got, tc.wantCurrency)
			}
			if got := listing.Attributes["currency_status"]; got != tc.wantCurrencyStatus {
				t.Errorf("Attributes[currency_status]: got %q, want %q", got, tc.wantCurrencyStatus)
			}
			if got := listing.Attributes["price_local"]; got != tc.wantPriceLocal {
				t.Errorf("Attributes[price_local]: got %q, want %q", got, tc.wantPriceLocal)
			}
			if got := listing.Attributes["price_local_ccy"]; got != tc.wantPriceLocalCcy {
				t.Errorf("Attributes[price_local_ccy]: got %q, want %q", got, tc.wantPriceLocalCcy)
			}
		})
	}
}

// TestEURCentsToBGNWholeCeil verifies that price-filter conversion uses ceil
// rather than round so user budget ceilings are never clipped (XOL-41 M3-G).
//
// Illustrative case where ceil differs from round:
//   200 EUR = 20000 cents → 200 × 1.95583 = 391.166 BGN
//   round(391.166) = 391  — cuts off listings at 391–392 BGN
//   ceil(391.166)  = 392  — correct: passes all listings within user intent
//
// Expected values derived from math.Ceil(float64(eurCents)/100 * 1.95583):
//   0 cents   → 0
//   1 cent    → ceil(0.0195583) = 1
//   10000     → ceil(195.583)   = 196
//   20000     → ceil(391.166)   = 392  (round would give 391 — the "clipping" case)
//   50000     → ceil(977.915)   = 978
//   100000    → ceil(1955.83)   = 1956
func TestEURCentsToBGNWholeCeil(t *testing.T) {
	cases := []struct {
		name     string
		eurCents int
		wantBGN  int
	}{
		{"0 EUR", 0, 0},
		{"1 cent", 1, 1},
		{"100 EUR (10000 cents)", 10000, 196},
		{"200 EUR (20000 cents) — ceil>round", 20000, 392},
		{"500 EUR (50000 cents)", 50000, 978},
		{"1000 EUR (100000 cents)", 100000, 1956},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EURCentsToBGNWhole(tc.eurCents)
			if got != tc.wantBGN {
				t.Errorf("EURCentsToBGNWhole(%d): expected %d BGN, got %d", tc.eurCents, tc.wantBGN, got)
			}
		})
	}
}
