package replycopilot

import (
	"context"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/models"
)

type budgetStubClassifier struct {
	called bool
}

func (s *budgetStubClassifier) Classify(_ context.Context, _ string) (ClassifyResult, error) {
	s.called = true
	return ClassifyResult{Interpretation: InterpNegotiable, Confidence: ConfHigh}, nil
}

// TestReplycopilotReturnsAIQuotaExhaustedOnCapFire confirms that when the
// W19-23 global budget is exhausted, Interpret:
//
//  1. Does NOT call the LLM classifier.
//  2. Returns Interpretation = "ai_quota_exhausted".
//  3. Returns a localised user-facing draft message that mentions the
//     daily reset.
//  4. Recommends ActionAskSeller as a safe fallback.
func TestReplycopilotReturnsAIQuotaExhaustedOnCapFire(t *testing.T) {
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })

	tr := aibudget.New()
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
	aibudget.SetGlobal(tr)

	cls := &budgetStubClassifier{}
	listing := models.Listing{
		ItemID:    "olxbg-1",
		Title:     "iPhone 13",
		Price:     50000,
		FairPrice: 60000,
	}
	rc := ReplyContext{
		SellerReply: "Можем да говорим за цената",
	}

	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, cls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cls.called {
		t.Fatalf("classifier should NOT be called when global budget is exhausted")
	}
	if result.Interpretation != InterpAIQuotaExhausted {
		t.Fatalf("expected Interpretation=ai_quota_exhausted, got %q", result.Interpretation)
	}
	if result.RecommendedAction != ActionAskSeller {
		t.Fatalf("expected ActionAskSeller, got %q", result.RecommendedAction)
	}
	if result.DraftNextMessage == "" {
		t.Fatalf("expected non-empty draft message")
	}
	// The Cyrillic-dominant seller_reply should bias detection to BG, so
	// the draft should be in Bulgarian. Check for the AI quota message
	// fragment.
	if !strings.Contains(result.DraftNextMessage, "AI квотата") {
		t.Fatalf("expected BG draft mentioning AI квотата, got %q", result.DraftNextMessage)
	}
}

// TestReplycopilotNormalPathProceedsWhenBudgetHasRoom verifies the happy
// path: the classifier is called and a normal interpretation is returned.
func TestReplycopilotNormalPathProceedsWhenBudgetHasRoom(t *testing.T) {
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	aibudget.SetGlobal(aibudget.New()) // fresh tracker, full budget

	cls := &budgetStubClassifier{}
	listing := models.Listing{
		ItemID:    "olxbg-2",
		Title:     "iPhone 13",
		Price:     50000,
		FairPrice: 60000,
	}
	rc := ReplyContext{
		SellerReply: "Yes I would consider 480 EUR for it",
	}

	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, cls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cls.called {
		t.Fatalf("classifier should be called when budget has room")
	}
	if result.Interpretation == InterpAIQuotaExhausted {
		t.Fatalf("expected normal interpretation, got AI quota exhausted")
	}
}
