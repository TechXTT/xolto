package store

import (
	"path/filepath"
	"testing"

	"github.com/TechXTT/marktbot/internal/models"
)

func TestShoppingProfileAndShortlistPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "marktbot-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	profileID, err := st.UpsertShoppingProfile(models.ShoppingProfile{
		UserID:             "u1",
		Name:               "Sony A7 III",
		TargetQuery:        "sony a7 iii",
		CategoryID:         487,
		BudgetMax:          1000,
		BudgetStretch:      1100,
		PreferredCondition: []string{"Gebruikt", "Zo goed als nieuw"},
		SearchQueries:      []string{"sony a7 iii", "sony alpha 7 iii"},
		Active:             true,
	})
	if err != nil {
		t.Fatalf("UpsertShoppingProfile() error = %v", err)
	}

	profile, err := st.GetActiveShoppingProfile("u1")
	if err != nil {
		t.Fatalf("GetActiveShoppingProfile() error = %v", err)
	}
	if profile == nil || profile.ID != profileID {
		t.Fatalf("expected active profile id %d, got %#v", profileID, profile)
	}

	err = st.SaveShortlistEntry(models.ShortlistEntry{
		UserID:              "u1",
		ProfileID:           profileID,
		ItemID:              "m1",
		Title:               "Sony A7 III",
		URL:                 "https://example.com/listing",
		RecommendationLabel: models.RecommendationWatch,
		RecommendationScore: 7.5,
		AskPrice:            90000,
		FairPrice:           95000,
		Verdict:             "worth watching",
		Concerns:            []string{"ask about shutter count"},
		SuggestedQuestions:  []string{"Wat is de shutter count?"},
		Status:              "watching",
	})
	if err != nil {
		t.Fatalf("SaveShortlistEntry() error = %v", err)
	}

	entry, err := st.GetShortlistEntry("u1", "m1")
	if err != nil {
		t.Fatalf("GetShortlistEntry() error = %v", err)
	}
	if entry == nil || entry.Title != "Sony A7 III" {
		t.Fatalf("expected shortlist entry, got %#v", entry)
	}
}

func TestListingQueriesAreScopedPerUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "marktbot-scope-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	listingA := models.Listing{ItemID: "m1", Title: "Sony A7 III", Price: 100000, PriceType: "fixed"}
	listingB := models.Listing{ItemID: "m2", Title: "Sony A6400", Price: 80000, PriceType: "fixed"}

	if err := st.SaveListing("u1", listingA, "sony camera", models.ScoredListing{Score: 8.5}); err != nil {
		t.Fatalf("SaveListing(u1, m1) error = %v", err)
	}
	if err := st.SaveListing("u1", listingB, "sony camera", models.ScoredListing{Score: 7.8}); err != nil {
		t.Fatalf("SaveListing(u1, m2) error = %v", err)
	}
	if err := st.SaveListing("u2", models.Listing{ItemID: "m3", Title: "Fuji X-T3", Price: 90000, PriceType: "fixed"}, "sony camera", models.ScoredListing{Score: 9.1}); err != nil {
		t.Fatalf("SaveListing(u2, m3) error = %v", err)
	}

	feed, err := st.ListRecentListings("u1", 10)
	if err != nil {
		t.Fatalf("ListRecentListings() error = %v", err)
	}
	if len(feed) != 2 {
		t.Fatalf("expected 2 user-scoped listings, got %d", len(feed))
	}
	for _, listing := range feed {
		if listing.ItemID == "m3" {
			t.Fatalf("feed leaked listing from another user: %#v", listing)
		}
	}

	comparables, err := st.GetComparableDeals("u1", "sony camera", "m1", 10)
	if err != nil {
		t.Fatalf("GetComparableDeals() error = %v", err)
	}
	if len(comparables) != 1 {
		t.Fatalf("expected 1 comparable for u1, got %d", len(comparables))
	}
	if comparables[0].ItemID != "m2" {
		t.Fatalf("expected comparable item m2, got %#v", comparables[0])
	}
}
