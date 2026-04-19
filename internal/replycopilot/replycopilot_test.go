package replycopilot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

type stubClassifier struct {
	result ClassifyResult
	err    error
	called bool
}

func (s *stubClassifier) Classify(_ context.Context, _ string) (ClassifyResult, error) {
	s.called = true
	return s.result, s.err
}

func bgListing() models.Listing {
	return models.Listing{
		Title:       "Продавам Sony A7 IV",
		Description: "Употребяван фотоапарат в добро состояние.",
		Price:       180000,
		FairPrice:   160000,
	}
}

func enListing() models.Listing {
	return models.Listing{
		Title:     "Sony A7 IV",
		Price:     180000,
		FairPrice: 160000,
	}
}

func TestShortReply_LowSignal_NoLLMCall(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{Interpretation: InterpNegotiable, Confidence: ConfHigh}}
	rc := ReplyContext{SellerReply: "ok"}
	result, err := Interpret(context.Background(), rc, enListing(), MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpretation != InterpLowSignal {
		t.Errorf("expected low_signal, got %q", result.Interpretation)
	}
	if result.RecommendedAction != ActionAskSeller {
		t.Errorf("expected ask_seller, got %q", result.RecommendedAction)
	}
	if stub.called {
		t.Error("stub classifier should NOT have been called for short reply")
	}
	found := false
	for _, sig := range result.Signals {
		if sig == "too_short" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'too_short' in signals, got %v", result.Signals)
	}
}

func TestLLMError_FallbackLowSignal(t *testing.T) {
	stub := &stubClassifier{err: fmt.Errorf("timeout")}
	rc := ReplyContext{SellerReply: "I can do 1600 euros for you."}
	result, err := Interpret(context.Background(), rc, enListing(), MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpretation != InterpLowSignal {
		t.Errorf("expected low_signal fallback on LLM error, got %q", result.Interpretation)
	}
	if result.RecommendedAction != ActionAskSeller {
		t.Errorf("expected ask_seller on fallback, got %q", result.RecommendedAction)
	}
}

func TestUnknownInterpretation_ForcedLowSignal(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{Interpretation: "unknown_value", Confidence: ConfHigh}}
	rc := ReplyContext{SellerReply: "The price is not negotiable at all."}
	result, err := Interpret(context.Background(), rc, enListing(), MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpretation != InterpLowSignal {
		t.Errorf("expected unknown interpretation forced to low_signal, got %q", result.Interpretation)
	}
}

func TestLowConfidence_ForcedLowSignal(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{Interpretation: InterpNegotiable, Confidence: ConfLow}}
	rc := ReplyContext{SellerReply: "Maybe we can talk about it later sometime."}
	result, err := Interpret(context.Background(), rc, enListing(), MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpretation != InterpLowSignal {
		t.Errorf("expected low confidence to force low_signal, got %q", result.Interpretation)
	}
}

func TestRiskySignal_ForcesRiskyAndSkip(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpNegotiable,
		Confidence:     ConfHigh,
		Signals:        []string{"other_buyer_claimed"},
	}}
	rc := ReplyContext{SellerReply: "I have another buyer coming tomorrow, decide fast!"}
	result, err := Interpret(context.Background(), rc, enListing(), MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpretation != InterpRisky {
		t.Errorf("expected risky, got %q", result.Interpretation)
	}
	if result.RecommendedAction != ActionSkip {
		t.Errorf("expected skip for risky, got %q", result.RecommendedAction)
	}
}

func TestNegotiable_HighConfidence_PriceFarBelowFair_Counter(t *testing.T) {
	// Price 1800 EUR, fair 1600 EUR → price > 1.05*fair(1680), so it's above.
	// Actually 1800 > 1680 → firm+high would skip, but negotiable+high → counter still.
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpNegotiable,
		Confidence:     ConfHigh,
		Signals:        []string{},
	}}
	listing := models.Listing{
		Title:     "Sony A7 IV",
		Price:     180000, // 1800 EUR
		FairPrice: 160000, // 1600 EUR; 1.05*1600=1680; price(1800)>1680
	}
	rc := ReplyContext{
		SellerReply:   "I could come down a little bit on the price, what did you have in mind?",
		OurOfferPrice: 0,
	}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// negotiable + high confidence: accept only if price<=1.05*fair; 1800>1680 → counter
	if result.RecommendedAction != ActionCounter {
		t.Errorf("expected counter, got %q", result.RecommendedAction)
	}
	if result.OfferPrice == 0 {
		t.Error("expected non-zero offer price for counter action")
	}
}

func TestFirm_HighConfidence_PriceWithinFair_Accept(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpFirm,
		Confidence:     ConfHigh,
		Signals:        []string{},
	}}
	listing := models.Listing{
		Title:     "Laptop",
		Price:     100000, // 1000 EUR
		FairPrice: 100000, // 1000 EUR; 1.05*1000=1050; price(1000)<=1050
	}
	rc := ReplyContext{SellerReply: "This is my final price, not going lower."}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecommendedAction != ActionAccept {
		t.Errorf("expected accept, got %q", result.RecommendedAction)
	}
	if result.OfferPrice != 0 {
		t.Errorf("expected offer_price omitted (0) for accept action, got %d", result.OfferPrice)
	}
}

func TestFirm_HighConfidence_PriceAboveFair_Skip(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpFirm,
		Confidence:     ConfHigh,
		Signals:        []string{},
	}}
	listing := models.Listing{
		Title:     "Camera",
		Price:     200000, // 2000 EUR
		FairPrice: 150000, // 1500 EUR; 1.05*1500=1575; price(2000)>1575
	}
	rc := ReplyContext{SellerReply: "The price is firm, no negotiation."}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecommendedAction != ActionSkip {
		t.Errorf("expected skip for firm + price above fair, got %q", result.RecommendedAction)
	}
}

func TestCounterOffer_NoOurOffer_FairPrice90Pct(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpNegotiable,
		Confidence:     ConfMedium,
		Signals:        []string{},
	}}
	listing := models.Listing{
		Title:     "Laptop",
		Price:     120000, // above 1.05*fair(10000)=10500 → won't trigger accept
		FairPrice: 10000,  // 100 EUR
	}
	rc := ReplyContext{
		SellerReply:   "Let me know if you want to negotiate.",
		OurOfferPrice: 0,
	}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecommendedAction != ActionCounter {
		t.Errorf("expected counter, got %q", result.RecommendedAction)
	}
	expected := int(float64(10000) * 0.90) // 9000
	if result.OfferPrice != expected {
		t.Errorf("expected offer_price=%d (90%% of fair), got %d", expected, result.OfferPrice)
	}
}

func TestCounterOffer_WithOurOffer_Midpoint(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpNegotiable,
		Confidence:     ConfMedium,
		Signals:        []string{},
	}}
	listing := models.Listing{
		Title:     "Camera",
		Price:     120000,
		FairPrice: 10000, // 100 EUR
	}
	rc := ReplyContext{
		SellerReply:   "I can come down on the price.",
		OurOfferPrice: 8000, // 80 EUR
	}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RecommendedAction != ActionCounter {
		t.Errorf("expected counter, got %q", result.RecommendedAction)
	}
	// midpoint = (8000 + 10000) / 2 = 9000
	if result.OfferPrice != 9000 {
		t.Errorf("expected offer_price=9000 (midpoint), got %d", result.OfferPrice)
	}
}

func TestBGListing_DraftContainsBulgarian(t *testing.T) {
	stub := &stubClassifier{result: ClassifyResult{
		Interpretation: InterpLowSignal,
		Confidence:     ConfLow,
		Signals:        []string{},
	}}
	listing := bgListing()
	rc := ReplyContext{SellerReply: "Цената е фиксирана."}
	result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Lang != "bg" {
		t.Errorf("expected lang=bg for Cyrillic reply, got %q", result.Lang)
	}
	if !strings.Contains(result.DraftNextMessage, "Благодаря") && !strings.Contains(result.DraftNextMessage, "Здравейте") &&
		!strings.Contains(result.DraftNextMessage, "Разбирам") && !strings.Contains(result.DraftNextMessage, "Чудесно") {
		t.Errorf("expected Bulgarian text in draft, got %q", result.DraftNextMessage)
	}
}

// TestOfferPriceAbsentForNonCounter ensures offer_price is 0 (omitted) for
// accept/skip/ask_seller actions.
func TestOfferPriceAbsentForNonCounter(t *testing.T) {
	cases := []struct {
		name   string
		interp Interpretation
		conf   Confidence
	}{
		{"accept", InterpFirm, ConfHigh},
		{"ask_seller_low_signal", InterpLowSignal, ConfLow},
	}

	for _, c := range cases {
		stub := &stubClassifier{result: ClassifyResult{
			Interpretation: c.interp,
			Confidence:     c.conf,
			Signals:        []string{},
		}}
		listing := models.Listing{
			Title:     "Camera",
			Price:     100000,
			FairPrice: 100000,
		}
		rc := ReplyContext{SellerReply: "This is my final price, I will not go lower at all."}
		result, err := Interpret(context.Background(), rc, listing, MissionContext{}, stub)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if result.OfferPrice != 0 {
			t.Errorf("%s: expected offer_price=0 (absent), got %d", c.name, result.OfferPrice)
		}
	}
}
