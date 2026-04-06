package reasoner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/format"
	"github.com/TechXTT/marktbot/internal/models"
)

type Reasoner struct {
	cfg    config.AIConfig
	client *http.Client
}

func New(cfg config.AIConfig) *Reasoner {
	return &Reasoner{
		cfg: cfg,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (r *Reasoner) Enabled() bool {
	return r.cfg.Enabled && r.cfg.APIKey != "" && r.cfg.Model != ""
}

func (r *Reasoner) Analyze(
	ctx context.Context,
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
) (models.DealAnalysis, error) {
	ranked := r.rankComparables(listing, comparables)
	heuristic := r.heuristicAnalysis(listing, search, marketAvg, ranked)

	if !r.Enabled() {
		return heuristic, nil
	}

	llmAnalysis, err := r.callLLM(ctx, listing, search, marketAvg, ranked)
	if err != nil {
		return heuristic, err
	}

	if llmAnalysis.FairPrice <= 0 {
		llmAnalysis.FairPrice = heuristic.FairPrice
	}
	if llmAnalysis.Confidence <= 0 {
		llmAnalysis.Confidence = heuristic.Confidence
	}
	if llmAnalysis.Reason == "" {
		llmAnalysis.Reason = heuristic.Reason
	}
	if len(llmAnalysis.ComparableDeals) == 0 {
		llmAnalysis.ComparableDeals = heuristic.ComparableDeals
	}
	if llmAnalysis.SearchAdvice == "" {
		llmAnalysis.SearchAdvice = heuristic.SearchAdvice
	}
	llmAnalysis.Source = "ai"

	return llmAnalysis, nil
}

func (r *Reasoner) heuristicAnalysis(
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
) models.DealAnalysis {
	fairPrice := marketAvg
	confidence := 0.35
	reason := "using recent market average"

	if len(comparables) > 0 {
		totalWeight := 0.0
		weightedPrice := 0.0
		for _, comp := range comparables {
			weight := math.Max(0.2, comp.Similarity)
			weightedPrice += float64(comp.Price) * weight
			totalWeight += weight
		}
		if totalWeight > 0 {
			fairPrice = int(weightedPrice / totalWeight)
		}
		avgSimilarity := averageSimilarity(comparables)
		confidence = math.Min(0.95, 0.45+0.08*float64(len(comparables))+0.25*avgSimilarity)
		reason = fmt.Sprintf(
			"%d similar listings suggest fair value around %s",
			len(comparables),
			format.Euro(fairPrice),
		)
	} else if marketAvg > 0 {
		confidence = 0.50
		reason = fmt.Sprintf("recent market average is %s", format.Euro(marketAvg))
	}

	if fairPrice <= 0 {
		fairPrice = listing.Price
		reason = "not enough comparable data yet"
	}

	return models.DealAnalysis{
		FairPrice:       fairPrice,
		Confidence:      clamp(confidence, 0.05, 0.99),
		Reason:          reason,
		Source:          "heuristic",
		ComparableDeals: comparables,
		SearchAdvice:    heuristicSearchAdvice(listing, search, comparables),
	}
}

func (r *Reasoner) rankComparables(listing models.Listing, comparables []models.ComparableDeal) []models.ComparableDeal {
	titleTokens := tokenSet(listing.Title + " " + listing.Description)
	ranked := make([]models.ComparableDeal, 0, len(comparables))

	for _, comp := range comparables {
		compTokens := tokenSet(comp.Title)
		similarity := jaccard(titleTokens, compTokens)
		priceDistance := 1.0
		if listing.Price > 0 {
			priceDistance = math.Abs(float64(comp.Price-listing.Price)) / float64(listing.Price)
		}

		if similarity == 0 && priceDistance > 0.35 {
			continue
		}

		comp.Similarity = clamp((similarity*0.75)+(math.Max(0, 1-priceDistance)*0.25), 0, 1)
		comp.MatchReason = comparableReason(similarity, priceDistance)
		ranked = append(ranked, comp)
	}

	slices.SortFunc(ranked, func(a, b models.ComparableDeal) int {
		if a.Similarity == b.Similarity {
			switch {
			case a.Price < b.Price:
				return -1
			case a.Price > b.Price:
				return 1
			default:
				return 0
			}
		}
		if a.Similarity > b.Similarity {
			return -1
		}
		return 1
	})

	if len(ranked) > r.cfg.MaxComparables && r.cfg.MaxComparables > 0 {
		ranked = ranked[:r.cfg.MaxComparables]
	}

	return ranked
}

func (r *Reasoner) callLLM(
	ctx context.Context,
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
) (models.DealAnalysis, error) {
	payload := chatCompletionRequest{
		Model:       r.cfg.Model,
		Temperature: r.cfg.Temperature,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "You analyze Dutch marketplace listings for resale/value hunting. Reply with strict JSON only.",
			},
			{
				Role:    "user",
				Content: buildPrompt(listing, search, marketAvg, comparables, r.cfg.SearchAdvice),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return models.DealAnalysis{}, fmt.Errorf("marshal ai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return models.DealAnalysis{}, fmt.Errorf("build ai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return models.DealAnalysis{}, fmt.Errorf("call ai provider: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return models.DealAnalysis{}, fmt.Errorf("ai provider returned status %d", resp.StatusCode)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return models.DealAnalysis{}, fmt.Errorf("decode ai response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return models.DealAnalysis{}, fmt.Errorf("ai response contained no choices")
	}

	content := extractJSON(completion.Choices[0].Message.Content)
	var parsed aiListingAnalysis
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return models.DealAnalysis{}, fmt.Errorf("parse ai json: %w", err)
	}

	selected := make([]models.ComparableDeal, 0, len(parsed.ComparableIndexes))
	for _, idx := range parsed.ComparableIndexes {
		if idx >= 0 && idx < len(comparables) {
			selected = append(selected, comparables[idx])
		}
	}
	if len(selected) == 0 {
		selected = comparables
	}

	return models.DealAnalysis{
		FairPrice:       parsed.FairPriceCents,
		Confidence:      clamp(parsed.Confidence, 0.05, 0.99),
		Reason:          strings.TrimSpace(parsed.Reasoning),
		Source:          "ai",
		ComparableDeals: selected,
		SearchAdvice:    strings.TrimSpace(parsed.SearchAdvice),
	}, nil
}

func buildPrompt(
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
	includeAdvice bool,
) string {
	type comparableInput struct {
		Index      int     `json:"index"`
		Title      string  `json:"title"`
		PriceCents int     `json:"price_cents"`
		Similarity float64 `json:"similarity"`
		Reason     string  `json:"reason"`
	}

	input := struct {
		Listing          models.Listing    `json:"listing"`
		Search           models.SearchSpec `json:"search"`
		MarketAvgCents   int               `json:"market_avg_cents"`
		NeedSearchAdvice bool              `json:"need_search_advice"`
		Comparables      []comparableInput `json:"comparables"`
	}{
		Listing:          listing,
		Search:           search,
		MarketAvgCents:   marketAvg,
		NeedSearchAdvice: includeAdvice,
		Comparables:      make([]comparableInput, 0, len(comparables)),
	}

	for i, comp := range comparables {
		input.Comparables = append(input.Comparables, comparableInput{
			Index:      i,
			Title:      comp.Title,
			PriceCents: comp.Price,
			Similarity: comp.Similarity,
			Reason:     comp.MatchReason,
		})
	}

	raw, _ := json.Marshal(input)
	return strings.Join([]string{
		"Analyze this marketplace listing against the provided comparables.",
		"Return JSON with this shape only:",
		`{"fair_price_cents":12345,"confidence":0.72,"reasoning":"short explanation","search_advice":"optional search refinement advice","comparable_indexes":[0,2]}`,
		"Confidence must be between 0 and 1. Use comparable_indexes for the strongest matches only.",
		string(raw),
	}, "\n")
}

func heuristicSearchAdvice(listing models.Listing, search models.SearchSpec, comparables []models.ComparableDeal) string {
	if len(comparables) == 0 {
		return ""
	}

	queryTokens := tokenSet(search.Query)
	common := map[string]int{}
	for _, comp := range comparables {
		for token := range tokenSet(comp.Title) {
			if _, exists := queryTokens[token]; exists {
				continue
			}
			common[token]++
		}
	}

	bestToken := ""
	bestCount := 0
	for token, count := range common {
		if count > bestCount {
			bestToken = token
			bestCount = count
		}
	}

	if bestCount < 2 || bestToken == "" {
		return ""
	}

	return fmt.Sprintf("Consider adding \"%s\" to the %q search when you want tighter comparable matches for listings like %q.", bestToken, search.Name, listing.Title)
}

func comparableReason(similarity, priceDistance float64) string {
	switch {
	case similarity >= 0.7:
		return "very close title/spec match"
	case similarity >= 0.4:
		return "strong overlap in title/spec terms"
	case priceDistance <= 0.1:
		return "close price band match"
	default:
		return "related listing in the same search bucket"
	}
}

func averageSimilarity(comparables []models.ComparableDeal) float64 {
	if len(comparables) == 0 {
		return 0
	}
	total := 0.0
	for _, comp := range comparables {
		total += comp.Similarity
	}
	return total / float64(len(comparables))
}

func tokenSet(value string) map[string]struct{} {
	re := regexp.MustCompile(`[a-zA-Z0-9]{2,}`)
	matches := re.FindAllString(strings.ToLower(value), -1)
	out := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if stopWords[match] {
			continue
		}
		out[match] = struct{}{}
	}
	return out
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	intersection := 0
	union := len(a)
	for token := range b {
		if _, ok := a[token]; ok {
			intersection++
			continue
		}
		union++
	}

	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func extractJSON(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		return value
	}

	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start >= 0 && end > start {
		return value[start : end+1]
	}

	return value
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

var stopWords = map[string]bool{
	"de": true, "het": true, "een": true, "en": true, "met": true, "voor": true,
	"van": true, "op": true, "te": true, "in": true, "is": true, "used": true,
	"nieuw": true, "goed": true, "als": true, "new": true,
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type aiListingAnalysis struct {
	FairPriceCents    int     `json:"fair_price_cents"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning"`
	SearchAdvice      string  `json:"search_advice"`
	ComparableIndexes []int   `json:"comparable_indexes"`
}
