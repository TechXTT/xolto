package vinted

import (
	"context"
	"fmt"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
)

type Marketplace struct {
	client *client
}

func New() *Marketplace {
	return &Marketplace{client: newClient()}
}

func (m *Marketplace) ID() string {
	return "vinted"
}

func (m *Marketplace) Name() string {
	return "Vinted"
}

func (m *Marketplace) Search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	return m.client.search(ctx, spec)
}

func (m *Marketplace) ListingURL(itemID string) string {
	if itemID == "" {
		return ""
	}
	return fmt.Sprintf("https://www.vinted.nl/items/%s", itemID)
}

func (m *Marketplace) SupportsMessaging() bool {
	return false
}

var _ marketplace.Marketplace = (*Marketplace)(nil)
