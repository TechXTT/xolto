package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/notify"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

// wordTokenRe extracts alphanumeric runs as discrete tokens. We intentionally
// split on non-alphanumeric characters (including hyphens) so that OEM part
// numbers like "52960-A6000" don't let a search for "a6000" bleed into unrelated
// categories via substring matching.
var wordTokenRe = regexp.MustCompile(`[a-z0-9]+`)

// queryStopWords are generic words or natural-language price qualifiers that
// should not count toward relevance scoring. They frequently appear in mission
// target queries like "sony a6000 under 500" but carry no product signal.
var queryStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"a": true, "an": true, "of": true, "to": true, "in": true, "on": true,
	"at": true, "is": true, "it": true, "or": true, "by": true,
	"under": true, "below": true, "less": true, "than": true, "max": true,
	"maximum": true, "up": true, "until": true, "upto": true,
	"above": true, "over": true, "more": true, "min": true, "minimum": true,
	"eur": true, "euro": true, "euros": true, "usd": true,
	"used": true, "new": true, "good": true, "like": true, "mint": true,
	"cheap": true, "deal": true, "wanted": true, "buy": true, "buying": true,
}

type UserWorker struct {
	specs         []models.SearchSpec
	db            store.Store
	registry      *marketplace.Registry
	scorer        *scorer.Scorer
	notifier      notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore      float64
}

// titleMatchesQuery decides whether a listing title plausibly refers to the
// same product category as the query. It tokenizes both sides on word
// boundaries (treating hyphens as separators), drops stopwords and pure numeric
// tokens, and requires EVERY meaningful query token to appear as a discrete
// token in the title. This prevents false matches like the query "sony a6000"
// latching onto a Hyundai wheel-cap title "…52960-A6000 5 gaats met schade"
// just because the OEM part number happens to contain "A6000" as a substring.
func titleMatchesQuery(title, query string) bool {
	titleTokens := tokenizeWords(title)
	queryTokens := meaningfulQueryTokens(query)

	if len(queryTokens) == 0 {
		// Fall back to a permissive match if the query had nothing but
		// stopwords/numbers — otherwise we'd drop every listing.
		return true
	}

	for _, qt := range queryTokens {
		if _, ok := titleTokens[qt]; !ok {
			return false
		}
	}
	return true
}

func tokenizeWords(s string) map[string]struct{} {
	matches := wordTokenRe.FindAllString(strings.ToLower(s), -1)
	out := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		out[m] = struct{}{}
	}
	return out
}

func meaningfulQueryTokens(query string) []string {
	raw := wordTokenRe.FindAllString(strings.ToLower(query), -1)
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, tok := range raw {
		if queryStopWords[tok] {
			continue
		}
		if len(tok) < 2 {
			continue
		}
		// Drop pure numeric tokens — price hints like "500" in "under 500"
		// shouldn't be treated as product identifiers. Alphanumeric tokens
		// like "a6000" or "rtx3080" remain distinctive model identifiers.
		if isAllDigits(tok) {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
			if !isNew {
				storedPrice, storedSource, found, err := w.db.GetListingScoringState(spec.UserID, listing.ItemID)
				if err != nil {
					slog.Warn("failed to load listing scoring state", "item", listing.ItemID, "error", err)
				} else if found && storedPrice == listing.Price && storedSource == "ai" {
					if err := w.db.TouchListing(spec.UserID, listing.ItemID); err != nil {
						slog.Warn("failed to touch cached listing", "item", listing.ItemID, "error", err)
					}
					continue
				}
			}
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
