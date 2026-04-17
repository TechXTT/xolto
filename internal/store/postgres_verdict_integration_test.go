package store

// Postgres integration tests for recommended_action + risk_flags fields.
//
// These tests execute real SQL against a live Postgres instance and are gated
// on TEST_POSTGRES_DSN. They skip silently when the variable is unset.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://xolto:xoltotest@localhost:54320/xolto_test?sslmode=disable" \
//	  go test ./internal/store/ -run TestPostgres -v -timeout 60s

import (
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// insertVerdictFixtures inserts a set of listings with distinct
// recommended_action values so the integration test can assert round-trip
// persistence and correct query-time reading.
func insertVerdictFixtures(t *testing.T, st *PostgresStore, userID string) []models.ScoredListing {
	t.Helper()

	type fix struct {
		itemID    string
		condition string
		score     float64
		action    string
		riskFlags []string
	}
	fixtures := []fix{
		// buy: all BUY signals present
		{"vrd-buy-1", "good", 9.0, "buy", nil},
		// negotiate: price slightly above fair
		{"vrd-neg-1", "like_new", 7.0, "negotiate", nil},
		// ask_seller: confidence thin
		{"vrd-ask-1", "good", 6.0, "ask_seller", nil},
		// skip: red flags or overpriced
		{"vrd-skip-1", "fair", 4.0, "skip", []string{"anomaly_price"}},
	}

	var saved []models.ScoredListing
	for _, f := range fixtures {
		l := models.Listing{
			ItemID:        f.itemID,
			Title:         "Verdict " + f.itemID,
			Price:         50000,
			PriceType:     "fixed",
			MarketplaceID: "marktplaats",
			Condition:     f.condition,
		}
		scored := models.ScoredListing{
			Score:             f.score,
			OfferPrice:        40000,
			FairPrice:         50000,
			Confidence:        0.70,
			RecommendedAction: f.action,
			RiskFlags:         f.riskFlags,
		}
		if err := st.SaveListing(userID, l, "verdict test query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", f.itemID, err)
		}
		saved = append(saved, scored)
	}
	return saved
}

// TestPostgresRecommendedActionRoundTrip verifies that recommended_action and
// risk_flags survive a SaveListing → ListRecentListingsPaginated round-trip.
// This is the mandatory live-Postgres integration test per the
// feedback_postgres_integration_test_required guardrail.
func TestPostgresRecommendedActionRoundTrip(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertVerdictFixtures(t, st, userID)

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated error = %v", err)
	}
	if total < 4 {
		t.Fatalf("expected at least 4 listings, got total=%d", total)
	}

	// Index by itemID for O(1) lookup.
	byID := make(map[string]models.Listing, len(listings))
	for _, l := range listings {
		byID[l.ItemID] = l
	}

	// Assert each fixture round-tripped with the correct recommended_action.
	wantActions := map[string]string{
		"vrd-buy-1":  "buy",
		"vrd-neg-1":  "negotiate",
		"vrd-ask-1":  "ask_seller",
		"vrd-skip-1": "skip",
	}
	for itemID, wantAction := range wantActions {
		l, ok := byID[itemID]
		if !ok {
			t.Errorf("item %q not found in query result (got IDs: %v)", itemID, itemIDKeys(byID))
			continue
		}
		if l.RecommendedAction != wantAction {
			t.Errorf("item %q: RecommendedAction = %q, want %q", itemID, l.RecommendedAction, wantAction)
		}
		// risk_flags must be an array (may be empty) — never null.
		if l.RiskFlags == nil && itemID == "vrd-skip-1" {
			t.Errorf("item %q: RiskFlags is nil, want non-nil slice", itemID)
		}
	}
}

// TestPostgresVerdictFieldsOnGetListing verifies the GetListing single-row
// path also reads recommended_action correctly.
func TestPostgresVerdictFieldsOnGetListing(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "vrd-get-1",
		Title:         "Verdict GetListing test",
		Price:         60000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "new",
	}
	scored := models.ScoredListing{
		Score:             9.5,
		OfferPrice:        55000,
		FairPrice:         60000,
		Confidence:        0.80,
		RecommendedAction: "buy",
		RiskFlags:         nil,
	}
	if err := st.SaveListing(userID, l, "get listing test", scored); err != nil {
		t.Fatalf("SaveListing error = %v", err)
	}

	got, err := st.GetListing(userID, "vrd-get-1")
	if err != nil {
		t.Fatalf("GetListing error = %v", err)
	}
	if got == nil {
		t.Fatal("GetListing returned nil")
	}
	if got.RecommendedAction != "buy" {
		t.Errorf("GetListing: RecommendedAction = %q, want %q", got.RecommendedAction, "buy")
	}
	if got.RiskFlags == nil {
		// risk_flags defaults to '[]' in the DB, so it should unmarshal to an
		// empty (non-nil) slice.
		t.Errorf("GetListing: RiskFlags is nil, want empty slice")
	}
}

// TestPostgresVerdictDefaultFallback verifies that a listing saved without
// an explicit recommended_action (e.g. empty string in ScoredListing) reads
// back as "ask_seller" due to the COALESCE default.
func TestPostgresVerdictDefaultFallback(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "vrd-default-1",
		Title:         "Verdict default fallback test",
		Price:         50000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "good",
	}
	scored := models.ScoredListing{
		Score:             5.0,
		OfferPrice:        40000,
		RecommendedAction: "", // empty — should persist as default 'ask_seller'
	}
	if err := st.SaveListing(userID, l, "default test", scored); err != nil {
		t.Fatalf("SaveListing error = %v", err)
	}

	// Read back via paginated query to exercise the COALESCE path.
	listings, _, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated error = %v", err)
	}
	var found *models.Listing
	for i := range listings {
		if listings[i].ItemID == "vrd-default-1" {
			found = &listings[i]
			break
		}
	}
	if found == nil {
		t.Fatal("vrd-default-1 not found in results")
	}
	// Empty string stored → DB default 'ask_seller'; COALESCE returns 'ask_seller'.
	if found.RecommendedAction != "ask_seller" {
		t.Errorf("default fallback: RecommendedAction = %q, want %q", found.RecommendedAction, "ask_seller")
	}
}

// TestPostgresVerdictAllFourActions seeds one listing for each of the four
// action variants and asserts every item round-trips cleanly.
func TestPostgresVerdictAllFourActions(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	actions := []string{"buy", "negotiate", "ask_seller", "skip"}
	for i, action := range actions {
		l := models.Listing{
			ItemID:        "vrd-all4-" + action,
			Title:         "All-four " + action,
			Price:         50000 + i*1000,
			PriceType:     "fixed",
			MarketplaceID: "marktplaats",
			Condition:     "good",
		}
		scored := models.ScoredListing{
			Score:             float64(5 + i),
			OfferPrice:        40000,
			RecommendedAction: action,
		}
		if err := st.SaveListing(userID, l, "all-four test", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", action, err)
		}
	}

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated error = %v", err)
	}
	if total < len(actions) {
		t.Fatalf("expected at least %d listings, got %d", len(actions), total)
	}

	byID := make(map[string]models.Listing, len(listings))
	for _, l := range listings {
		byID[l.ItemID] = l
	}

	for _, action := range actions {
		itemID := "vrd-all4-" + action
		l, ok := byID[itemID]
		if !ok {
			t.Errorf("item %q not found", itemID)
			continue
		}
		if l.RecommendedAction != action {
			t.Errorf("item %q: got %q, want %q", itemID, l.RecommendedAction, action)
		}
	}
}

// itemIDKeys is a small helper to extract map keys for error messages.
func itemIDKeys(m map[string]models.Listing) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
