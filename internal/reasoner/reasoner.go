package reasoner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/models"
)

// UsageCallback is called after each LLM request with token counts and timing.
type UsageCallback func(callType, model string, promptTokens, completionTokens, latencyMs int, success bool, errMsg string)

type Reasoner struct {
	cfg     config.AIConfig
	client  *http.Client
	limiter *rateLimiter
	onUsage UsageCallback
}

func New(cfg config.AIConfig) *Reasoner {
	cfg = config.NormalizeAIConfig(cfg)
	return &Reasoner{
		cfg: cfg,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
		limiter: newRateLimiter(cfg.MaxCallsPerUserPerHour, cfg.MaxCallsGlobalPerHour),
	}
}

func (r *Reasoner) SetUsageCallback(cb UsageCallback) { r.onUsage = cb }

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
	heuristic := r.HeuristicAnalysis(listing, search, marketAvg, ranked)

	if !r.Enabled() {
		return heuristic, nil
	}

	if heuristic.Confidence >= r.cfg.SkipLLMConfidence {
		score := heuristicDealScore(listing.Price, heuristic.FairPrice)
		if score <= r.cfg.SkipLLMScoreLow || score >= r.cfg.SkipLLMScoreHigh {
			heuristic.Source = "heuristic-confident"
			return heuristic, nil
		}
	}

	llmAnalysis, err := r.callLLM(ctx, listing, search, marketAvg, ranked)
	if err != nil {
		if errors.Is(err, errRateLimited) {
			heuristic.Source = "rate-limited"
			return heuristic, nil
		}
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

func (r *Reasoner) HeuristicAnalysis(
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
		Relevant:        true, // heuristic path cannot judge relevance; rely on worker pre-filter
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

var errRateLimited = errors.New("reasoner llm rate limited")

func (r *Reasoner) callLLM(
	ctx context.Context,
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
) (models.DealAnalysis, error) {
	if r.limiter != nil && !r.limiter.Allow(search.UserID) {
		return models.DealAnalysis{}, errRateLimited
	}

	payload := chatCompletionRequest{
		Model:       r.cfg.Model,
		Temperature: r.cfg.Temperature,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: "You analyze secondhand marketplace listings for resale and value hunting. " +
					"You must also judge whether the listing is actually relevant to what the user searched for — " +
					"a listing is NOT relevant if it is a completely different product category than the search intent " +
					"(e.g. a phone appearing in a camera search, or a bag appearing in a laptop search). " +
					"Reply with strict JSON only.",
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

	start := time.Now()
	resp, err := r.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		r.reportUsage("reasoner", 0, 0, latencyMs, false, err.Error())
		return models.DealAnalysis{}, fmt.Errorf("call ai provider: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := fmt.Sprintf("ai provider returned status %d", resp.StatusCode)
		r.reportUsage("reasoner", 0, 0, latencyMs, false, errMsg)
		return models.DealAnalysis{}, fmt.Errorf("%s", errMsg)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		r.reportUsage("reasoner", 0, 0, latencyMs, false, err.Error())
		return models.DealAnalysis{}, fmt.Errorf("decode ai response: %w", err)
	}

	r.reportUsage("reasoner", completion.Usage.PromptTokens, completion.Usage.CompletionTokens, latencyMs, true, "")

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
	parsed.FairPriceCents = normalizeFairPriceCents(parsed.FairPriceCents, listing.Price, marketAvg, selected)

	return models.DealAnalysis{
		Relevant:        parsed.Relevant,
		FairPrice:       parsed.FairPriceCents,
		Confidence:      clamp(parsed.Confidence, 0.05, 0.99),
		Reason:          strings.TrimSpace(parsed.Reasoning),
		Source:          "ai",
		ComparableDeals: selected,
		SearchAdvice:    strings.TrimSpace(parsed.SearchAdvice),
	}, nil
}

func (r *Reasoner) reportUsage(callType string, prompt, completion, latencyMs int, success bool, errMsg string) {
	if r.onUsage != nil {
		r.onUsage(callType, r.cfg.Model, prompt, completion, latencyMs, success, errMsg)
	}
}

func buildPrompt(
	listing models.Listing,
	search models.SearchSpec,
	marketAvg int,
	comparables []models.ComparableDeal,
	includeAdvice bool,
) string {
	type promptListing struct {
		Title     string `json:"t"`
		Desc      string `json:"d,omitempty"`
		Price     int    `json:"p"`
		PriceType string `json:"pt,omitempty"`
		Condition string `json:"c,omitempty"`
	}
	type promptSearch struct {
		Query    string `json:"q"`
		MaxPrice int    `json:"max,omitempty"`
		MinPrice int    `json:"min,omitempty"`
	}
	type promptComparable struct {
		Index int     `json:"i"`
		Title string  `json:"t"`
		Price int     `json:"p"`
		Sim   float64 `json:"s"`
	}

	input := struct {
		Listing          promptListing      `json:"l"`
		Search           promptSearch       `json:"s"`
		MarketAvgCents   int                `json:"m,omitempty"`
		NeedSearchAdvice bool               `json:"a,omitempty"`
		Comparables      []promptComparable `json:"c,omitempty"`
	}{
		Listing: promptListing{
			Title:     listing.Title,
			Desc:      trimPromptText(listing.Description, 300),
			Price:     listing.Price,
			PriceType: listing.PriceType,
			Condition: listing.Condition,
		},
		Search: promptSearch{
			Query:    search.Query,
			MaxPrice: search.MaxPrice,
			MinPrice: search.MinPrice,
		},
		MarketAvgCents:   marketAvg,
		NeedSearchAdvice: includeAdvice,
		Comparables:      make([]promptComparable, 0, len(comparables)),
	}

	for i, comp := range comparables {
		input.Comparables = append(input.Comparables, promptComparable{
			Index: i,
			Title: comp.Title,
			Price: comp.Price,
			Sim:   roundPromptFloat(comp.Similarity),
		})
	}

	raw, _ := json.Marshal(input)
	return strings.Join([]string{
		`Analyze listing vs comparables. IMPORTANT: all numeric prices are integer euro cents (e.g. 16361 means EUR 163.61). Set relevant=false if wrong product category. Return JSON: {"relevant":true,"fair_price_cents":N,"confidence":0.0-1.0,"reasoning":"...","search_advice":"...","comparable_indexes":[0,2]}`,
		string(raw),
	}, "\n")
}

func trimPromptText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxChars <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars]))
}

func roundPromptFloat(value float64) float64 {
	return math.Round(value*100) / 100
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

// normalizeFairPriceCents corrects common LLM unit mistakes (treating cents as
// euros or vice versa). It keeps the original value unless an alternate unit
// scaling lands substantially closer to the observed price baseline.
func normalizeFairPriceCents(rawFair, listingPrice, marketAvg int, comparables []models.ComparableDeal) int {
	if rawFair <= 0 {
		return rawFair
	}
	baseline := referencePriceBaseline(listingPrice, marketAvg, comparables)
	if baseline <= 0 {
		return rawFair
	}

	candidates := []int{rawFair}
	if rawFair >= 100 {
		candidates = append(candidates, rawFair/100)
	}
	maxInt := int(^uint(0) >> 1)
	if rawFair > 0 && rawFair <= maxInt/100 {
		candidates = append(candidates, rawFair*100)
	}

	best := rawFair
	bestDistance := absInt(rawFair - baseline)
	for _, candidate := range candidates[1:] {
		if candidate <= 0 {
			continue
		}
		distance := absInt(candidate - baseline)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	if best == rawFair {
		return rawFair
	}

	rawDistance := absInt(rawFair - baseline)
	if rawDistance <= baseline*4 {
		return rawFair
	}
	// Require a material improvement before rewriting the model output.
	if bestDistance >= rawDistance/2 {
		return rawFair
	}
	return best
}

func referencePriceBaseline(listingPrice, marketAvg int, comparables []models.ComparableDeal) int {
	prices := make([]int, 0, len(comparables)+2)
	if listingPrice > 0 {
		prices = append(prices, listingPrice)
	}
	if marketAvg > 0 {
		prices = append(prices, marketAvg)
	}
	for _, comp := range comparables {
		if comp.Price > 0 {
			prices = append(prices, comp.Price)
		}
	}
	if len(prices) == 0 {
		return 0
	}
	slices.Sort(prices)
	mid := len(prices) / 2
	if len(prices)%2 == 0 {
		return (prices[mid-1] + prices[mid]) / 2
	}
	return prices[mid]
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
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

func heuristicDealScore(listingPrice, fairPrice int) float64 {
	if listingPrice <= 0 || fairPrice <= 0 {
		return 5.0
	}
	ratio := float64(listingPrice) / float64(fairPrice)
	return clamp(10.0-10.0*ratio+5.0, 1, 10)
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"this": true, "that": true, "are": true, "has": true, "have": true,
	"in": true, "is": true, "it": true, "on": true, "at": true,
	"used": true, "good": true, "new": true, "like": true,
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
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type aiListingAnalysis struct {
	Relevant          bool    `json:"relevant"`
	FairPriceCents    int     `json:"fair_price_cents"`
	Confidence        float64 `json:"confidence"`
	Reasoning         string  `json:"reasoning"`
	SearchAdvice      string  `json:"search_advice"`
	ComparableIndexes []int   `json:"comparable_indexes"`
}
