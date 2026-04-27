package billing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// knownTierSlugs enumerates every internal slug that TierDisplayName
// recognises (i.e. has a non-default switch arm for). When TierDisplayName
// gains or loses a case, this list MUST be updated in lockstep with
// testdata/tier_display_labels.json.
//
// CANONICAL SOURCE: TierDisplayName in limits.go (same package).
// Frontends mirror this snapshot — see W19-11.
var knownTierSlugs = []string{"free", "pro", "power"}

const drift_failure_message = "If you changed TierDisplayName, regenerate testdata/tier_display_labels.json AND update mirrored copies in xolto-landing + xolto-app per W18-4 / W19-11."

// TestTierDisplayLabelsJSONMatchesCanonical asserts that the JSON snapshot
// at testdata/tier_display_labels.json agrees with TierDisplayName for every
// (slug → expected) pair, and that the JSON covers every slug TierDisplayName
// knows about.
//
// The JSON snapshot is the contract that frontends (xolto-app, xolto-landing)
// mirror. If this test fails, drift has been introduced — fix the JSON or the
// switch, and update mirrored copies in lockstep.
func TestTierDisplayLabelsJSONMatchesCanonical(t *testing.T) {
	path := filepath.Join("testdata", "tier_display_labels.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var snapshot map[string]string
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}

	// _canonical_source is metadata — strip it before comparing slug sets.
	const traceabilityKey = "_canonical_source"
	expectedTraceability := "markt/internal/billing/limits.go::TierDisplayName"
	if got, ok := snapshot[traceabilityKey]; !ok {
		t.Errorf("%s: missing %q traceability field", path, traceabilityKey)
	} else if got != expectedTraceability {
		t.Errorf("%s: %q = %q, want %q", path, traceabilityKey, got, expectedTraceability)
	}
	delete(snapshot, traceabilityKey)

	// Forward direction: every (slug → expected) pair in the JSON must match
	// what TierDisplayName returns.
	for slug, expected := range snapshot {
		got := TierDisplayName(slug)
		if got != expected {
			t.Errorf("TierDisplayName(%q) = %q, want %q (per %s).\n%s",
				slug, got, expected, path, drift_failure_message)
		}
	}

	// Inverse direction: the JSON must enumerate every slug TierDisplayName
	// recognises. This catches the case where a new switch arm is added to
	// TierDisplayName but the JSON snapshot is not updated.
	jsonSlugs := make([]string, 0, len(snapshot))
	for slug := range snapshot {
		jsonSlugs = append(jsonSlugs, slug)
	}
	sort.Strings(jsonSlugs)

	expectedSlugs := append([]string(nil), knownTierSlugs...)
	sort.Strings(expectedSlugs)

	if !equalStringSlices(jsonSlugs, expectedSlugs) {
		t.Errorf("JSON snapshot slug set = %v, want %v (per knownTierSlugs).\n%s",
			jsonSlugs, expectedSlugs, drift_failure_message)
	}

	// Sanity: every slug in knownTierSlugs must produce a non-default
	// (non-"Free") label, OR be exactly the literal "free" slug. This guards
	// against the test silently rubber-stamping the default fallback.
	for _, slug := range knownTierSlugs {
		got := TierDisplayName(slug)
		if got == "" {
			t.Errorf("TierDisplayName(%q) returned empty string", slug)
		}
		if slug != "free" && got == "Free" {
			t.Errorf("TierDisplayName(%q) = %q (default fallback); slug should have a dedicated case.\n%s",
				slug, got, drift_failure_message)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
