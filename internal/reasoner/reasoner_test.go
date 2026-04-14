package reasoner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBuildPromptUsesSlimPayload(t *testing.T) {
	description := strings.Repeat("a", 320) + "TAIL"
	prompt := buildPrompt(
		models.Listing{
			CanonicalID: "marktplaats:123",
			Title:       "Sony A7 III",
			Description: description,
			Price:       95000,
			PriceType:   "negotiable",
			Condition:   "used",
			ImageURLs:   []string{"https://example.com/image.jpg"},
			Seller:      models.Seller{ID: "seller-1", Name: "Alice"},
			Location:    models.Location{City: "Amsterdam", Distance: 1000},
		},
		models.SearchSpec{
			ID:              42,
			UserID:          "u1",
			ProfileID:       9,
			Name:            "Camera hunt",
			Query:           "sony a7 iii",
			MaxPrice:        100000,
			MinPrice:        80000,
			OfferPercentage: 70,
			AutoMessage:     true,
			MessageTemplate: "hi",
			Enabled:         true,
		},
		99000,
		[]models.ComparableDeal{
			{Title: "Sony A7 III body", Price: 97000, Similarity: 0.876, MatchReason: "strong title match"},
		},
		true,
	)

	if !strings.Contains(prompt, `"l":{"t":"Sony A7 III"`) {
		t.Fatalf("expected slim listing payload, got %q", prompt)
	}
	for _, forbidden := range []string{
		"image_urls",
		"seller",
		"location",
		"canonical",
		"match_reason",
		"message_template",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt unexpectedly contains %q: %q", forbidden, prompt)
		}
	}
	if strings.Contains(prompt, "TAIL") {
		t.Fatalf("expected description truncation, got %q", prompt)
	}
}

func TestAnalyzeSkipsLLMForConfidentHeuristic(t *testing.T) {
	r := New(config.AIConfig{
		Enabled:           true,
		APIKey:            "test-key",
		Model:             "test-model",
		SkipLLMConfidence: 0.75,
		SkipLLMScoreLow:   3.0,
		SkipLLMScoreHigh:  9.0,
	})

	calls := 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("llm should not be called")
	})

	analysis, err := r.Analyze(
		context.Background(),
		models.Listing{
			Title:       "Sony A7 III body",
			Description: "Sony mirrorless camera body only",
			Price:       1000,
			PriceType:   "fixed",
		},
		models.SearchSpec{Query: "sony a7 iii"},
		10000,
		[]models.ComparableDeal{
			{Title: "Sony A7 III body", Price: 10000},
			{Title: "Sony A7 III camera body", Price: 9800},
			{Title: "Sony A7 III mirrorless body", Price: 10200},
		},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if analysis.Source != "heuristic-confident" {
		t.Fatalf("expected heuristic-confident source, got %q", analysis.Source)
	}
	if calls != 0 {
		t.Fatalf("expected no LLM call, got %d", calls)
	}
}

func TestAnalyzeFallsBackWhenRateLimited(t *testing.T) {
	r := New(config.AIConfig{
		Enabled:                true,
		APIKey:                 "test-key",
		Model:                  "test-model",
		MaxCallsPerUserPerHour: 1,
		MaxCallsGlobalPerHour:  10,
	})

	calls := 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body := `{"choices":[{"message":{"role":"assistant","content":"{\"relevant\":true,\"fair_price_cents\":10000,\"confidence\":0.80,\"reasoning\":\"ok\",\"search_advice\":\"\",\"comparable_indexes\":[]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	listing := models.Listing{
		Title:     "Sony A7 III body",
		Price:     10000,
		PriceType: "fixed",
	}
	search := models.SearchSpec{
		UserID: "u1",
		Query:  "sony a7 iii",
	}

	first, err := r.Analyze(context.Background(), listing, search, 10000, nil)
	if err != nil {
		t.Fatalf("first Analyze() error = %v", err)
	}
	if first.Source != "ai" {
		t.Fatalf("expected first analysis from ai, got %q", first.Source)
	}

	second, err := r.Analyze(context.Background(), listing, search, 10000, nil)
	if err != nil {
		t.Fatalf("second Analyze() error = %v", err)
	}
	if second.Source != "rate-limited" {
		t.Fatalf("expected rate-limited fallback, got %q", second.Source)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one LLM call, got %d", calls)
	}
}

func TestNormalizeFairPriceCentsFixesEuroCentMismatch(t *testing.T) {
	got := normalizeFairPriceCents(
		1636100, // likely EUR interpreted then multiplied by 100
		16361,
		17000,
		[]models.ComparableDeal{
			{Price: 16000},
			{Price: 18000},
		},
	)
	if got != 16361 {
		t.Fatalf("expected normalized fair price 16361, got %d", got)
	}
}

func TestNormalizeFairPriceCentsKeepsReasonableValues(t *testing.T) {
	got := normalizeFairPriceCents(
		16800,
		16361,
		17000,
		[]models.ComparableDeal{
			{Price: 16000},
			{Price: 18000},
		},
	)
	if got != 16800 {
		t.Fatalf("expected fair price to remain unchanged, got %d", got)
	}
}
