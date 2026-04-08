package olxbg

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TechXTT/marktbot/internal/models"
)

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
	URL   string `json:"url"`
	Title string `json:"title"`
	Price struct {
		Value struct {
			Value         float64 `json:"value"`
			PriceForWhole float64 `json:"price_for_whole"`
		} `json:"value"`
		Negotiable bool   `json:"negotiable"`
		Currency   string `json:"currency"`
		Type       string `json:"type"` // "price" | "free" | "exchange" | "negotiable"
	} `json:"price"`
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
	Params []struct {
		Key   string `json:"key"`
		Name  string `json:"name"`
		Value struct {
			Key   flexString `json:"key"`
			Label string     `json:"label"`
		} `json:"value"`
	} `json:"params"`
	IsActive bool `json:"is_active"`
}

func mapListing(offer apiOffer) models.Listing {
	priceCents := parsePriceToCents(offer.Price.Value.Value)
	priceType := "fixed"
	if offer.Price.Negotiable {
		priceType = "negotiable"
	} else if offer.Price.Type == "free" {
		priceType = "free"
	} else if offer.Price.Type == "exchange" {
		priceType = "negotiable"
	}

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
		Price:         priceCents,
		PriceType:     priceType,
		Condition:     condition,
		URL:           offer.URL,
		ImageURLs:     imageURLs,
		Seller: models.Seller{
			Name: offer.Contact.Name,
		},
		Attributes: map[string]string{
			"city":     city,
			"currency": "BGN",
		},
	}
}

// conditionFromParams extracts a normalized condition string from OLX params.
// OLX BG uses Bulgarian condition labels under the "state" or "condition" param key.
func conditionFromParams(params []struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Value struct {
		Key   flexString `json:"key"`
		Label string     `json:"label"`
	} `json:"value"`
}) string {
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
