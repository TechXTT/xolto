package store

// Postgres integration tests for comparables_count + comparables_median_age_days.
//
// These tests execute real SQL against a live Postgres instance and are gated
// on TEST_POSTGRES_DSN. They skip silently when the variable is unset.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://xolto:xoltotest@localhost:54320/xolto_test?sslmode=disable" \
//	  go test ./internal/store/ -run TestPostgresComparables -v -timeout 60s

import (
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

// TestPostgresComparablesRoundTripGetListing verifies that ComparablesCount and
// ComparablesMedianAgeDays survive a SaveListing → GetListing round-trip.
func TestPostgresComparablesRoundTripGetListing(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "comp-get-1",
		Title:         "Comparables GetListing test",
		Price:         60000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "good",
	}
	scored := models.ScoredListing{
		Score:                    8.0,
		OfferPrice:               50000,
		FairPrice:                60000,
		Confidence:               0.75,
		RecommendedAction:        "buy",
		ComparablesCount:         7,
		ComparablesMedianAgeDays: 12,
	}
	if err := st.SaveListing(userID, l, "comp get test", scored); err != nil {
		t.Fatalf("SaveListing error = %v", err)
	}

	got, err := st.GetListing(userID, "comp-get-1")
	if err != nil {
		t.Fatalf("GetListing error = %v", err)
	}
	if got == nil {
		t.Fatal("GetListing returned nil")
	}
	if got.ComparablesCount != 7 {
		t.Errorf("GetListing: ComparablesCount = %d, want 7", got.ComparablesCount)
	}
	if got.ComparablesMedianAgeDays != 12 {
		t.Errorf("GetListing: ComparablesMedianAgeDays = %d, want 12", got.ComparablesMedianAgeDays)
	}
}

// TestPostgresComparablesRoundTripPaginated verifies that ComparablesCount and
// ComparablesMedianAgeDays survive a SaveListing → ListRecentListingsPaginated
// round-trip.
func TestPostgresComparablesRoundTripPaginated(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "comp-pag-1",
		Title:         "Comparables paginated test",
		Price:         50000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "like_new",
	}
	scored := models.ScoredListing{
		Score:                    7.5,
		OfferPrice:               42000,
		FairPrice:                50000,
		Confidence:               0.70,
		RecommendedAction:        "negotiate",
		ComparablesCount:         7,
		ComparablesMedianAgeDays: 12,
	}
	if err := st.SaveListing(userID, l, "comp pag test", scored); err != nil {
		t.Fatalf("SaveListing error = %v", err)
	}

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated error = %v", err)
	}
	if total < 1 {
		t.Fatalf("expected at least 1 listing, got total=%d", total)
	}

	var found *models.Listing
	for i := range listings {
		if listings[i].ItemID == "comp-pag-1" {
			found = &listings[i]
			break
		}
	}
	if found == nil {
		t.Fatal("comp-pag-1 not found in ListRecentListingsPaginated results")
	}
	if found.ComparablesCount != 7 {
		t.Errorf("paginated: ComparablesCount = %d, want 7", found.ComparablesCount)
	}
	if found.ComparablesMedianAgeDays != 12 {
		t.Errorf("paginated: ComparablesMedianAgeDays = %d, want 12", found.ComparablesMedianAgeDays)
	}
}

// TestPostgresComparablesZeroDefault verifies that a listing saved with zero
// comparables reads back with ComparablesCount=0 and ComparablesMedianAgeDays=0.
func TestPostgresComparablesZeroDefault(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "comp-zero-1",
		Title:         "Comparables zero test",
		Price:         40000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "fair",
	}
	scored := models.ScoredListing{
		Score:                    5.0,
		RecommendedAction:        "ask_seller",
		ComparablesCount:         0,
		ComparablesMedianAgeDays: 0,
	}
	if err := st.SaveListing(userID, l, "comp zero test", scored); err != nil {
		t.Fatalf("SaveListing error = %v", err)
	}

	got, err := st.GetListing(userID, "comp-zero-1")
	if err != nil {
		t.Fatalf("GetListing error = %v", err)
	}
	if got == nil {
		t.Fatal("GetListing returned nil")
	}
	if got.ComparablesCount != 0 {
		t.Errorf("zero: ComparablesCount = %d, want 0", got.ComparablesCount)
	}
	if got.ComparablesMedianAgeDays != 0 {
		t.Errorf("zero: ComparablesMedianAgeDays = %d, want 0", got.ComparablesMedianAgeDays)
	}
}

// TestPostgresGetListingScoringStateComparablesCount verifies that
// GetListingScoringState returns the stored comparables_count so the worker
// can detect stale rows (count=0) and force a re-score (XOL-17 regression).
func TestPostgresGetListingScoringStateComparablesCount(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	l := models.Listing{
		ItemID:        "pg-sc-count-1",
		Title:         "ThinkPad X1 Carbon scoring-state count test",
		Price:         35000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "good",
	}

	// Save with comparables_count=0 (simulates a pre-XOL-16 row).
	scoredZero := models.ScoredListing{
		Score:                    6.5,
		ReasoningSource:          "ai",
		RecommendedAction:        "ask_seller",
		ComparablesCount:         0,
		ComparablesMedianAgeDays: 0,
	}
	if err := st.SaveListing(userID, l, "thinkpad x1 pg", scoredZero); err != nil {
		t.Fatalf("SaveListing(zero) error = %v", err)
	}

	_, _, count, found, err := st.GetListingScoringState(userID, "pg-sc-count-1")
	if err != nil {
		t.Fatalf("GetListingScoringState() error = %v", err)
	}
	if !found {
		t.Fatal("expected scoring state to be found")
	}
	if count != 0 {
		t.Fatalf("expected comparablesCount=0 (stale row), got %d", count)
	}

	// Re-save with comparables_count=15 (post-XOL-16 re-score).
	scoredPopulated := models.ScoredListing{
		Score:                    7.5,
		ReasoningSource:          "ai",
		RecommendedAction:        "negotiate",
		ComparablesCount:         15,
		ComparablesMedianAgeDays: 9,
	}
	if err := st.SaveListing(userID, l, "thinkpad x1 pg", scoredPopulated); err != nil {
		t.Fatalf("SaveListing(populated) error = %v", err)
	}

	_, _, count, found, err = st.GetListingScoringState(userID, "pg-sc-count-1")
	if err != nil {
		t.Fatalf("GetListingScoringState(populated) error = %v", err)
	}
	if !found {
		t.Fatal("expected scoring state to be found after re-save")
	}
	if count != 15 {
		t.Fatalf("expected comparablesCount=15 after re-score, got %d", count)
	}
}

// TestPostgresGetComparableDealsUsesFirstSeen verifies the full pipeline:
// score → SaveListing → backdate first_seen → TouchListing (worker keeps-alive) →
// GetComparableDeals → assert LastSeen reflects first_seen, not last_seen.
//
// This is the XOL-17 Postgres integration gate: if GetComparableDeals reverts to
// selecting last_seen, the age will be ~0 (touched seconds ago) and the test
// will fail because ageDays < 9.
func TestPostgresGetComparableDealsUsesFirstSeen(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)

	// Insert anchor listing (the one being scored — excluded from comparable lookup).
	anchor := models.Listing{
		ItemID:        "pg-fs-anchor",
		Title:         "ThinkPad X1 Carbon anchor pg",
		Price:         35000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "good",
	}
	if err := st.SaveListing(userID, anchor, "thinkpad x1 carbon pg", models.ScoredListing{Score: 7.0, RecommendedAction: "ask_seller"}); err != nil {
		t.Fatalf("SaveListing(anchor) error = %v", err)
	}

	// Insert a comparable listing.
	comp := models.Listing{
		ItemID:        "pg-fs-comp1",
		Title:         "ThinkPad X1 Carbon comp pg",
		Price:         33000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		Condition:     "good",
	}
	if err := st.SaveListing(userID, comp, "thinkpad x1 carbon pg", models.ScoredListing{Score: 6.5, RecommendedAction: "ask_seller"}); err != nil {
		t.Fatalf("SaveListing(comp) error = %v", err)
	}

	// Backdate first_seen to 10 days ago while leaving last_seen at NOW().
	// This mirrors real prod behaviour: the listing was discovered 10 days ago
	// but the worker keeps calling TouchListing to update last_seen.
	_, updErr := st.db.Exec(
		`UPDATE listings SET first_seen = NOW() - INTERVAL '10 days' WHERE item_id = $1`,
		scopedItemID(userID, "pg-fs-comp1"),
	)
	if updErr != nil {
		t.Fatalf("UPDATE first_seen error = %v", updErr)
	}

	// TouchListing advances last_seen to NOW().
	if err := st.TouchListing(userID, "pg-fs-comp1"); err != nil {
		t.Fatalf("TouchListing() error = %v", err)
	}

	deals, err := st.GetComparableDeals(userID, "thinkpad x1 carbon pg", "pg-fs-anchor", 10)
	if err != nil {
		t.Fatalf("GetComparableDeals() error = %v", err)
	}
	if len(deals) == 0 {
		t.Fatal("expected at least 1 comparable deal")
	}

	var found *models.ComparableDeal
	for i := range deals {
		if deals[i].ItemID == "pg-fs-comp1" {
			found = &deals[i]
			break
		}
	}
	if found == nil {
		t.Fatal("pg-fs-comp1 not found in comparable deals")
	}

	ageDays := int(time.Since(found.LastSeen).Hours() / 24)
	if ageDays < 9 || ageDays > 11 {
		t.Fatalf(
			"expected comparable age ~10 days (from first_seen), got %d days — "+
				"GetComparableDeals must SELECT first_seen, not last_seen (XOL-17 regression)",
			ageDays,
		)
	}
}
