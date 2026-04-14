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
