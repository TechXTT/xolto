package reasoner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

// TestTokenSetCyrillicTitle verifies that Cyrillic characters in OLX BG titles
// are tokenised correctly after switching the regex to [\p{L}\p{N}]{2,}.
// Before this fix, a BG-only string returned an empty set (XOL-32 AC).
func TestTokenSetCyrillicTitle(t *testing.T) {
	// A representative OLX BG camera title — Latin model number + Cyrillic words.
	title := "Canon 6D Mark II с 24-70 f/2.8"
	tokens := tokenSet(title)

	// Must contain Cyrillic token "с" — but it is only 1 rune so it's excluded
	// by the {2,} minimum. Use a multi-char Cyrillic word instead.
	cyrillicTitle := "Фотоапарат Canon EOS R10 употребяван"
	tokens = tokenSet(cyrillicTitle)

	// XOL-32 AC: tokenSet on a Cyrillic-only string returns a non-empty set.
	if len(tokens) == 0 {
		t.Fatal("tokenSet on Cyrillic title returned empty set — Cyrillic tokenizer broken")
	}

	// Specific Cyrillic tokens that must appear.
	for _, want := range []string{"фотоапарат", "употребяван"} {
		if _, ok := tokens[want]; !ok {
			t.Errorf("expected Cyrillic token %q in set, got %v", want, tokens)
		}
	}

	// Latin tokens must still appear (regression guard).
	for _, want := range []string{"canon", "eos", "r10"} {
		if _, ok := tokens[want]; !ok {
			t.Errorf("expected Latin token %q in set, got %v", want, tokens)
		}
	}
}

// TestTokenSetCyrillicOnly verifies the AC from XOL-32: tokenSet on a
// Cyrillic-only string must return a non-empty set.
func TestTokenSetCyrillicOnly(t *testing.T) {
	tokens := tokenSet("слушалки батерия фотоапарат")
	if len(tokens) == 0 {
		t.Fatal("tokenSet on Cyrillic-only string returned empty set")
	}
	for _, want := range []string{"слушалки", "батерия", "фотоапарат"} {
		if _, ok := tokens[want]; !ok {
			t.Errorf("expected token %q, not found in %v", want, tokens)
		}
	}
}

// TestTokenSetLatinUnchanged verifies that Latin-only titles tokenise exactly
// as before (regression guard for the Cyrillic fix).
func TestTokenSetLatinUnchanged(t *testing.T) {
	tokens := tokenSet("Sony A7 III body mirrorless")
	for _, want := range []string{"sony", "a7", "iii", "body", "mirrorless"} {
		if _, ok := tokens[want]; !ok {
			t.Errorf("expected Latin token %q, not found in %v", want, tokens)
		}
	}
}

// ---------------------------------------------------------------------------
// XOL-60 SUP-9: per-call-site model override + json_schema request shape tests
// ---------------------------------------------------------------------------

// validScorerResponse is an AI response body the test server returns to allow
// the full callLLM path to complete.
const validScorerResponse = `{"choices":[{"message":{"role":"assistant","content":"{\"relevant\":true,\"fair_price_cents\":10000,\"confidence\":0.80,\"reasoning\":\"ok\",\"search_advice\":\"\",\"comparable_indexes\":[]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

// TestScorerRequestShape_ModelOverride verifies that:
//   - When SetModel is called with a non-empty string, the outgoing request body
//     carries that overridden model (not cfg.Model).
//   - The request body has response_format.type=="json_schema".
//   - The request body has response_format.json_schema.strict==true.
//   - The schema object inside json_schema is non-empty.
//
// (XOL-60 SUP-9 AC)
func TestScorerRequestShape_ModelOverride(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validScorerResponse))
	}))
	defer srv.Close()

	r := New(config.AIConfig{
		Enabled:               true,
		APIKey:                "test-key",
		Model:                 "gpt-4o-mini",
		BaseURL:               srv.URL,
		SkipLLMConfidence:     0.99, // ensure LLM is always called
		MaxCallsPerUserPerHour: 100,
		MaxCallsGlobalPerHour:  1000,
	})
	r.SetModel("gpt-5-mini") // per-call-site override
	r.client = srv.Client()

	listing := models.Listing{
		Title:     "Sony A7 III body",
		Price:     10000,
		PriceType: "fixed",
	}
	search := models.SearchSpec{
		UserID: "u1",
		Query:  "sony a7 iii",
	}
	_, err := r.Analyze(context.Background(), listing, search, 10000, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Assert model override propagated.
	if got, _ := captured["model"].(string); got != "gpt-5-mini" {
		t.Errorf("expected model=gpt-5-mini in request, got %q", got)
	}

	// Assert response_format.type == "json_schema".
	rf, ok := captured["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %#v", captured["response_format"])
	}
	if got := rf["type"]; got != "json_schema" {
		t.Errorf("expected response_format.type=json_schema, got %q", got)
	}

	// Assert response_format.json_schema.strict == true.
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema missing or wrong type: %#v", rf["json_schema"])
	}
	if got := js["strict"]; got != true {
		t.Errorf("expected response_format.json_schema.strict=true, got %v", got)
	}

	// Assert schema is non-empty.
	schema, ok := js["schema"].(map[string]any)
	if !ok || len(schema) == 0 {
		t.Errorf("expected non-empty schema, got %#v", js["schema"])
	}

	// gpt-5 compliance (XOL-65): temperature must be absent (gpt-5 rejects != 1),
	// and max_completion_tokens must be present (reasoning budget).
	if _, hasTemp := captured["temperature"]; hasTemp {
		t.Errorf("expected temperature absent from request (gpt-5 rejects non-default), got %v", captured["temperature"])
	}
	if got, _ := captured["max_completion_tokens"].(float64); got != 2048 {
		t.Errorf("expected max_completion_tokens=2048, got %v", captured["max_completion_tokens"])
	}
}

// TestScorerRequestShape_ModelFallthrough verifies that when SetModel is NOT
// called, the outgoing request uses the cfg.Model value (XOL-60 SUP-9 AC).
func TestScorerRequestShape_ModelFallthrough(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validScorerResponse))
	}))
	defer srv.Close()

	r := New(config.AIConfig{
		Enabled:                true,
		APIKey:                 "test-key",
		Model:                  "gpt-4o-mini",
		BaseURL:                srv.URL,
		SkipLLMConfidence:      0.99,
		MaxCallsPerUserPerHour: 100,
		MaxCallsGlobalPerHour:  1000,
	})
	// No SetModel call — should fall through to cfg.Model.
	r.client = srv.Client()

	listing := models.Listing{
		Title:     "Sony A7 III body",
		Price:     10000,
		PriceType: "fixed",
	}
	search := models.SearchSpec{
		UserID: "u1",
		Query:  "sony a7 iii",
	}
	_, err := r.Analyze(context.Background(), listing, search, 10000, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if got, _ := captured["model"].(string); got != "gpt-4o-mini" {
		t.Errorf("expected model=gpt-4o-mini (fallthrough), got %q", got)
	}
}

// TestScorerLocaleInstruction verifies that the system prompt carries a
// language instruction derived from search.CountryCode (XOL-20).
func TestScorerLocaleInstruction(t *testing.T) {
	cases := []struct {
		countryCode string
		wantSubstr  string
	}{
		{"BG", "Bulgarian"},
		{"bg", "Bulgarian"}, // case-insensitive
		{"NL", "Dutch"},
		{"", "English"},  // default
		{"US", "English"}, // unknown → English
	}

	for _, tc := range cases {
		t.Run(tc.countryCode, func(t *testing.T) {
			var captured map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &captured)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(validScorerResponse))
			}))
			defer srv.Close()

			r := New(config.AIConfig{
				Enabled:                true,
				APIKey:                 "test-key",
				Model:                  "gpt-5-nano",
				BaseURL:                srv.URL,
				SkipLLMConfidence:      0.99,
				MaxCallsPerUserPerHour: 100,
				MaxCallsGlobalPerHour:  1000,
			})
			r.client = srv.Client()

			listing := models.Listing{Title: "Sony A7 III", Price: 10000, PriceType: "fixed"}
			search := models.SearchSpec{
				UserID:      "u1",
				Query:       "sony a7 iii",
				CountryCode: tc.countryCode,
			}
			_, err := r.Analyze(context.Background(), listing, search, 10000, nil)
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}

			// Extract system message content from captured messages.
			msgs, _ := captured["messages"].([]any)
			var systemContent string
			for _, m := range msgs {
				msg, _ := m.(map[string]any)
				if msg["role"] == "system" {
					systemContent, _ = msg["content"].(string)
				}
			}
			if !strings.Contains(systemContent, tc.wantSubstr) {
				t.Errorf("expected system prompt to contain %q for country_code=%q, got: %q", tc.wantSubstr, tc.countryCode, systemContent)
			}
		})
	}
}
