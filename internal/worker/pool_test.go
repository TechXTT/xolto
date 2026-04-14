package worker

import (
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

func TestRoundRobinByPriorityBandFairness(t *testing.T) {
	userA := &models.User{ID: "a", Tier: "pro"}
	userB := &models.User{ID: "b", Tier: "pro"}

	ordered := roundRobinByPriorityBand([]candidate{
		{spec: models.SearchSpec{ID: 1}, user: userA, priority: 39},
		{spec: models.SearchSpec{ID: 2}, user: userA, priority: 38},
		{spec: models.SearchSpec{ID: 3}, user: userB, priority: 37},
		{spec: models.SearchSpec{ID: 4}, user: userB, priority: 36},
	})

	got := []int64{ordered[0].spec.ID, ordered[1].spec.ID, ordered[2].spec.ID, ordered[3].spec.ID}
	want := []int64{1, 3, 2, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected round robin order: got %v want %v", got, want)
		}
	}
}

func TestFinalizeDispatchOrderRespectsPlanCaps(t *testing.T) {
	pool := &Pool{}
	freeUser := &models.User{ID: "free-user", Tier: "free"}
	proUser := &models.User{ID: "pro-user", Tier: "pro"}

	ordered := pool.finalizeDispatchOrder([]candidate{
		{spec: models.SearchSpec{ID: 1, MarketplaceID: "marktplaats"}, user: freeUser, priority: 30},
		{spec: models.SearchSpec{ID: 2, MarketplaceID: "marktplaats"}, user: freeUser, priority: 29},
		{spec: models.SearchSpec{ID: 3, MarketplaceID: "marktplaats"}, user: proUser, priority: 28},
		{spec: models.SearchSpec{ID: 4, MarketplaceID: "marktplaats"}, user: proUser, priority: 27},
	}, false)

	if len(ordered) != 3 {
		t.Fatalf("expected 3 dispatched candidates, got %d", len(ordered))
	}
	if ordered[0].spec.ID != 1 || ordered[1].spec.ID != 3 || ordered[2].spec.ID != 4 {
		t.Fatalf("unexpected dispatch order: %#v", []int64{ordered[0].spec.ID, ordered[1].spec.ID, ordered[2].spec.ID})
	}
	if ordered[1].priority != 18 || ordered[2].priority != 7 {
		t.Fatalf("expected marketplace pressure penalties to reduce later priorities, got %d and %d", ordered[1].priority, ordered[2].priority)
	}
}

func TestNextRunAtAfterAppliesBackoff(t *testing.T) {
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)
	spec := models.SearchSpec{
		CheckInterval:        time.Minute,
		ConsecutiveEmptyRuns: 2,
		ConsecutiveFailures:  1,
	}
	user := &models.User{Tier: "free"}

	got := nextRunAtAfter(spec, user, now)
	want := now.Add(30 * time.Minute * 4 * 2)
	if !got.Equal(want) {
		t.Fatalf("unexpected next run: got %s want %s", got, want)
	}
}

func TestPopulateSearchLocationUsesMissionThenUserDefaults(t *testing.T) {
	user := &models.User{
		CountryCode:       "BG",
		City:              "Sofia",
		PostalCode:        "1000",
		PreferredRadiusKm: 25,
	}
	mission := &models.Mission{
		CountryCode:  "DK",
		City:         "Copenhagen",
		PostalCode:   "2100",
		TravelRadius: 60,
	}
	spec := populateSearchLocation(models.SearchSpec{MarketplaceID: "VINTED"}, user, mission)

	if spec.MarketplaceID != "vinted_nl" {
		t.Fatalf("expected legacy marketplace id to normalize, got %q", spec.MarketplaceID)
	}
	if spec.CountryCode != "DK" || spec.City != "Copenhagen" || spec.PostalCode != "2100" || spec.RadiusKm != 60 {
		t.Fatalf("unexpected populated location: %+v", spec)
	}
}
