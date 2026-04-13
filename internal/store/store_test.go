package store

import (
	"path/filepath"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

func TestMissionAndShortlistPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	missionID, err := st.UpsertMission(models.Mission{
		UserID:             "u1",
		Name:               "Sony A7 III",
		TargetQuery:        "sony a7 iii",
		CategoryID:         487,
		BudgetMax:          1000,
		BudgetStretch:      1100,
		PreferredCondition: []string{"Gebruikt", "Zo goed als nieuw"},
		SearchQueries:      []string{"sony a7 iii", "sony alpha 7 iii"},
		Status:             "active",
		Urgency:            "flexible",
		Category:           "camera",
		Active:             true,
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}

	mission, err := st.GetActiveMission("u1")
	if err != nil {
		t.Fatalf("GetActiveMission() error = %v", err)
	}
	if mission == nil || mission.ID != missionID {
		t.Fatalf("expected active mission id %d, got %#v", missionID, mission)
	}

	err = st.SaveShortlistEntry(models.ShortlistEntry{
		UserID:              "u1",
		MissionID:           missionID,
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
	dbPath := filepath.Join(t.TempDir(), "xolto-scope-test.db")
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

	feed, err := st.ListRecentListings("u1", 10, 0)
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

func TestListingScoringStatePersistsReasoningSource(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-scoring-state.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	listing := models.Listing{
		ItemID:    "m1",
		Title:     "Sony A7 III",
		Price:     95000,
		PriceType: "fixed",
	}
	scored := models.ScoredListing{
		Score:           8.9,
		FairPrice:       102000,
		OfferPrice:      90000,
		Confidence:      0.88,
		Reason:          "strong comparable support",
		ReasoningSource: "ai",
	}

	if err := st.SaveListing("u1", listing, "sony camera", scored); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	price, source, found, err := st.GetListingScoringState("u1", "m1")
	if err != nil {
		t.Fatalf("GetListingScoringState() error = %v", err)
	}
	if !found {
		t.Fatalf("expected stored scoring state")
	}
	if price != listing.Price {
		t.Fatalf("expected stored price %d, got %d", listing.Price, price)
	}
	if source != "ai" {
		t.Fatalf("expected reasoning source %q, got %q", "ai", source)
	}

	if err := st.TouchListing("u1", "m1"); err != nil {
		t.Fatalf("TouchListing() error = %v", err)
	}
}
