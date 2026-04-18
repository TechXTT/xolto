package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// TestMatchesOutreachStateEnvelope is the dual-envelope parity test (AC10).
//
// Scenario: user has one outreach thread on listing A and no thread on listing B.
// Expected:
//   - OutreachState is a populated struct on A under BOTH matches[] and items[].
//   - OutreachState is null on B under BOTH matches[] and items[].
//   - The payload for each item in matches[] is byte-equal to the corresponding
//     item in items[] (dual-envelope invariant).
func TestMatchesOutreachStateEnvelope(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	// Insert two listings: A and B.
	listingA := models.Listing{
		ItemID:        "listing-A",
		Title:         "OLX listing A",
		Price:         10000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}
	listingB := models.Listing{
		ItemID:        "listing-B",
		Title:         "OLX listing B",
		Price:         20000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}

	// Save listings under the correct userID.
	for _, l := range []models.Listing{listingA, listingB} {
		if err := st.SaveListing(userID, l, "test query", models.ScoredListing{Score: 7.0}); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", l.ItemID, err)
		}
	}

	// Record an outreach for listing A only via the API.
	postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id":     "listing-A",
		"marketplace_id": "olxbg",
		"draft_text":     "test draft",
		"draft_shape":    "buy",
		"draft_lang":     "bg",
	})

	// Fetch /matches.
	res := doMatchesRequest(t, srv, token, "limit=100&offset=0")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 from /matches, got %d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error = %v", err)
	}

	matchesRaw, ok := body["matches"].([]any)
	if !ok {
		t.Fatalf("expected matches[] array, got %T", body["matches"])
	}
	itemsRaw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected items[] array, got %T", body["items"])
	}

	if len(matchesRaw) != len(itemsRaw) {
		t.Fatalf("matches[] len=%d != items[] len=%d", len(matchesRaw), len(itemsRaw))
	}

	// Byte-equal invariant: every item in matches[] must equal the corresponding item in items[].
	for i := range matchesRaw {
		mBytes, _ := json.Marshal(matchesRaw[i])
		iBytes, _ := json.Marshal(itemsRaw[i])
		if !reflect.DeepEqual(matchesRaw[i], itemsRaw[i]) {
			t.Errorf("item[%d] mismatch: matches[]=%s items[]=%s", i, mBytes, iBytes)
		}
	}

	// Locate listing A and B in the response.
	var itemA, itemB map[string]any
	for _, raw := range matchesRaw {
		m, _ := raw.(map[string]any)
		switch m["ItemID"] {
		case "listing-A":
			itemA = m
		case "listing-B":
			itemB = m
		}
	}

	if itemA == nil {
		t.Fatal("listing-A not found in matches[] response")
	}
	if itemB == nil {
		t.Fatal("listing-B not found in matches[] response")
	}

	// listing A must have a non-null OutreachState.
	outreachA := itemA["OutreachState"]
	if outreachA == nil {
		t.Fatal("expected OutreachState to be non-null for listing-A (has a thread)")
	}
	envA, ok := outreachA.(map[string]any)
	if !ok {
		t.Fatalf("expected OutreachState to be an object for listing-A, got %T", outreachA)
	}
	if envA["state"] != "awaiting_reply" {
		t.Fatalf("expected OutreachState.state=awaiting_reply for listing-A, got %v", envA["state"])
	}

	// listing B must have null OutreachState.
	outreachB := itemB["OutreachState"]
	if outreachB != nil {
		t.Fatalf("expected OutreachState=null for listing-B (no thread), got %v", outreachB)
	}
}

// TestMatchesOutreachStateAppearsInBothKeys asserts that the same item
// appears verbatim in both matches[] and items[] (dual-envelope invariant).
// This is a simpler smoke-test complement to TestMatchesOutreachStateEnvelope.
func TestMatchesOutreachDualEnvelopeKeys(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	l := models.Listing{
		ItemID:        "dual-env-listing",
		Title:         "Dual envelope test",
		Price:         15000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}
	if err := st.SaveListing(userID, l, "test", models.ScoredListing{Score: 8.0}); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}
	// Add outreach thread.
	postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id": "dual-env-listing", "marketplace_id": "olxbg",
		"draft_text": "msg", "draft_shape": "buy", "draft_lang": "en",
	})

	res := doMatchesRequest(t, srv, token, "")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)

	matchesRaw, _ := body["matches"].([]any)
	itemsRaw, _ := body["items"].([]any)

	if len(matchesRaw) == 0 || len(itemsRaw) == 0 {
		t.Fatal("expected at least one item in matches[] and items[]")
	}

	// Top-level keys must be equal sets.
	for i := range matchesRaw {
		if i >= len(itemsRaw) {
			break
		}
		mMap, _ := matchesRaw[i].(map[string]any)
		iMap, _ := itemsRaw[i].(map[string]any)
		if mMap == nil || iMap == nil {
			continue
		}
		// Both must have the same set of top-level keys.
		for k := range mMap {
			if _, ok := iMap[k]; !ok {
				t.Errorf("key %q present in matches[%d] but missing from items[%d]", k, i, i)
			}
		}
		for k := range iMap {
			if _, ok := mMap[k]; !ok {
				t.Errorf("key %q present in items[%d] but missing from matches[%d]", k, i, i)
			}
		}
	}

	// OutreachState must be non-null in both envelopes for the listing with a thread.
	findByItemID := func(slice []any, id string) map[string]any {
		for _, raw := range slice {
			m, _ := raw.(map[string]any)
			if m["ItemID"] == id {
				return m
			}
		}
		return nil
	}

	mItem := findByItemID(matchesRaw, "dual-env-listing")
	iItem := findByItemID(itemsRaw, "dual-env-listing")

	if mItem == nil {
		t.Fatal("dual-env-listing not found in matches[]")
	}
	if iItem == nil {
		t.Fatal("dual-env-listing not found in items[]")
	}
	if mItem["OutreachState"] == nil {
		t.Fatal("expected OutreachState in matches[] for dual-env-listing")
	}
	if iItem["OutreachState"] == nil {
		t.Fatal("expected OutreachState in items[] for dual-env-listing")
	}

	// Values must be equal.
	mOS, _ := json.Marshal(mItem["OutreachState"])
	iOS, _ := json.Marshal(iItem["OutreachState"])
	if string(mOS) != string(iOS) {
		t.Errorf("OutreachState mismatch: matches[]=%s items[]=%s", mOS, iOS)
	}

	// HTTP status sanity.
	_ = http.StatusOK
}
