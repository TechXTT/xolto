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

	"github.com/TechXTT/marktbot/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	defaultCategoryID = 356
	cameraCategoryID  = 487
	lensCategoryID    = 495
)

type Generator struct {
	aiCfg  config.AIConfig
	client *http.Client
}

func New(aiCfg config.AIConfig) *Generator {
	return &Generator{
		aiCfg: aiCfg,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

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
	return g.aiCfg.Enabled && g.aiCfg.APIKey != "" && g.aiCfg.Model != ""
}

func (g *Generator) generateWithAI(ctx context.Context, topic string) ([]config.SearchConfig, error) {
	reqBody := chatCompletionRequest{
		Model:       g.aiCfg.Model,
		Temperature: g.aiCfg.Temperature,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: "You generate marketplace search presets for Marktplaats.nl. Return strict JSON only. " +
					"Optimize for bargain hunting of used electronics. Use euro budgets as whole euros. " +
					"For camera bodies use category_id 487, for lenses use 495, and only use 356 for gaming-related items. " +
					"Use Dutch conditions and keep auto_message false.",
			},
			{
				Role:    "user",
				Content: buildPrompt(topic),
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal ai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.aiCfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build ai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.aiCfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ai provider: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ai provider returned status %d", resp.StatusCode)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return nil, fmt.Errorf("decode ai response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("ai response contained no choices")
	}

	content := extractJSON(completion.Choices[0].Message.Content)
	var payload struct {
		Searches []config.SearchConfig `json:"searches"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
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
		fmt.Sprintf("Create 3 to 5 Marktplaats search presets for this topic: %q.", topic),
		"Return JSON with this exact shape only:",
		`{"searches":[{"name":"Sony A7 IV","query":"sony a7 iv","category_id":487,"max_price":1800,"min_price":900,"condition":["Gebruikt","Zo goed als nieuw"],"offer_percentage":78,"auto_message":false,"message_template":"Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?"},{"name":"Sony FE 24-70mm","query":"sony fe 24-70","category_id":495,"max_price":900,"min_price":200,"condition":["Gebruikt","Zo goed als nieuw"],"offer_percentage":72,"auto_message":false,"message_template":"Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?"}]}`,
		"Rules:",
		"- Focus on likely used-market deal opportunities, not brand-new retail terms.",
		"- Prefer specific model searches plus one accessory or lens search when appropriate.",
		"- Use realistic euro price ranges.",
		"- Use Dutch condition values.",
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
			search.Condition = []string{"Gebruikt", "Zo goed als nieuw"}
		}
		search.AutoMessage = false
		if strings.TrimSpace(search.MessageTemplate) == "" {
			search.MessageTemplate = "Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?"
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
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 78,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik ben geïnteresseerd in {{.Title}}. Als alles goed werkt, zou je €{{.OfferPrice}} accepteren?",
		},
		{
			Name:            "Sony A7 III",
			Query:           "sony a7 iii",
			CategoryID:      cameraCategoryID,
			MaxPrice:        1100,
			MinPrice:        500,
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 75,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Is {{.Title}} nog beschikbaar? Ik kan snel handelen en €{{.OfferPrice}} bieden.",
		},
		{
			Name:            "Sony A6700",
			Query:           "sony a6700",
			CategoryID:      cameraCategoryID,
			MaxPrice:        1400,
			MinPrice:        700,
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 78,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik heb interesse in {{.Title}}. Zou €{{.OfferPrice}} een optie zijn?",
		},
		{
			Name:            "Sony FE Lens",
			Query:           "sony fe lens",
			CategoryID:      lensCategoryID,
			MaxPrice:        900,
			MinPrice:        150,
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?",
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
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 75,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?",
		},
		{
			Name:            base + " Lens",
			Query:           strings.ToLower(base + " lens"),
			CategoryID:      lensCategoryID,
			MaxPrice:        900,
			MinPrice:        100,
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?",
		},
	}
}

func genericSearches(topic string) []config.SearchConfig {
	base := cleanName(topic)
	query := strings.ToLower(strings.TrimSpace(topic))
	return []config.SearchConfig{
		{
			Name:            base + " Deals",
			Query:           query,
			CategoryID:      defaultCategoryID,
			MaxPrice:        1000,
			MinPrice:        50,
			Condition:       []string{"Gebruikt", "Zo goed als nieuw"},
			OfferPercentage: 72,
			AutoMessage:     false,
			MessageTemplate: "Hoi! Ik ben geïnteresseerd in {{.Title}}. Zou je €{{.OfferPrice}} accepteren?",
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
