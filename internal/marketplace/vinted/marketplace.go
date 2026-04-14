package vinted

import (
	"context"
	"fmt"

	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
)

type Marketplace struct {
	cfg    Config
	client *client
}

func New(cfg Config) *Marketplace {
	return &Marketplace{cfg: cfg, client: newClient(cfg)}
}

func NetherlandsConfig() Config {
	return Config{
		ID:             "vinted_nl",
		Name:           "Vinted NL",
		CountryCode:    "NL",
		HomeURL:        "https://www.vinted.nl/",
		BaseURL:        "https://www.vinted.nl/api/v2/catalog/items",
		Referer:        "https://www.vinted.nl/",
		AcceptLanguage: "nl-NL,nl;q=0.9,en;q=0.8",
		ListingBaseURL: "https://www.vinted.nl/items/",
	}
}

func DenmarkConfig() Config {
	return Config{
		ID:             "vinted_dk",
		Name:           "Vinted DK",
		CountryCode:    "DK",
		HomeURL:        "https://www.vinted.dk/",
		BaseURL:        "https://www.vinted.dk/api/v2/catalog/items",
		Referer:        "https://www.vinted.dk/",
		AcceptLanguage: "da-DK,da;q=0.9,en;q=0.8",
		ListingBaseURL: "https://www.vinted.dk/items/",
	}
}

func (m *Marketplace) ID() string {
	return m.cfg.ID
}

func (m *Marketplace) Name() string {
	return m.cfg.Name
}

func (m *Marketplace) Search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	return m.client.search(ctx, spec)
}

func (m *Marketplace) ListingURL(itemID string) string {
	if itemID == "" {
		return ""
	}
	return fmt.Sprintf("%s%s", m.cfg.ListingBaseURL, itemID)
}

func (m *Marketplace) SupportsMessaging() bool {
	return false
}

var _ marketplace.Marketplace = (*Marketplace)(nil)
