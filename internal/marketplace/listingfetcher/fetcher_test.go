package listingfetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// buildOLXOfferPayload returns a minimal OLX BG API v1 /offers/{id}/ JSON
// response body with the given price and currency.
func buildOLXOfferPayload(id, title string, price float64, currency string) []byte {
	payload := map[string]any{
		"data": map[string]any{
			"id":    id,
			"url":   fmt.Sprintf("https://www.olx.bg/ad/test-%s.html", id),
			"title": title,
			"params": []map[string]any{
				{
					"key": "price",
					"value": map[string]any{
						"value":      price,
						"currency":   currency,
						"type":       "price",
						"negotiable": false,
					},
				},
			},
			"photos": []map[string]any{
				{"link": "https://img.olxcdn.com/test.jpg"},
			},
			"description": "test description",
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

// TestFetchOLXByIDEURListing verifies that a EUR-quoted listing fetched via
// the direct OLX API path is NOT divided by 1.95583. A 700 EUR listing must
// arrive as 70000 EUR cents (XOL-31 AC).
func TestFetchOLXByIDEURListing(t *testing.T) {
	// Use a numeric OLX offer ID (the API returns id as a JSON number).
	payload := buildOLXOfferPayload("98765001", "Canon EOS R10 body + 18-45", 700, "EUR")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	f := New()
	f.http.Transport = &redirectAllTransport{target: srv.URL}

	// The URL must contain a numeric id segment that olxBGIDRe can extract.
	listing, err := f.fetchOLXByID(context.Background(), "https://www.olx.bg/ad/canon-eos-r10/98765001/")
	if err != nil {
		t.Fatalf("fetchOLXByID() error = %v", err)
	}

	// 700 EUR → 70000 EUR cents (no division by 1.95583)
	wantCents := 70000
	if listing.Price != wantCents {
		t.Errorf("EUR listing: expected %d cents, got %d (%.1f%% off)",
			wantCents, listing.Price,
			float64(absInt(listing.Price-wantCents))/float64(wantCents)*100,
		)
	}
	if got := listing.Attributes["currency_status"]; got != "bgn_native" {
		t.Errorf("expected currency_status=bgn_native, got %q", got)
	}
}

// TestFetchOLXByIDBGNListing verifies that a BGN-quoted listing is correctly
// converted to EUR cents on the analyze path.
func TestFetchOLXByIDBGNListing(t *testing.T) {
	payload := buildOLXOfferPayload("98765002", "MacBook Air 2019 i5", 390, "BGN")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	f := New()
	f.http.Transport = &redirectAllTransport{target: srv.URL}

	listing, err := f.fetchOLXByID(context.Background(), "https://www.olx.bg/ad/macbook/98765002/")
	if err != nil {
		t.Fatalf("fetchOLXByID() error = %v", err)
	}

	// 390 BGN → 390/1.95583 EUR ≈ 199.40 EUR → ~19940 cents
	if listing.Price <= 0 {
		t.Fatalf("BGN listing: expected non-zero price, got %d", listing.Price)
	}
	if listing.Price < 19800 || listing.Price > 20100 {
		t.Errorf("BGN listing: expected ~19940 cents, got %d", listing.Price)
	}
	if got := listing.Attributes["currency_status"]; got != "converted_from_eur" {
		t.Errorf("expected currency_status=converted_from_eur, got %q", got)
	}
}

// redirectAllTransport redirects every HTTP request to a fixed test server URL,
// preserving the original path, query, and method. This lets us intercept the
// hardcoded olx.bg API calls inside fetchOLXByID without modifying production
// URL logic.
type redirectAllTransport struct {
	target string
}

func (t *redirectAllTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = targetURL.Scheme
	cloned.URL.Host = targetURL.Host
	cloned.Host = targetURL.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
