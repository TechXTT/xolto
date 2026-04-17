package olxbg

import (
	"math"
	"testing"
)

// TestCurrencyStatusEUR verifies that a EUR-quoted offer is stored as EUR cents
// with no BGN conversion applied. A 1000 EUR listing must NOT be divided by
// 1.95583 — it must land as 100000 EUR cents (within ±1% of truth per XOL-30 AC).
func TestCurrencyStatusEUR(t *testing.T) {
	eurCents, status := currencyStatus(1000.0, "EUR", "test-offer-1")
	if status != "bgn_native" {
		t.Errorf("expected status bgn_native, got %q", status)
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
	eurCents, status := currencyStatus(700.0, "BGN", "test-offer-2")
	if status != "converted_from_eur" {
		t.Errorf("expected status converted_from_eur, got %q", status)
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
	eurCents, status := currencyStatus(500.0, "", "test-offer-3")
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
	_, status := currencyStatus(200.0, "USD", "test-offer-4")
	if status != "unknown" {
		t.Errorf("expected status unknown for USD, got %q", status)
	}
}

// TestMapListingEURPrice verifies the end-to-end mapping path: a EUR-quoted
// offer must emerge with the correct EUR-cents price and currency_status
// attribute set to "bgn_native" (no BGN conversion).
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
	if got := listing.Attributes["currency_status"]; got != "bgn_native" {
		t.Errorf("expected currency_status=bgn_native, got %q", got)
	}
}

// TestMapListingBGNPrice verifies that a BGN-quoted offer is correctly
// converted to EUR cents and tagged with currency_status=converted_from_eur.
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
	if got := listing.Attributes["currency_status"]; got != "converted_from_eur" {
		t.Errorf("expected currency_status=converted_from_eur, got %q", got)
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
			eurCents, _ := currencyStatus(tc.quotedEUR, "EUR", "test")
			deltaFrac := math.Abs(float64(eurCents-tc.wantCents)) / float64(tc.wantCents)
			if deltaFrac > 0.01 {
				t.Errorf("%s: got %d cents, want %d cents (%.2f%% off, limit 1%%)",
					tc.name, eurCents, tc.wantCents, deltaFrac*100)
			}
		})
	}
}
