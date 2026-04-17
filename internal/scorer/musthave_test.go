package scorer

import (
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

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
