package scorer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/store"
)

func TestScorePrefiltersObviouslyOverBudgetListing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-scorer.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	rsn := reasoner.New(config.AIConfig{})
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	scored := sc.Score(context.Background(), models.Listing{
		ItemID:    "m1",
		Title:     "Sony A7 III",
		Price:     200000,
		PriceType: "fixed",
	}, models.SearchSpec{
		UserID:          "u1",
		Query:           "sony a7 iii",
		MaxPrice:        100000,
		OfferPercentage: 70,
	})

	if scored.ReasoningSource != "prefilter" {
		t.Fatalf("expected prefilter reasoning source, got %q", scored.ReasoningSource)
	}
	if scored.Confidence <= 0 {
		t.Fatalf("expected heuristic confidence to be preserved, got %.2f", scored.Confidence)
	}
}

func TestScoreUsesAICacheForRepeatedListing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-scorer-cache.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var llmCalls atomic.Int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"choices":[
				{"message":{"content":"{\"relevant\":true,\"fair_price_cents\":51000,\"confidence\":0.82,\"reasoning\":\"cached ai reasoning\",\"search_advice\":\"\",\"comparable_indexes\":[]}"}} 
			],
			"usage":{"prompt_tokens":120,"completion_tokens":40,"total_tokens":160}
		}`)
	}))
	defer llmServer.Close()

	rsn := reasoner.New(config.AIConfig{
		Enabled:       true,
		BaseURL:       llmServer.URL,
		APIKey:        "test-key",
		Model:         "test-model",
		PromptVersion: 1,
	})
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	listing := models.Listing{
		ItemID:    "olxbg-sony-a6000-123",
		Title:     "Sony A6000 body",
		Price:     50000,
		PriceType: "fixed",
	}
	search := models.SearchSpec{
		UserID:          "u1",
		Query:           "sony a6000",
		OfferPercentage: 70,
	}

	first := sc.Score(context.Background(), listing, search)
	second := sc.Score(context.Background(), listing, search)

	if first.ReasoningSource != "ai" {
		t.Fatalf("expected first score to use ai, got %q", first.ReasoningSource)
	}
	if second.ReasoningSource != "ai-cache" {
		t.Fatalf("expected second score to use ai-cache, got %q", second.ReasoningSource)
	}
	if llmCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 llm call, got %d", llmCalls.Load())
	}
}

// TestAccessoryPreFilter verifies that accessory-only titles are caught by the
// pre-filter and returned with ReasoningSource=accessory-prefilter before any
// LLM call is attempted (XOL-21).
func TestAccessoryPreFilter(t *testing.T) {
	accessoryCases := []struct {
		title string
		match bool
	}{
		// NL accessories
		{"Laptop adapter 65W", true},
		{"Accu voor Dell Inspiron", true},
		{"Oplader Lenovo ThinkPad", true},
		// BG accessories
		{"Зарядно за лаптоп Dell", true},
		{"Батерия за MacBook Pro", true},
		{"Части за лаптоп Lenovo", true},
		// EN accessories
		{"Charger for MacBook Air", true},
		{"Battery 87Wh replacement", true},
		{"Laptop bag 15 inch", true},
		// Primary devices — must NOT be filtered
		{"Dell XPS 15 laptop", false},
		{"MacBook Pro 14 M3", false},
		{"Sony A7 III body", false},
		{"Lenovo ThinkPad X1 Carbon i7", false},
	}

	for _, tc := range accessoryCases {
		t.Run(tc.title, func(t *testing.T) {
			got := isAccessoryTitle(tc.title)
			if got != tc.match {
				t.Errorf("isAccessoryTitle(%q) = %v, want %v", tc.title, got, tc.match)
			}
		})
	}
}

// TestScoreEmitsRecommendedActionForUnscorableListing locks the contract that
// reserved/fast-bid/no-price listings — which bypass the full scoring pipeline
// — still carry a non-empty recommended_action so the dash never sees an empty
// enum. Per the F-5 locked taxonomy, the trust-preservation default is
// ask_seller.
func TestScoreEmitsRecommendedActionForUnscorableListing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-scorer-unscorable.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	rsn := reasoner.New(config.AIConfig{})
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	cases := []struct {
		name      string
		priceType string
		price     int
	}{
		{"reserved", "reserved", 0},
		{"fast-bid", "fast-bid", 0},
		{"zero-price-fixed", "fixed", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scored := sc.Score(context.Background(), models.Listing{
				ItemID:    "m-" + tc.name,
				Title:     "Sony A6000 " + tc.name,
				Price:     tc.price,
				PriceType: tc.priceType,
			}, models.SearchSpec{
				UserID:   "u1",
				Query:    "sony a6000",
				MaxPrice: 90000,
			})
			if scored.RecommendedAction != ActionAskSeller {
				t.Fatalf("unscorable listing RecommendedAction = %q, want %q",
					scored.RecommendedAction, ActionAskSeller)
			}
		})
	}
}

// TestComputeRiskFlagsOffPlatformRedirect verifies that off_platform_redirect
// fires for OLX.bg scam contact patterns and does not fire for clean listings (XOL-80).
func TestComputeRiskFlagsOffPlatformRedirect(t *testing.T) {
	cases := []struct {
		name        string
		description string
		wantFlag    bool
	}{
		{
			name:        "cyrillic_write_whatsapp_with_phone",
			description: "Пишете ми на WhatsApp +359888123456",
			wantFlag:    true,
		},
		{
			name:        "cyrillic_call_bg_mobile",
			description: "Обадете се на 0877123456 за повече информация",
			wantFlag:    true,
		},
		{
			name:        "viber_mention_bulgarian",
			description: "За въпроси пишете на Viber или Telegram",
			wantFlag:    true,
		},
		{
			name:        "phone_only_no_messenger",
			description: "Телефон за контакт: 0887654321",
			wantFlag:    true,
		},
		{
			name:        "clean_description",
			description: "Отлично качество, без проблеми. Продавам поради закупуване на нов модел.",
			wantFlag:    false,
		},
		{
			name:        "empty_description",
			description: "",
			wantFlag:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := computeRiskFlags(models.Listing{
				ItemID:      "test-" + tc.name,
				Title:       "Sony A6000",
				Description: tc.description,
				Price:       50000,
				PriceType:   "fixed",
				ImageURLs:   []string{"a", "b", "c"},
			}, 0)
			got := containsFlag(flags, "off_platform_redirect")
			if got != tc.wantFlag {
				t.Errorf("off_platform_redirect for %q: got %v, want %v (flags=%v)",
					tc.description, got, tc.wantFlag, flags)
			}
		})
	}
}

// TestComputeRiskFlagsVagueConditionBG verifies that BG vague-condition terms
// trigger the vague_condition flag (XOL-80).
func TestComputeRiskFlagsVagueConditionBG(t *testing.T) {
	cases := []struct {
		name  string
		title string
		desc  string
	}{
		{
			name:  "for_parts_bg",
			title: "Лаптоп Dell за части",
			desc:  "",
		},
		{
			name:  "not_working_bg",
			title: "iPhone 12",
			desc:  "Телефонът не работи, продавам за части.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := computeRiskFlags(models.Listing{
				ItemID:      "test-" + tc.name,
				Title:       tc.title,
				Description: tc.desc,
				Price:       10000,
				PriceType:   "fixed",
				ImageURLs:   []string{"a", "b", "c"},
			}, 0)
			if !containsFlag(flags, "vague_condition") {
				t.Errorf("expected vague_condition flag for title=%q desc=%q, got flags=%v",
					tc.title, tc.desc, flags)
			}
		})
	}
}

// containsFlag is a test helper that checks whether a named flag appears in a
// flag slice.
func containsFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}
