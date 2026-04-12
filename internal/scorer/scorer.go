package scorer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/format"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/reasoner"
	"github.com/TechXTT/marktbot/internal/store"
)

const minOfferCents = 1000 // EUR10 minimum offer

type Scorer struct {
	store      store.Reader
	scoringCfg struct {
		MinScore         float64
		MarketSampleSize int
	}
	reasoner *reasoner.Reasoner
}

func (sc *Scorer) shouldSkipLLM(listing models.Listing, search models.SearchSpec, heuristic models.DealAnalysis) bool {
	if search.MaxPrice > 0 && listing.Price > search.MaxPrice*3/2 {
		return true
	}
	if heuristic.Confidence >= 0.70 && heuristic.FairPrice > 0 {
		ratio := float64(listing.Price) / float64(heuristic.FairPrice)
		score := clamp(10.0-10.0*ratio+5.0, 1, 10)
		if score < 3.0 {
			return true
		}
	}
	return false
}

func New(s store.Reader, cfg interface {
	GetMinScore() float64
	GetMarketSampleSize() int
}, rsn *reasoner.Reasoner) *Scorer {
	return &Scorer{
		store: s,
		scoringCfg: struct {
			MinScore         float64
			MarketSampleSize int
		}{
			MinScore:         cfg.GetMinScore(),
			MarketSampleSize: cfg.GetMarketSampleSize(),
		},
		reasoner: rsn,
	}
}

// Score evaluates a listing and returns a ScoredListing with score and offer price.
func (sc *Scorer) Score(ctx context.Context, listing models.Listing, search models.SearchSpec) models.ScoredListing {
	var score float64
	var reason strings.Builder

	if !hasActionablePrice(listing) {
		reason.WriteString("listing has no actionable asking price")
		if listing.PriceType != "" {
			reason.WriteString(" (")
			reason.WriteString(listing.PriceType)
			reason.WriteString(")")
		}

		return models.ScoredListing{
			Listing:         listing,
			Score:           1.0,
			OfferPrice:      0,
			FairPrice:       0,
			MarketAverage:   0,
			Confidence:      0,
			Reason:          reason.String(),
			ReasoningSource: "rule",
		}
	}

	marketAvg, hasMarket, err := sc.store.GetMarketAverage(search.Query, search.CategoryID, sc.scoringCfg.MarketSampleSize)
	if err != nil {
		slog.Warn("failed to load market average", "query", search.Query, "error", err)
	}

	comparables, err := sc.store.GetComparableDeals(search.UserID, search.Query, listing.ItemID, 50)
	if err != nil {
		slog.Warn("failed to load comparable deals", "item", listing.ItemID, "error", err)
	}

	// Prepend user-approved matches from this mission. They're ground-truth
	// relevant items, so they anchor both fair-price calibration and the
	// reasoner's relevance judgement. Deduped by ItemID against `comparables`
	// so an already-seen listing doesn't appear twice.
	if search.ProfileID > 0 {
		approved, aerr := sc.store.GetApprovedComparables(search.UserID, search.ProfileID, 10)
		if aerr != nil {
			slog.Warn("failed to load approved comparables", "mission", search.ProfileID, "error", aerr)
		} else if len(approved) > 0 {
			seen := make(map[string]bool, len(comparables))
			for _, c := range comparables {
				seen[c.ItemID] = true
			}
			merged := make([]models.ComparableDeal, 0, len(approved)+len(comparables))
			for _, a := range approved {
				if a.ItemID == listing.ItemID || seen[a.ItemID] {
					continue
				}
				merged = append(merged, a)
			}
			comparables = append(merged, comparables...)
		}
	}

	analysis := models.DealAnalysis{
		FairPrice:  marketAvg,
		Confidence: 0.35,
		Reason:     "market-average fallback",
		Source:     "heuristic",
	}
	if sc.reasoner != nil {
		heuristic := sc.reasoner.HeuristicAnalysis(listing, search, marketAvg, comparables)
		analysis = heuristic
		if sc.shouldSkipLLM(listing, search, heuristic) {
			analysis.Source = "prefilter"
		} else {
			analysis, err = sc.reasoner.Analyze(ctx, listing, search, marketAvg, comparables)
			if err != nil {
				analysis = heuristic
				slog.Warn("ai reasoning failed, using heuristic fallback", "item", listing.ItemID, "error", err)
			}
		}
	}

	referencePrice := analysis.FairPrice
	if referencePrice <= 0 && hasMarket {
		referencePrice = marketAvg
	}

	if referencePrice > 0 {
		ratio := float64(listing.Price) / float64(referencePrice)
		score = clamp(10.0-10.0*ratio+5.0, 1, 10)
		reason.WriteString(fmt.Sprintf("%.0f%% of fair value (%s)", ratio*100, format.Euro(referencePrice)))
	} else if search.MaxPrice > 0 {
		ratio := float64(listing.Price) / float64(search.MaxPrice)
		score = clamp(10.0-8.0*ratio, 1, 10)
		reason.WriteString(fmt.Sprintf("%.0f%% of max budget", ratio*100))
	} else {
		score = 5.0
		reason.WriteString("no market data")
	}

	if analysis.Confidence >= 0.75 {
		score += 0.4
		reason.WriteString(", strong comparable confidence")
	} else if analysis.Confidence < 0.4 {
		score -= 0.3
		reason.WriteString(", weak comparable confidence")
	}

	if listing.PriceType == "negotiable" {
		score += 1.0
		reason.WriteString(", negotiable")
	}

	if !listing.Date.IsZero() && time.Since(listing.Date) < time.Hour {
		score += 0.5
		reason.WriteString(", fresh listing")
	}

	switch strings.ToLower(listing.Condition) {
	case "like_new":
		score += 0.5
		reason.WriteString(", like new")
	case "new":
		score += 0.5
		reason.WriteString(", new condition")
	}

	score = clamp(score, 1, 10)

	// AI explicitly judged the listing as irrelevant to the search query.
	if analysis.Source == "ai" && !analysis.Relevant {
		return models.ScoredListing{
			Listing:         listing,
			Score:           1.0,
			OfferPrice:      0,
			Reason:          "not relevant to search: " + analysis.Reason,
			ReasoningSource: "ai",
		}
	}

	riskFlags := computeRiskFlags(listing, analysis.FairPrice)
	offerPrice := calculateOffer(listing.Price, analysis.FairPrice, marketAvg, hasMarket, search.OfferPercentage)

	if analysis.Reason != "" {
		reason.WriteString(" | ")
		reason.WriteString(analysis.Reason)
	}

	return models.ScoredListing{
		Listing:         listing,
		Score:           score,
		OfferPrice:      offerPrice,
		FairPrice:       analysis.FairPrice,
		MarketAverage:   marketAvg,
		Confidence:      analysis.Confidence,
		Reason:          reason.String(),
		ReasoningSource: analysis.Source,
		SearchAdvice:    analysis.SearchAdvice,
		ComparableDeals: analysis.ComparableDeals,
		RiskFlags:       riskFlags,
	}
}

func calculateOffer(askingPrice, fairPrice, marketAvg int, hasMarket bool, offerPct int) int {
	base := askingPrice
	if fairPrice > 0 {
		base = fairPrice
	} else if hasMarket && marketAvg > 0 {
		base = marketAvg
	}

	offer := base * offerPct / 100
	if offer < minOfferCents {
		offer = minOfferCents
	}
	if offer > askingPrice {
		offer = askingPrice
	}
	return offer
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func hasActionablePrice(listing models.Listing) bool {
	if listing.Price <= 0 {
		return false
	}

	switch listing.PriceType {
	case "reserved", "see-description", "exchange", "free":
		return false
	default:
		return true
	}
}

// computeRiskFlags returns trust-signal flags for a listing.
func computeRiskFlags(listing models.Listing, fairPrice int) []string {
	var flags []string
	lower := strings.ToLower(listing.Title + " " + listing.Description)
	electronics := isElectronicsListing(lower)

	if fairPrice > 0 && listing.Price > 0 && listing.Price < fairPrice/2 {
		flags = append(flags, "anomaly_price")
	}

	vagueTerms := []string{"as is", "as-is", "untested", "for parts", "sold as seen", "no returns", "working condition", "not working"}
	for _, term := range vagueTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "vague_condition")
			break
		}
	}

	bundleTerms := []string{"bundle", " lot ", "complete set", "collection"}
	for _, term := range bundleTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "unclear_bundle")
			break
		}
	}

	if electronics {
		hasDigit := false
		for _, c := range listing.Title {
			if c >= '0' && c <= '9' {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			flags = append(flags, "no_model_id")
		}
	}

	if electronics && len(listing.ImageURLs) < 3 {
		flags = append(flags, "missing_key_photos")
	}

	if electronics && isPhoneOrLaptop(lower) && !hasBatteryHealthSignal(lower) {
		flags = append(flags, "no_battery_health")
	}

	if hasRefurbishedAmbiguity(lower) {
		flags = append(flags, "refurbished_ambiguity")
	}

	return flags
}

func isElectronicsListing(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		"camera", "lens", "laptop", "macbook", "iphone", "ipad", "samsung", "pixel",
		"sony", "nikon", "canon", "fuji", "fujifilm", "gpu", "cpu", "graphics card",
		"smartphone", "tablet", "notebook", "thinkpad", "surface", "playstation", "xbox", "nintendo",
		"monitor", "television", "tv", "router", "modem", "headphone", "airpods", "charger", "battery",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isPhoneOrLaptop(text string) bool {
	return containsAny(text,
		"iphone", "samsung", "pixel", "oneplus", "smartphone", "phone",
		"laptop", "macbook", "notebook", "thinkpad", "surface",
	)
}

func hasBatteryHealthSignal(text string) bool {
	return containsAny(text,
		"battery health", "battery capaciteit", "battery capacity", "accu", "accuprestatie",
		"cycle count", "battery cycles", "batterijconditie", "battery condition",
	)
}

func hasRefurbishedAmbiguity(text string) bool {
	if !containsAny(text, "refurb", "renewed", "reconditioned", "gereviseerd") {
		return false
	}
	if containsAny(text, "grade a", "grade b", "grade c", "warranty", "garantie", "12 month", "24 month") {
		return false
	}
	return true
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
