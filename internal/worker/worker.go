package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

type UserWorker struct {
	specs    []models.SearchSpec
	db       store.Store
	registry *marketplace.Registry
	scorer   *scorer.Scorer
	notifier notify.Dispatcher
	minScore float64
}

func (w *UserWorker) RunCycle(ctx context.Context) error {
	for _, spec := range w.specs {
		if !spec.Enabled {
			continue
		}
		mp, ok := w.registry.Get(spec.MarketplaceID)
		if !ok {
			slog.Warn("unknown marketplace", "marketplace", spec.MarketplaceID)
			continue
		}
		listings, err := mp.Search(ctx, spec)
		if err != nil {
			slog.Warn("worker search failed", "marketplace", spec.MarketplaceID, "query", spec.Query, "error", err)
			continue
		}
		for _, listing := range listings {
			if listing.Price > 0 {
				_ = w.db.RecordPrice(spec.Query, spec.CategoryID, listing.Price)
			}
			isNew, _ := w.db.IsNew(spec.UserID, listing.ItemID)
			prevScore, hadPrev, _ := w.db.GetListingScore(spec.UserID, listing.ItemID)
			scored := w.scorer.Score(ctx, listing, spec)
			_ = w.db.SaveListing(spec.UserID, listing, spec.Query, scored.Score)

			crossed := !isNew && hadPrev && prevScore < w.minScore && scored.Score >= w.minScore
			if !isNew && !crossed {
				continue
			}
			if scored.Score < w.minScore || scored.OfferPrice <= 0 {
				continue
			}
			if w.notifier != nil {
				payload, _ := json.Marshal(map[string]any{
					"type":   "deal_found",
					"userID": spec.UserID,
					"search": spec.Name,
					"deal":   scored,
				})
				w.notifier.Publish(spec.UserID, string(payload))
			}
			slog.Info("worker deal found", "user", spec.UserID, "title", listing.Title, "score", fmt.Sprintf("%.1f", scored.Score))
		}
	}
	return nil
}
