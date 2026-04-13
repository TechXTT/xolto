package olxbg

import (
	"context"
	"fmt"

	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
)

// Marketplace implements the OLX Bulgaria marketplace (olx.bg).
// Prices are stored in BGN stotinki (1 BGN = 100 stotinki).
type Marketplace struct {
	client *client
}

func New() *Marketplace {
	return &Marketplace{client: newClient()}
}

func (m *Marketplace) ID() string   { return "olxbg" }
func (m *Marketplace) Name() string { return "OLX Bulgaria" }

func (m *Marketplace) Search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	return m.client.search(ctx, spec)
}

func (m *Marketplace) ListingURL(itemID string) string {
	if itemID == "" {
		return ""
	}
	return fmt.Sprintf("https://www.olx.bg/obyava/-%s.html", itemID)
}

func (m *Marketplace) SupportsMessaging() bool { return false }

var _ marketplace.Marketplace = (*Marketplace)(nil)
