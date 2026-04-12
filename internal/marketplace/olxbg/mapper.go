package olxbg

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/TechXTT/marktbot/internal/models"
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
	ID    flexString `json:"id"`
	URL   string     `json:"url"`
	Title string     `json:"title"`
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

func mapListing(offer apiOffer) models.Listing {
	bgnStotinki, priceType := priceFromParams(offer.Params)
	eurCents := BGNStotinkiToEURCents(bgnStotinki)

	var imageURLs []string
	for _, photo := range offer.Photos {
		if photo.Link != "" {
			imageURLs = append(imageURLs, photo.Link)
		}
	}

	condition := conditionFromParams(offer.Params)
	city := offer.Location.City.Name

	return models.Listing{
		ItemID:        fmt.Sprintf("olxbg_%s", string(offer.ID)),
		CanonicalID:   fmt.Sprintf("olxbg:%s", string(offer.ID)),
		MarketplaceID: "olxbg",
		Title:         offer.Title,
		Price:         eurCents,
		PriceType:     priceType,
		Condition:     condition,
		URL:           offer.URL,
		ImageURLs:     imageURLs,
		Seller: models.Seller{
			Name: offer.Contact.Name,
		},
		Attributes: map[string]string{
			"city":            city,
			"currency":        "EUR",
			"price_local":     fmt.Sprintf("%d", bgnStotinki),
			"price_local_ccy": "BGN",
		},
	}
}

// priceFromParams extracts the raw price in BGN stotinki and the normalized
// price type from the offer's params array. OLX nests the price under a param
// with key "price" whose value carries the numeric amount plus negotiable/type
// hints. The caller is expected to convert to EUR cents before storing on the
// listing. Returns (0, "") when the price param is missing.
func priceFromParams(params []apiParam) (int, string) {
	for _, p := range params {
		if p.Key != "price" {
			continue
		}
		priceType := "fixed"
		switch {
		case p.Value.Negotiable:
			priceType = "negotiable"
		case p.Value.Type == "free":
			priceType = "free"
		case p.Value.Type == "exchange":
			priceType = "negotiable"
		}
		return parsePriceToCents(p.Value.Value), priceType
	}
	return 0, ""
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

// parsePriceToCents converts a BGN float value to stotinki (integer cents).
func parsePriceToCents(value float64) int {
	return int(value * 100)
}
