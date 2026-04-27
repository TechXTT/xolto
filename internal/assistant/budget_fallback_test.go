package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/config"
)

// TestChatTextReturnsQuotaExhaustedOnCapFire confirms that when the
// W19-23 global $3/24h cap is exhausted, chatText:
//
//  1. Does NOT make an HTTP call.
//  2. Returns *QuotaExhaustedError with a non-zero RetryAfter.
//  3. The typed error matches via errors.As + IsAIQuotaExhausted helper.
func TestChatTextReturnsQuotaExhaustedOnCapFire(t *testing.T) {
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	tr := aibudget.New()
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
	aibudget.SetGlobal(tr)

	a := New(&config.Config{
		AI: config.AIConfig{
			Enabled: true,
			BaseURL: "http://127.0.0.1:1", // unreachable on purpose
			APIKey:  "test",
			Model:   "gpt-5-mini",
		},
	}, nil, nil, nil)

	_, err := a.chatText(context.Background(), "user-1", 0, "test", map[string]any{
		"model":       "gpt-5-mini",
		"temperature": 0.5,
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err == nil {
		t.Fatalf("expected error on cap-fire, got nil")
	}
	var qe *QuotaExhaustedError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QuotaExhaustedError, got %T: %v", err, err)
	}
	if qe.RetryAfter <= 0 {
		t.Fatalf("expected non-zero RetryAfter, got %v", qe.RetryAfter)
	}
	if !IsAIQuotaExhausted(err) {
		t.Fatalf("IsAIQuotaExhausted helper should return true for *QuotaExhaustedError")
	}
}

// TestParseBriefPropagatesQuotaExhausted is a regression test for the
// 2026-04-27 production incident where parseBrief silently swallowed the
// QuotaExhaustedError from parseBriefWithAI and returned the heuristic
// mission, causing /assistant/converse to render 200 OK with a degraded
// response instead of 503 + Retry-After. Pro/Buyer tiers paid for AI; the
// silent fallback was a billing-trust regression.
//
// This test exhausts the global cap and asserts parseBrief surfaces the
// typed error so the API layer can render 503. Other (non-cap) errors from
// parseBriefWithAI still degrade gracefully — that path is exercised by the
// existing assistant_test.go heuristic-fallback coverage.
func TestParseBriefPropagatesQuotaExhausted(t *testing.T) {
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	tr := aibudget.New()
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
	aibudget.SetGlobal(tr)

	a := New(&config.Config{
		AI: config.AIConfig{
			Enabled: true,
			BaseURL: "http://127.0.0.1:1", // unreachable on purpose
			APIKey:  "test",
			Model:   "gpt-5-mini",
		},
	}, nil, nil, nil)

	_, err := a.parseBrief(context.Background(), "user-1", "I want a Sony A6700 around 800 EUR")
	if err == nil {
		t.Fatalf("expected error on cap-fire (parseBrief must propagate, not swallow), got nil")
	}
	var qe *QuotaExhaustedError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QuotaExhaustedError, got %T: %v", err, err)
	}
	if qe.RetryAfter <= 0 {
		t.Fatalf("expected non-zero RetryAfter, got %v", qe.RetryAfter)
	}
}
