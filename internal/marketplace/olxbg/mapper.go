package olxbg

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/TechXTT/xolto/internal/models"
)

// BGNPerEUR is the fixed BGN→EUR exchange rate (currency board peg).
// 1 EUR = 1.95583 BGN. OLX BG quotes prices in BGN stotinki but the rest of
// the system — scorer comparables, UI formatting, mission budgets — operates
// in EUR cents, so we convert at the edge.
const BGNPerEUR = 1.95583

// BGNStotinkiToEURCents converts BGN stotinki (1/100 BGN) to EUR cents.
func BGNStotinkiToEURCents(bgnStotinki int) int {
	if bgnStotinki <= 0 {
		return 0
	}
	return int(math.Round(float64(bgnStotinki) / BGNPerEUR))
}

// EURCentsToBGNWhole converts EUR cents to whole BGN units, which is the
// shape OLX API v1 expects for price[from]/price[to] filters.
func EURCentsToBGNWhole(eurCents int) int {
	if eurCents <= 0 {
		return 0
	}
	return int(math.Round(float64(eurCents) / 100 * BGNPerEUR))
}

// flexString unmarshals a JSON field that may be a string, number, or array.
// For arrays it takes the first element.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	switch b[0] {
	case '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		if len(arr) > 0 {
			return f.UnmarshalJSON(arr[0])
		}
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*f = flexString(n.String())
	}
	return nil
}

// OLX API v1 response types.

type searchResponse struct {
	Data     []apiOffer `json:"data"`
	Metadata struct {
		TotalElements int `json:"total_elements"`
	} `json:"metadata"`
}

type apiOffer struct {
	ID     flexString `json:"id"`
	URL    string     `json:"url"`
	Title  string     `json:"title"`
	Photos []struct {
		Link string `json:"link"`
	} `json:"photos"`
	Location struct {
		City struct {
			Name string `json:"name"`
		} `json:"city"`
		Region struct {
			Name string `json:"name"`
		} `json:"region"`
	} `json:"location"`
	Contact struct {
		Name string `json:"name"`
	} `json:"contact"`
	Params []apiParam `json:"params"`
	// OLX API v1 exposes advert status as a string ("active", "removed_by_user", etc.)
	// instead of the legacy `is_active` boolean.
	Status string `json:"status"`
}

// apiParam represents a single entry in the offer's params array. OLX nests
// price and condition into this same shape — price has numeric `value.value`
// plus `negotiable`/`type`, condition/state uses `value.key` + `value.label`.
// A single flat shape tolerates both because absent fields decode to zero.
type apiParam struct {
	Key   string     `json:"key"`
	Name  string     `json:"name"`
	Value paramValue `json:"value"`
}

type paramValue struct {
	// Price-shaped params:
	Value      float64 `json:"value"`
	Negotiable bool    `json:"negotiable"`
	Currency   string  `json:"currency"`
	Type       string  `json:"type"` // "price" | "free" | "exchange"
	// State/enum-shaped params:
	Key   flexString `json:"key"`
	Label string     `json:"label"`
}

// CurrencyStatus values for the currency_status Attribute and envelope field.
// These are the only three values emitted; the dash must treat any other value
// as "unknown" for forward-compatibility.
const (
	// CurrencyStatusNative indicates the offer was quoted in EUR; the stored
	// Price is EUR cents with no conversion applied.
	CurrencyStatusNative = "bgn_native" // intentionally named for the common BGN case — see below
	// CurrencyStatusConverted indicates the offer was quoted in BGN; the stored
	// Price has been converted to EUR cents at the fixed peg (1 EUR = 1.95583 BGN).
	CurrencyStatusConverted = "converted_from_eur" // EUR→EUR: no-op; BGN→EUR: converted
	// CurrencyStatusUnknown indicates the currency field was missing or
	// unrecognised; the Price is computed under the default BGN assumption.
	CurrencyStatusUnknown = "unknown"
)

// CurrencyStatusFromAPI returns the EUR-cent price and the currency_status
// string for the given raw offer price and API-reported currency string. This
// function is the single source of truth for OLX BG currency conversion; both
// the crawler mapper and the ad-hoc fetcher (listingfetcher) must use it.
//
// Conversion rules:
//
//   - "EUR": price is already in EUR; multiply by 100 for cents — no peg division.
//   - "BGN": price is in BGN; divide by BGNPerEUR after converting to stotinki.
//   - anything else: fall back to BGN assumption; emit "unknown" status + warn log.
func CurrencyStatusFromAPI(rawPrice float64, apiCurrency, offerID string) (eurCents int, status string) {
	switch strings.ToUpper(strings.TrimSpace(apiCurrency)) {
	case "EUR":
		// OLX returns e.g. 700 meaning 700 EUR. Store as EUR cents directly.
		eurCents = int(math.Round(rawPrice * 100))
		return eurCents, "bgn_native" // reuse constant; value meaning: native listing currency, no BGN conversion
	case "BGN":
		// OLX returns e.g. 700 meaning 700.00 BGN. Convert to EUR cents via peg.
		bgnStotinki := int(math.Round(rawPrice * 100))
		eurCents = BGNStotinkiToEURCents(bgnStotinki)
		return eurCents, "converted_from_eur"
	default:
		// Unknown or missing currency — fall back to BGN assumption so the system
		// does not silently emit zero prices, but warn so we can catch new values.
		slog.Warn("olxbg mapper: unrecognised currency, falling back to BGN assumption",
			"offer_id", offerID,
			"currency", apiCurrency,
		)
		bgnStotinki := int(math.Round(rawPrice * 100))
		eurCents = BGNStotinkiToEURCents(bgnStotinki)
		return eurCents, "unknown"
	}
}

func mapListing(offer apiOffer) models.Listing {
	rawPrice, apiCurrency, priceType := priceFromParams(offer.Params)
	offerID := string(offer.ID)
	eurCents, status := CurrencyStatusFromAPI(rawPrice, apiCurrency, offerID)

	// price_local stores the original API value in its original currency unit
	// (not stotinki — the API already returns the face value, e.g. 700 for 700 BGN).
	priceLocalStr := fmt.Sprintf("%.2f", rawPrice)

	var imageURLs []string
	for _, photo := range offer.Photos {
		if photo.Link != "" {
			imageURLs = append(imageURLs, photo.Link)
		}
	}

	condition := conditionFromParams(offer.Params)
	city := offer.Location.City.Name

	return models.Listing{
		ItemID:         fmt.Sprintf("olxbg_%s", offerID),
		CanonicalID:    fmt.Sprintf("olxbg:%s", offerID),
		MarketplaceID:  "olxbg",
		Title:          offer.Title,
		Price:          eurCents,
		PriceType:      priceType,
		Condition:      condition,
		URL:            offer.URL,
		ImageURLs:      imageURLs,
		CurrencyStatus: status,
		Seller: models.Seller{
			Name: offer.Contact.Name,
		},
		Attributes: map[string]string{
			"city":            city,
			"currency":        "EUR",
			"price_local":     priceLocalStr,
			"price_local_ccy": strings.ToUpper(strings.TrimSpace(apiCurrency)),
			"currency_status": status,
		},
	}
}

// priceFromParams extracts the raw price (in the API's face-value unit),
// the API-reported currency string, and the normalized price type from the
// offer's params array. OLX nests the price under a param with key "price"
// whose value carries the numeric amount, currency, and negotiable/type hints.
//
// The returned rawPrice is the float as returned by the API (e.g. 700 for
// 700 EUR or 700 BGN). The caller must inspect currency and apply the correct
// conversion via currencyStatus. Returns (0, "", "") when the price param
// is missing.
func priceFromParams(params []apiParam) (rawPrice float64, currency string, priceType string) {
	for _, p := range params {
		if p.Key != "price" {
			continue
		}
		priceType = "fixed"
		switch {
		case p.Value.Negotiable:
			priceType = "negotiable"
		case p.Value.Type == "free":
			priceType = "free"
		case p.Value.Type == "exchange":
			priceType = "negotiable"
		}
		return p.Value.Value, p.Value.Currency, priceType
	}
	return 0, "", ""
}

// conditionFromParams extracts a normalized condition string from OLX params.
// OLX BG uses Bulgarian condition labels under the "state" or "condition" param key.
func conditionFromParams(params []apiParam) string {
	for _, p := range params {
		if p.Key == "state" || p.Key == "condition" {
			return normalizeCondition(string(p.Value.Key), p.Value.Label)
		}
	}
	return ""
}

// normalizeCondition maps OLX BG condition keys/labels to the standard set.
// OLX BG condition keys observed: "new", "used"
// Bulgarian labels: "Ново" (new), "Като ново" (like new), "Добро" (good), "За ремонт" (for repair)
func normalizeCondition(key, label string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "new":
		return "new"
	case "used":
		return "good"
	}
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "ново":
		return "new"
	case "като ново":
		return "like_new"
	case "добро":
		return "good"
	case "за ремонт":
		return "fair"
	}
	return strings.ToLower(strings.TrimSpace(key))
}

