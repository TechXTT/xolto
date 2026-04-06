package marktplaats

import (
	"context"
	"fmt"
	"time"

	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"golang.org/x/time/rate"
	"net/http"
)

type Marketplace struct {
	client *httpClient
}

func New(mpCfg config.MarktplaatsConfig) *Marketplace {
	rps := 1.0
	if mpCfg.RequestDelay > 0 {
		rps = 1.0 / mpCfg.RequestDelay.Seconds()
	}
	return &Marketplace{client: &httpClient{
		http:     &http.Client{Timeout: 30 * time.Second},
		limiter:  rate.NewLimiter(rate.Limit(rps), 1),
		zipCode:  mpCfg.ZipCode,
		distance: mpCfg.Distance,
	}}
}

func (m *Marketplace) ID() string {
	return "marktplaats"
}

func (m *Marketplace) Name() string {
	return "Marktplaats"
}

func (m *Marketplace) Search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	return m.client.search(ctx, spec)
}

func (m *Marketplace) ListingURL(itemID string) string {
	if itemID == "" {
		return ""
	}
	return fmt.Sprintf("https://www.marktplaats.nl/v/%s", itemID)
}

func (m *Marketplace) SupportsMessaging() bool {
	return false
}

var _ marketplace.Marketplace = (*Marketplace)(nil)
