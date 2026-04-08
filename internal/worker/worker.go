package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

type UserWorker struct {
	specs         []models.SearchSpec
	db            store.Store
	registry      *marketplace.Registry
	scorer        *scorer.Scorer
	notifier      notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore      float64
}

// titleMatchesQuery returns false when a multi-token query has fewer than 2
// tokens present in the listing title, catching obvious category mismatches
// (e.g. a Sony phone surfaced by a "sony camera" search) before scoring.
func titleMatchesQuery(title, query string) bool {
	lower := strings.ToLower(title)
	tokens := strings.Fields(strings.ToLower(query))
	matches := 0
	for _, t := range tokens {
		if len(t) >= 2 && strings.Contains(lower, t) {
			matches++
		}
	}
	if len(tokens) <= 1 {
		return matches > 0
	}
	return matches >= 2
}

func (w *UserWorker) RunCycle(ctx context.Context) error {
	for _, spec := range w.specs {
		if !spec.Enabled {
			continue
		}
		if spec.ProfileID > 0 {
			mission, err := w.db.GetMission(spec.ProfileID)
			if err != nil {
				slog.Warn("failed to load mission for search", "search_id", spec.ID, "mission_id", spec.ProfileID, "error", err)
				continue
			}
			if mission == nil || mission.Status == "paused" || mission.Status == "completed" {
				continue
			}
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
			if !titleMatchesQuery(listing.Title, spec.Query) {
				continue
			}
			listing.ProfileID = spec.ProfileID
			if listing.Price > 0 {
				_ = w.db.RecordPrice(spec.Query, spec.CategoryID, listing.Price)
			}
			isNew, _ := w.db.IsNew(spec.UserID, listing.ItemID)
			prevScore, hadPrev, _ := w.db.GetListingScore(spec.UserID, listing.ItemID)
			scored := w.scorer.Score(ctx, listing, spec)
			_ = w.db.SaveListing(spec.UserID, listing, spec.Query, scored)

			crossed := !isNew && hadPrev && prevScore < w.minScore && scored.Score >= w.minScore
			if !isNew && !crossed {
				continue
			}
			if scored.Score < w.minScore || scored.OfferPrice <= 0 {
				continue
			}
			if w.notifier != nil {
				payload, _ := json.Marshal(map[string]any{
					"type":      "deal_found",
					"userID":    spec.UserID,
					"missionID": spec.ProfileID,
					"search":    spec.Name,
					"deal":      scored,
				})
				w.notifier.Publish(spec.UserID, string(payload))
			}
			if w.emailNotifier != nil && w.emailNotifier.Enabled() {
				if user, err := w.db.GetUserByID(spec.UserID); err == nil && user != nil && user.Email != "" {
					_ = w.emailNotifier.SendDealAlert(user.Email, listing, scored.Score)
				}
			}
			slog.Info("worker deal found", "user", spec.UserID, "title", listing.Title, "score", fmt.Sprintf("%.1f", scored.Score))
		}
	}
	return nil
}
