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
