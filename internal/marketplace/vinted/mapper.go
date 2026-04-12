package vinted

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/TechXTT/marktbot/internal/models"
)

type apiItem struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Price      apiPrice `json:"price"`
	Currency   string   `json:"currency"`
	Status     string   `json:"status"`
	URL        string   `json:"url"`
	Photo      apiPhoto `json:"photo"`
	User       apiUser  `json:"user"`
	BrandTitle string   `json:"brand_title"`
}

type apiPrice struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currency_code"`
}

type apiPhoto struct {
	URL string `json:"url"`
}

type apiUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

func mapListing(item apiItem) models.Listing {
	priceCents := parsePrice(item.Price.Amount)
	return models.Listing{
		ItemID:        fmt.Sprintf("v%d", item.ID),
		CanonicalID:   fmt.Sprintf("vinted:%d", item.ID),
		MarketplaceID: "vinted",
		Title:         item.Title,
		Price:         priceCents,
		PriceType:     "fixed",
		Condition:     normalizeCondition(item.Status),
		URL:           item.URL,
		ImageURLs:     []string{item.Photo.URL},
		Seller: models.Seller{
			ID:   fmt.Sprintf("%d", item.User.ID),
			Name: item.User.Login,
		},
		Attributes: map[string]string{
			"brand": item.BrandTitle,
		},
	}
}

func normalizeCondition(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new_with_tags", "new_without_tags":
		return "new"
	case "very_good":
		return "like_new"
	case "good":
		return "good"
	case "satisfactory":
		return "fair"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func parsePrice(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int(parsed * 100)
}
