package scorer

import (
	"context"
	"errors"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// ---------------------------------------------------------------------------
// Mock MustHaveEvaluator for semantic tests
// ---------------------------------------------------------------------------

// mockEvaluator is a test-only implementation of MustHaveEvaluator.
// It records every call and returns the configured response or error.
type mockEvaluator struct {
	callCount int
	// lastInput captures the must-haves passed in the most recent Evaluate call.
	lastUnknowns []string
	// response is returned on success.
	response map[string]string
	// err is returned when non-nil.
	err error
}

func (m *mockEvaluator) Evaluate(_ context.Context, _ string, _ int64, _ string, _, _ string, unknowns []string) (map[string]string, error) {
	m.callCount++
	m.lastUnknowns = append([]string(nil), unknowns...)
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// Ensure mockEvaluator satisfies MustHaveEvaluator at compile time.
var _ MustHaveEvaluator = (*mockEvaluator)(nil)

// TestScoreMustHavesEmptyList verifies that a mission with zero must-haves
// returns an empty non-nil slice (never null in JSON).
func TestScoreMustHavesEmptyList(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-1",
		Title:       "Sony A7 III body",
		Description: "Great condition, battery health 95%",
	}
	result := ScoreMustHaves(listing, nil)
	if result == nil {
		t.Fatal("ScoreMustHaves(nil must-haves) returned nil; want non-nil empty slice")
	}
	if len(result) != 0 {
		t.Fatalf("ScoreMustHaves(nil must-haves): expected len=0, got %d: %v", len(result), result)
	}

	result2 := ScoreMustHaves(listing, []string{})
	if result2 == nil {
		t.Fatal("ScoreMustHaves(empty must-haves) returned nil; want non-nil empty slice")
	}
	if len(result2) != 0 {
		t.Fatalf("ScoreMustHaves(empty must-haves): expected len=0, got %d: %v", len(result2), result2)
	}
}

// TestScoreMustHavesStatusMet verifies that must-haves whose keywords appear
// in the listing title or description receive status "met".
func TestScoreMustHavesStatusMet(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-2",
		Title:       "Sony A7 III, NL seller",
		Description: "Battery health 87%, no dead pixels confirmed, original box included.",
	}
	cases := []struct {
		mustHave string
		wantMet  bool
	}{
		{"NL seller", true},
		{"dead pixels", true},  // strip leading "no" → match on "dead pixels"
		{"battery health", true},
		{"original box", true},
	}
	for _, tc := range cases {
		matches := ScoreMustHaves(listing, []string{tc.mustHave})
		if len(matches) != 1 {
			t.Fatalf("must-have %q: expected 1 result, got %d", tc.mustHave, len(matches))
		}
		got := matches[0].Status
		if tc.wantMet && got != MustHaveStatusMet {
			t.Errorf("must-have %q: expected %q, got %q", tc.mustHave, MustHaveStatusMet, got)
		}
		if !tc.wantMet && got == MustHaveStatusMet {
			t.Errorf("must-have %q: expected not-met, got %q", tc.mustHave, got)
		}
	}
}

// TestScoreMustHavesStatusUnknown verifies that must-haves with no matching
// signal in the listing receive status "unknown".
func TestScoreMustHavesStatusUnknown(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-3",
		Title:       "iPhone 14 Pro, 128GB",
		Description: "Selling my phone, works perfectly.",
	}
	mustHaves := []string{
		"battery >=80%",
		"original charger",
		"NL seller",
	}
	results := ScoreMustHaves(listing, mustHaves)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != MustHaveStatusUnknown {
			t.Errorf("must-have %q: expected %q, got %q", r.Text, MustHaveStatusUnknown, r.Status)
		}
	}
}

// TestScoreMustHavesStatusMissed verifies that the "missed" status constant
// exists and has the correct value. The current implementation does not emit
// "missed" from text matching (conservative approach to avoid false exclusions),
// but the constant must be defined for future semantic matching.
func TestScoreMustHavesStatusMissed(t *testing.T) {
	// Verify the constant is defined with the exact required value.
	if MustHaveStatusMissed != "missed" {
		t.Fatalf("MustHaveStatusMissed = %q, want %q", MustHaveStatusMissed, "missed")
	}
	// Verify the other status constants match the spec too.
	if MustHaveStatusMet != "met" {
		t.Fatalf("MustHaveStatusMet = %q, want %q", MustHaveStatusMet, "met")
	}
	if MustHaveStatusUnknown != "unknown" {
		t.Fatalf("MustHaveStatusUnknown = %q, want %q", MustHaveStatusUnknown, "unknown")
	}
}

// TestScoreMustHavesNilSafeDescription verifies that a listing with empty
// Title and Description does not panic and returns "unknown" for all must-haves.
func TestScoreMustHavesNilSafeDescription(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-4",
		Title:       "",
		Description: "",
	}
	mustHaves := []string{"battery health", "NL seller", "no scratches"}
	results := ScoreMustHaves(listing, mustHaves)
	if len(results) != len(mustHaves) {
		t.Fatalf("expected %d results, got %d", len(mustHaves), len(results))
	}
	for _, r := range results {
		if r.Status != MustHaveStatusUnknown {
			t.Errorf("must-have %q on empty listing: expected %q, got %q", r.Text, MustHaveStatusUnknown, r.Status)
		}
	}
}

// TestScoreMustHavesPreservesOrder verifies that the output slice preserves
// the source order of must-haves (never alphabetised or reordered).
func TestScoreMustHavesPreservesOrder(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-5",
		Title:       "Camera body, pixel check passed",
		Description: "NL seller, fast shipping",
	}
	mustHaves := []string{
		"NL seller",
		"pixel check",
		"original box",
		"battery health",
	}
	results := ScoreMustHaves(listing, mustHaves)
	if len(results) != len(mustHaves) {
		t.Fatalf("expected %d results, got %d", len(mustHaves), len(results))
	}
	for i, r := range results {
		if r.Text != mustHaves[i] {
			t.Errorf("position %d: expected Text=%q, got %q (order not preserved)", i, mustHaves[i], r.Text)
		}
	}
}

// TestScoreMustHavesCaseInsensitive verifies that matching is case-insensitive
// so "NL Seller" matches "nl seller" in the description.
func TestScoreMustHavesCaseInsensitive(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-6",
		Title:       "MacBook Pro",
		Description: "NL SELLER, battery health 91%, grade A screen",
	}
	results := ScoreMustHaves(listing, []string{"nl seller", "BATTERY HEALTH", "Grade A"})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != MustHaveStatusMet {
			t.Errorf("must-have %q: expected %q (case-insensitive), got %q", r.Text, MustHaveStatusMet, r.Status)
		}
	}
}

// TestScoreMustHavesMixedStatuses verifies a realistic mix of met and unknown
// statuses in a single call.
func TestScoreMustHavesMixedStatuses(t *testing.T) {
	listing := models.Listing{
		ItemID:      "test-7",
		Title:       "iPhone 13, NL seller, 256GB",
		Description: "Battery health 88%, minor scuffs on the back.",
	}
	cases := []struct {
		text   string
		status string
	}{
		{"NL seller", MustHaveStatusMet},
		{"battery >=80%", MustHaveStatusUnknown}, // >=80% not literally in text
		{"battery health", MustHaveStatusMet},
		{"original box", MustHaveStatusUnknown},
	}
	var mustHaves []string
	for _, c := range cases {
		mustHaves = append(mustHaves, c.text)
	}
	results := ScoreMustHaves(listing, mustHaves)
	if len(results) != len(cases) {
		t.Fatalf("expected %d results, got %d", len(cases), len(results))
	}
	for i, r := range results {
		if r.Status != cases[i].status {
			t.Errorf("must-have[%d] %q: expected %q, got %q", i, r.Text, cases[i].status, r.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// ScoreMustHavesSemantic tests
// ---------------------------------------------------------------------------

// TestSemanticAllMet verifies that when every tokenizer result is "met",
// the evaluator is never called (cost guard).
func TestSemanticAllMet(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-1",
		Title:       "iPhone 13, NL seller, 256GB, battery health 92%",
		Description: "Original charger included. Grade A screen.",
	}
	mustHaves := []string{"NL seller", "battery health", "original charger"}
	mock := &mockEvaluator{response: map[string]string{}}

	results := ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 42, mock)
	if mock.callCount != 0 {
		t.Errorf("expected 0 evaluator calls when all tokenizer results are met, got %d", mock.callCount)
	}
	for _, r := range results {
		if r.Status != MustHaveStatusMet {
			t.Errorf("must-have %q: expected %q, got %q", r.Text, MustHaveStatusMet, r.Status)
		}
	}
}

// TestSemanticNilEvaluator verifies that a nil evaluator behaves identically
// to the tokenizer-only path.
func TestSemanticNilEvaluator(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-nil",
		Title:       "Sony A7",
		Description: "no battery info",
	}
	mustHaves := []string{"battery health >=90%", "original box"}
	tokenizerOnly := ScoreMustHaves(listing, mustHaves)
	withNil := ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 0, nil)

	if len(tokenizerOnly) != len(withNil) {
		t.Fatalf("len mismatch: tokenizer=%d, semantic-nil=%d", len(tokenizerOnly), len(withNil))
	}
	for i := range tokenizerOnly {
		if tokenizerOnly[i] != withNil[i] {
			t.Errorf("slot %d: tokenizer=%v semantic-nil=%v", i, tokenizerOnly[i], withNil[i])
		}
	}
}

// TestSemanticUnknownSubsetForwarded verifies that when the tokenizer returns
// a mix of "met" and "unknown", only the "unknown" subset is forwarded to the
// evaluator.
func TestSemanticUnknownSubsetForwarded(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-2",
		Title:       "MacBook Pro, NL seller",
		Description: "Minor wear.",
	}
	mustHaves := []string{"NL seller", "battery health >=80%", "original charger"}
	// Tokenizer: "NL seller" → met, others → unknown.
	mock := &mockEvaluator{response: map[string]string{
		"battery health >=80%": "unknown",
		"original charger":     "unknown",
	}}

	ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 1, mock)

	if mock.callCount != 1 {
		t.Fatalf("expected 1 evaluator call, got %d", mock.callCount)
	}
	// Evaluator must receive only the unknown subset, not "NL seller".
	for _, mh := range mock.lastUnknowns {
		if mh == "NL seller" {
			t.Errorf("evaluator received already-met must-have %q", mh)
		}
	}
	if len(mock.lastUnknowns) != 2 {
		t.Errorf("expected 2 unknown must-haves forwarded, got %d: %v", len(mock.lastUnknowns), mock.lastUnknowns)
	}
}

// TestSemanticMissedEmitted verifies that when the evaluator returns "missed",
// the output slice contains "missed" in the correct slot and order is preserved.
// Must-haves are chosen so the tokenizer returns "unknown" (no literal keyword
// match), leaving the evaluator to supply the verdict.
func TestSemanticMissedEmitted(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-3",
		Title:       "iPhone 12",
		Description: "Selling fast, no info about battery.",
	}
	// "battery >=80%" and "warranty included" — neither appears literally in
	// title/description so tokenizer will return "unknown" for both.
	// "NL seller" also unknown.
	mustHaves := []string{"battery >=80%", "NL seller", "warranty included"}
	mock := &mockEvaluator{response: map[string]string{
		"battery >=80%":    "missed",
		"NL seller":        "unknown",
		"warranty included": "missed",
	}}

	results := ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 5, mock)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Verify order preserved.
	for i, mh := range mustHaves {
		if results[i].Text != mh {
			t.Errorf("slot %d: expected text=%q, got %q", i, mh, results[i].Text)
		}
	}
	if results[0].Status != MustHaveStatusMissed {
		t.Errorf("slot 0 (battery >=80%%): expected missed, got %q", results[0].Status)
	}
	if results[1].Status != MustHaveStatusUnknown {
		t.Errorf("slot 1 (NL seller): expected unknown, got %q", results[1].Status)
	}
	if results[2].Status != MustHaveStatusMissed {
		t.Errorf("slot 2 (warranty included): expected missed, got %q", results[2].Status)
	}
}

// TestSemanticEvaluatorError verifies that on evaluator error, the result
// equals the tokenizer-only output (no "missed" emitted on failure).
func TestSemanticEvaluatorError(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-4",
		Title:       "Pixel 6",
		Description: "Selling fast.",
	}
	mustHaves := []string{"battery health >=90%", "original box"}
	mock := &mockEvaluator{err: errors.New("LLM timeout")}

	tokenizerOnly := ScoreMustHaves(listing, mustHaves)
	semanticResult := ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 9, mock)

	if len(tokenizerOnly) != len(semanticResult) {
		t.Fatalf("len mismatch on error fallback: expected %d, got %d", len(tokenizerOnly), len(semanticResult))
	}
	for i := range tokenizerOnly {
		if tokenizerOnly[i] != semanticResult[i] {
			t.Errorf("slot %d: tokenizer=%v semantic-error=%v", i, tokenizerOnly[i], semanticResult[i])
		}
	}
	// No "missed" should appear on error.
	for _, r := range semanticResult {
		if r.Status == MustHaveStatusMissed {
			t.Errorf("got unexpected %q status after evaluator error", MustHaveStatusMissed)
		}
	}
}

// TestSemanticEmptyMustHaves verifies that an empty must-haves slice returns
// an empty non-nil slice without invoking the evaluator.
func TestSemanticEmptyMustHaves(t *testing.T) {
	listing := models.Listing{ItemID: "sem-5", Title: "Sony RX100", Description: ""}
	mock := &mockEvaluator{response: map[string]string{}}

	results := ScoreMustHavesSemantic(context.Background(), listing, nil, 0, mock)
	if results == nil {
		t.Fatal("expected non-nil slice for empty must-haves, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
	if mock.callCount != 0 {
		t.Errorf("expected 0 evaluator calls for empty must-haves, got %d", mock.callCount)
	}
}

// TestSemanticMetFromLLM verifies that the LLM can upgrade "unknown" to "met".
func TestSemanticMetFromLLM(t *testing.T) {
	listing := models.Listing{
		ItemID:      "sem-6",
		Title:       "Sony A7 III",
		Description: "батерия 95%, продавач от София",
	}
	mustHaves := []string{"battery >=90%", "BG seller"}
	// Tokenizer won't match these; LLM confirms both.
	mock := &mockEvaluator{response: map[string]string{
		"battery >=90%": "met",
		"BG seller":     "met",
	}}

	results := ScoreMustHavesSemantic(context.Background(), listing, mustHaves, 10, mock)
	for _, r := range results {
		if r.Status != MustHaveStatusMet {
			t.Errorf("must-have %q: expected %q after LLM upgrade, got %q", r.Text, MustHaveStatusMet, r.Status)
		}
	}
}
