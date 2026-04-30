package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	defaultCategoryID = 356
	cameraCategoryID  = 487
	lensCategoryID    = 495
)

// UsageCallback is called after each LLM request with token counts and timing.
type UsageCallback func(callType, model string, promptTokens, completionTokens, latencyMs int, success bool, errMsg string)

type Generator struct {
	aiCfg   config.AIConfig
	model   string // per-call-site override (XOL-60 SUP-9); defaults to aiCfg.Model
	client  *http.Client
	onUsage UsageCallback
}

func New(aiCfg config.AIConfig) *Generator {
	return &Generator{
		aiCfg: aiCfg,
		model: aiCfg.Model,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// SetModel sets the per-call-site model override (AI_MODEL_GENERATOR).
// It falls through to aiCfg.Model when not explicitly set. Call after New().
func (g *Generator) SetModel(model string) {
	if model != "" {
		g.model = model
	}
}

func (g *Generator) SetUsageCallback(cb UsageCallback) { g.onUsage = cb }

func (g *Generator) GenerateSearches(ctx context.Context, topic string) ([]config.SearchConfig, error) {
	normalized := strings.TrimSpace(strings.ToLower(topic))
	if normalized == "" {
		return nil, fmt.Errorf("topic is required")
	}

	if g.aiEnabled() {
		searches, err := g.generateWithAI(ctx, topic)
		if err == nil && len(searches) > 0 {
			return searches, nil
		}
		if err != nil {
			return fallbackSearches(topic), fmt.Errorf("ai generation failed, used fallback: %w", err)
		}
	}

	return fallbackSearches(topic), nil
}

func PrintSearches(searches []config.SearchConfig) error {
	payload := struct {
		Searches []config.SearchConfig `yaml:"searches"`
	}{
		Searches: searches,
	}

	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer encoder.Close()

	return encoder.Encode(payload)
}

func (g *Generator) aiEnabled() bool {
	return g.aiCfg.Enabled && g.aiCfg.APIKey != "" && g.model != ""
}

func (g *Generator) generateWithAI(ctx context.Context, topic string) ([]config.SearchConfig, error) {
	// searchConfigSchema is derived from config.SearchConfig — the exact fields
	// returned by the AI (XOL-60 SUP-9 strict json_schema).
	// Using map[string]any so that JSON serialization is field-order-stable and
	// no typed struct inadvertently emits unexpected zero-value fields (XOL-66).
	searchConfigSchema := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "search_config_list",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"searches": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":             map[string]any{"type": "string"},
								"query":            map[string]any{"type": "string"},
								"category_id":      map[string]any{"type": "integer"},
								"max_price":        map[string]any{"type": "integer"},
								"min_price":        map[string]any{"type": "integer"},
								"condition":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								"offer_percentage": map[string]any{"type": "integer"},
								"auto_message":     map[string]any{"type": "boolean"},
								"message_template": map[string]any{"type": "string"},
							},
							"required":             []string{"name", "query", "category_id", "max_price", "min_price", "condition", "offer_percentage", "auto_message", "message_template"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"searches"},
				"additionalProperties": false,
			},
		},
	}

	// gpt-5 reasoning models reject temperature != 1 and consume hidden reasoning
	// tokens before visible output. Omit temperature, set max_completion_tokens.
	// Mirrors the pattern in internal/reasoner/reasoner.go (XOL-66).
	reqPayload := map[string]any{
		"model":                 g.model,
		"max_completion_tokens": 2048,
		"messages": []map[string]any{
			{
				"role": "system",
				"content": "You generate search presets for European second-hand marketplaces (OLX.bg, Marktplaats, Vinted). Return strict JSON only. " +
					"Optimize for bargain hunting of used electronics: cameras, lenses, laptops, phones, gaming gear, audio equipment. Use euro budgets as whole euros. " +
					"For camera bodies use category_id 487, for lenses use 495, and only use 356 for gaming-related items. " +
					"Use canonical English conditions (new, like_new, good, fair) and keep auto_message false.",
			},
			{
				"role":    "user",
				"content": buildPrompt(topic),
			},
		},
		"response_format": searchConfigSchema,
	}

	body, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal ai request: %w", err)
	}

	// W19-23 global AI-spend cap. Pre-spend gate. Search generation is the
	// least-critical AI path (no real-time UX), so on cap-fire we return
	// a typed error and the caller falls back to the static preset list.
	budget := aibudget.Global()
	if budget != nil {
		if allowed, _ := budget.Allow(ctx, "generator", aibudget.EstimatedCostPerCallUSD); !allowed {
			return nil, fmt.Errorf("global ai budget exhausted")
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.aiCfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return nil, fmt.Errorf("build ai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.aiCfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := g.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		g.reportUsage("generator", 0, 0, latencyMs, false, err.Error())
		return nil, fmt.Errorf("call ai provider: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		errMsg := fmt.Sprintf("ai provider returned status %d", resp.StatusCode)
		g.reportUsage("generator", 0, 0, latencyMs, false, errMsg)
		return nil, fmt.Errorf("%s", errMsg)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		g.reportUsage("generator", 0, 0, latencyMs, false, err.Error())
		return nil, fmt.Errorf("decode ai response: %w", err)
	}

	g.reportUsage("generator", completion.Usage.PromptTokens, completion.Usage.CompletionTokens, latencyMs, true, "")
	if budget != nil {
		actualCostUSD := float64(completion.Usage.PromptTokens)*0.25/1_000_000 +
			float64(completion.Usage.CompletionTokens)*2.00/1_000_000
		budget.Reconcile(actualCostUSD)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("ai response contained no choices")
	}

	// With strict json_schema mode, the model returns valid JSON directly.
	// No extractJSON fallback — surface parse failures as typed errors.
	var payload struct {
		Searches []config.SearchConfig `json:"searches"`
	}
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &payload); err != nil {
		return nil, fmt.Errorf("parse ai json: %w", err)
	}

	searches := sanitizeSearches(payload.Searches)
	if len(searches) == 0 {
		return nil, fmt.Errorf("ai response did not contain valid searches")
	}
	return searches, nil
}

func buildPrompt(topic string) string {
	return strings.Join([]string{
		fmt.Sprintf("Create 3 to 5 second-hand marketplace search presets for this topic: %q.", topic),
		"Return JSON with this exact shape only:",
		`{"searches":[{"name":"Sony A7 IV","query":"sony a7 iv","category_id":487,"max_price":1800,"min_price":900,"condition":["good","like_new"],"offer_percentage":78,"auto_message":false,"message_template":"Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?"},{"name":"Sony FE 24-70mm","query":"sony fe 24-70","category_id":495,"max_price":900,"min_price":200,"condition":["good","like_new"],"offer_percentage":72,"auto_message":false,"message_template":"Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?"}]}`,
		"Rules:",
		"- Focus on likely used-market deal opportunities, not brand-new retail terms.",
		"- Prefer specific model searches plus one accessory or lens search when appropriate.",
		"- Use realistic euro price ranges.",
		"- Use canonical English condition values: new, like_new, good, fair.",
		"- Use category_id 487 for camera bodies and 495 for lenses/objectives.",
		"- Keep message_template polite and short.",
	}, "\n")
}

func sanitizeSearches(searches []config.SearchConfig) []config.SearchConfig {
	out := make([]config.SearchConfig, 0, len(searches))
	for _, search := range searches {
		if strings.TrimSpace(search.Query) == "" {
			continue
		}
		if strings.TrimSpace(search.Name) == "" {
			search.Name = cleanName(search.Query)
		}
		search.CategoryID = inferredCategoryID(search)
		if search.MinPrice < 0 {
			search.MinPrice = 0
		}
		if search.MaxPrice < search.MinPrice {
			search.MaxPrice = search.MinPrice
		}
		if search.OfferPercentage <= 0 || search.OfferPercentage > 100 {
			search.OfferPercentage = 72
		}
		if len(search.Condition) == 0 {
			search.Condition = []string{"good", "like_new"}
		}
		search.AutoMessage = false
		if strings.TrimSpace(search.MessageTemplate) == "" {
			search.MessageTemplate = "Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?"
		}
		out = append(out, search)
	}
	return out
}

func inferredCategoryID(search config.SearchConfig) int {
	text := strings.ToLower(strings.TrimSpace(search.Name + " " + search.Query))

	switch {
	case containsAny(text, "lens", "lenzen", "objectief", "objectieven", "16-35", "16-55", "24-70", "24-105", "28-70", "70-200", "35mm", "50mm", "85mm", "135mm", "f/", "fe ", "gm ", "g "):
		return lensCategoryID
	case containsAny(text, "sony a", "alpha", "camera", "camera body", "body", "fotocamera", "mirrorless", "a6400", "a6700", "a7", "a7r", "a7s", "z6", "z7", "r6", "r5", "xt5", "x-t5", "x-t4", "lumix"):
		return cameraCategoryID
	case search.CategoryID > 0:
		return search.CategoryID
	default:
		return defaultCategoryID
	}
}

func fallbackSearches(topic string) []config.SearchConfig {
	normalized := strings.TrimSpace(strings.ToLower(topic))
	switch {
	case strings.Contains(normalized, "sony"):
		return sonyCameraSearches()
	case strings.Contains(normalized, "camera"), strings.Contains(normalized, "cameras"):
		return genericCameraSearches(topic)
	default:
		return genericSearches(topic)
	}
}

func sonyCameraSearches() []config.SearchConfig {
	return []config.SearchConfig{
		{
			Name:            "Sony A7 IV",
			Query:           "sony a7 iv",
			CategoryID:      cameraCategoryID,
			MaxPrice:        1800,
			MinPrice:        900,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 78,
			AutoMessage:     false,
			MessageTemplate: "Hi, I'm interested in {{.Title}}. If everything works well, would you accept EUR {{.OfferPrice}}?",
		},
		{
			Name:            "Sony A7 III",
			Query:           "sony a7 iii",
			CategoryID:      cameraCategoryID,
			MaxPrice:        1100,
			MinPrice:        500,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 75,
			AutoMessage:     false,
			MessageTemplate: "Hi, is {{.Title}} still available? I can move quickly and offer EUR {{.OfferPrice}}.",
		},
		{
			Name:            "Sony A6700",
			Query:           "sony a6700",
			CategoryID:      cameraCategoryID,
			MaxPrice:        1400,
			MinPrice:        700,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 78,
			AutoMessage:     false,
			MessageTemplate: "Hi, I'm interested in {{.Title}}. Would EUR {{.OfferPrice}} work for you?",
		},
		{
			Name:            "Sony FE Lens",
			Query:           "sony fe lens",
			CategoryID:      lensCategoryID,
			MaxPrice:        900,
			MinPrice:        150,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: "Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?",
		},
	}
}

func genericCameraSearches(topic string) []config.SearchConfig {
	base := cleanName(topic)
	return []config.SearchConfig{
		{
			Name:            base + " Body",
			Query:           strings.ToLower(base + " body"),
			CategoryID:      cameraCategoryID,
			MaxPrice:        1500,
			MinPrice:        300,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 75,
			AutoMessage:     false,
			MessageTemplate: "Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?",
		},
		{
			Name:            base + " Lens",
			Query:           strings.ToLower(base + " lens"),
			CategoryID:      lensCategoryID,
			MaxPrice:        900,
			MinPrice:        100,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: "Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?",
		},
	}
}

func genericSearches(topic string) []config.SearchConfig {
	// W19-37 / XOL-134: broaden the static floor from 1 to 3 entries.
	// The prior single-entry shape produced 1 chip on missions for
	// non-sony/non-camera inputs (e.g. "Fujifilm X-T4", "MacBook Pro 14"),
	// violating the W19-31-locked 3-5 floor. Three deterministic
	// permutations cover the common buying-context shapes without
	// invoking the LLM.
	base := cleanName(topic)
	query := strings.ToLower(strings.TrimSpace(topic))
	template := "Hi, I'm interested in {{.Title}}. Would you accept EUR {{.OfferPrice}}?"
	return []config.SearchConfig{
		{
			Name:            base + " Deals",
			Query:           query,
			CategoryID:      defaultCategoryID,
			MaxPrice:        1000,
			MinPrice:        50,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: template,
		},
		{
			Name:            base + " Used",
			Query:           query + " used",
			CategoryID:      defaultCategoryID,
			MaxPrice:        1000,
			MinPrice:        50,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: template,
		},
		{
			Name:            base + " For Sale",
			Query:           query + " for sale",
			CategoryID:      defaultCategoryID,
			MaxPrice:        1000,
			MinPrice:        50,
			Condition:       []string{"good", "like_new"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: template,
		},
	}
}

func cleanName(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, " ")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
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

func (g *Generator) reportUsage(callType string, prompt, completion, latencyMs int, success bool, errMsg string) {
	if g.onUsage != nil {
		g.onUsage(callType, g.model, prompt, completion, latencyMs, success, errMsg)
	}
}
