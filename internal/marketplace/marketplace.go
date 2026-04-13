package marketplace

import (
	"context"

	"github.com/TechXTT/xolto/internal/models"
)

type Marketplace interface {
	ID() string
	Name() string
	Search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error)
	ListingURL(itemID string) string
	SupportsMessaging() bool
}

type Messenger interface {
	Marketplace
	Init(ctx context.Context) error
	SendMessage(ctx context.Context, listing models.Listing, spec models.SearchSpec, offerPrice int) error
	Close()
}
