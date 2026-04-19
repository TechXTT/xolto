package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

	price, source, _, found, err := st.GetListingScoringState("u1", "m1")
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

// TestGetListingScoringStateReturnsComparablesCount verifies that
// GetListingScoringState returns the stored comparables_count so the worker can
// detect stale rows (count=0) and force a re-score (XOL-17 regression).
func TestGetListingScoringStateReturnsComparablesCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-scoring-state-count.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	listing := models.Listing{
		ItemID:    "rc1",
		Title:     "ThinkPad X1 Carbon",
		Price:     35000,
		PriceType: "fixed",
	}

	// Save with comparables_count=0 (simulates a listing scored before XOL-16).
	scoredZero := models.ScoredListing{
		Score:                    6.5,
		ReasoningSource:          "ai",
		ComparablesCount:         0,
		ComparablesMedianAgeDays: 0,
	}
	if err := st.SaveListing("u1", listing, "thinkpad x1", scoredZero); err != nil {
		t.Fatalf("SaveListing(zero) error = %v", err)
	}

	_, _, count, found, err := st.GetListingScoringState("u1", "rc1")
	if err != nil {
		t.Fatalf("GetListingScoringState() error = %v", err)
	}
	if !found {
		t.Fatal("expected scoring state to be found")
	}
	if count != 0 {
		t.Fatalf("expected comparablesCount=0 (stale row), got %d", count)
	}

	// Now save with comparables_count=12 (simulates a post-XOL-16 re-score).
	scoredPopulated := models.ScoredListing{
		Score:                    7.2,
		ReasoningSource:          "ai",
		ComparablesCount:         12,
		ComparablesMedianAgeDays: 8,
	}
	if err := st.SaveListing("u1", listing, "thinkpad x1", scoredPopulated); err != nil {
		t.Fatalf("SaveListing(populated) error = %v", err)
	}

	_, _, count, found, err = st.GetListingScoringState("u1", "rc1")
	if err != nil {
		t.Fatalf("GetListingScoringState() (populated) error = %v", err)
	}
	if !found {
		t.Fatal("expected scoring state to be found after re-save")
	}
	if count != 12 {
		t.Fatalf("expected comparablesCount=12 after re-score, got %d", count)
	}
}

// TestGetComparableDealsUsesFirstSeen verifies that GetComparableDeals populates
// ComparableDeal.LastSeen from first_seen (not last_seen) so that
// computeComparableStats produces a non-zero MedianAgeDays for listings that
// were discovered days ago but recently touched by the worker (XOL-17 regression).
func TestGetComparableDealsUsesFirstSeen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-first-seen.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	// Insert anchor listing (the one we are scoring).
	anchor := models.Listing{
		ItemID:    "anchor",
		Title:     "ThinkPad X1 Carbon anchor",
		Price:     35000,
		PriceType: "fixed",
	}
	if err := st.SaveListing("u1", anchor, "thinkpad x1 carbon", models.ScoredListing{Score: 7.0, ReasoningSource: "ai"}); err != nil {
		t.Fatalf("SaveListing(anchor) error = %v", err)
	}

	// Insert a comparable listing.
	comp := models.Listing{
		ItemID:    "comp1",
		Title:     "ThinkPad X1 Carbon comp",
		Price:     33000,
		PriceType: "fixed",
	}
	if err := st.SaveListing("u1", comp, "thinkpad x1 carbon", models.ScoredListing{Score: 6.5, ReasoningSource: "ai"}); err != nil {
		t.Fatalf("SaveListing(comp) error = %v", err)
	}

	// Simulate the passage of time by backdating first_seen to 10 days ago
	// using a direct SQL UPDATE (mirrors what really happens in prod: the
	// listing was first discovered days ago but the worker keeps touching it).
	_, updErr := st.db.Exec(
		`UPDATE listings SET first_seen = datetime('now', '-10 days') WHERE item_id = ?`,
		scopedItemID("u1", "comp1"),
	)
	if updErr != nil {
		t.Fatalf("UPDATE first_seen error = %v", updErr)
	}

	// TouchListing advances last_seen to now — simulating that the worker just
	// saw it again — while first_seen stays 10 days old.
	if err := st.TouchListing("u1", "comp1"); err != nil {
		t.Fatalf("TouchListing() error = %v", err)
	}

	deals, err := st.GetComparableDeals("u1", "thinkpad x1 carbon", "anchor", 10)
	if err != nil {
		t.Fatalf("GetComparableDeals() error = %v", err)
	}
	if len(deals) != 1 {
		t.Fatalf("expected 1 comparable, got %d", len(deals))
	}

	ageDays := int(time.Since(deals[0].LastSeen).Hours() / 24)
	if ageDays < 9 || ageDays > 11 {
		t.Fatalf("expected comparable age ~10 days (from first_seen), got %d days — "+
			"GetComparableDeals must use first_seen, not last_seen (XOL-17)", ageDays)
	}
}

func TestAIScoreCacheRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-ai-score-cache.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	const (
		cacheKey      = "olxbg:12345:16361:1"
		promptVersion = 1
	)
	if err := st.SetAIScoreCache(cacheKey, 16361, `{"relevant":true,"fair_price":16361}`, promptVersion); err != nil {
		t.Fatalf("SetAIScoreCache() error = %v", err)
	}

	score, reasoning, found, err := st.GetAIScoreCache(cacheKey, promptVersion)
	if err != nil {
		t.Fatalf("GetAIScoreCache() error = %v", err)
	}
	if !found {
		t.Fatalf("expected cache entry to be found")
	}
	if score != 16361 {
		t.Fatalf("expected cached score 16361, got %v", score)
	}
	if reasoning == "" {
		t.Fatalf("expected cached reasoning payload")
	}

	if _, _, found, err := st.GetAIScoreCache(cacheKey, 2); err != nil {
		t.Fatalf("GetAIScoreCache(promptVersion=2) error = %v", err)
	} else if found {
		t.Fatalf("expected cache miss for different prompt version")
	}
}

func TestStripeProcessedEventIdempotency(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-stripe-idempotency.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	firstSeen, err := st.RecordStripeProcessedEvent("evt_test_123")
	if err != nil {
		t.Fatalf("RecordStripeProcessedEvent(first) error = %v", err)
	}
	if !firstSeen {
		t.Fatalf("expected first webhook event insert to return firstSeen=true")
	}

	firstSeen, err = st.RecordStripeProcessedEvent("evt_test_123")
	if err != nil {
		t.Fatalf("RecordStripeProcessedEvent(second) error = %v", err)
	}
	if firstSeen {
		t.Fatalf("expected duplicate webhook event insert to return firstSeen=false")
	}
}

func TestAdminSearchOpsAggregationAndFilters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-admin-ops.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("ops@example.com", "hash", "Ops User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	missionID, err := st.UpsertMission(models.Mission{
		UserID:        userID,
		Name:          "Sony Mission",
		TargetQuery:   "sony a6000",
		Status:        "active",
		Urgency:       "flexible",
		Category:      "camera",
		CategoryID:    487,
		SearchQueries: []string{"sony a6000"},
		Active:        true,
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}
	searchID, err := st.CreateSearchConfig(models.SearchSpec{
		UserID:        userID,
		ProfileID:     missionID,
		Name:          "sony a6000",
		Query:         "sony a6000",
		MarketplaceID: "marktplaats",
		CountryCode:   "NL",
		CategoryID:    487,
		Enabled:       true,
		CheckInterval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateSearchConfig() error = %v", err)
	}

	now := time.Now().UTC()
	if err := st.RecordSearchRun(models.SearchRunLog{
		SearchConfigID: searchID,
		UserID:         userID,
		MissionID:      missionID,
		Plan:           "pro",
		MarketplaceID:  "marktplaats",
		CountryCode:    "NL",
		StartedAt:      now.Add(-3 * time.Minute),
		FinishedAt:     now.Add(-2 * time.Minute),
		Status:         "success",
		ResultsFound:   4,
		NewListings:    2,
		DealHits:       1,
	}); err != nil {
		t.Fatalf("RecordSearchRun(success) error = %v", err)
	}
	if err := st.RecordSearchRun(models.SearchRunLog{
		SearchConfigID:  searchID,
		UserID:          userID,
		MissionID:       missionID,
		Plan:            "pro",
		MarketplaceID:   "marktplaats",
		CountryCode:     "NL",
		StartedAt:       now.Add(-90 * time.Second),
		FinishedAt:      now.Add(-60 * time.Second),
		Status:          "search_failed",
		ErrorCode:       "search_failed",
		SearchesAvoided: 1,
	}); err != nil {
		t.Fatalf("RecordSearchRun(search_failed) error = %v", err)
	}

	stats, err := st.GetSearchOpsStats(30)
	if err != nil {
		t.Fatalf("GetSearchOpsStats() error = %v", err)
	}
	if stats.TotalRuns != 2 {
		t.Fatalf("expected 2 total runs, got %d", stats.TotalRuns)
	}
	if stats.SuccessfulRuns != 1 || stats.FailedRuns != 1 {
		t.Fatalf("expected 1 successful and 1 failed run, got success=%d failed=%d", stats.SuccessfulRuns, stats.FailedRuns)
	}
	if stats.ByStatus["success"] != 1 || stats.ByStatus["search_failed"] != 1 {
		t.Fatalf("unexpected by-status breakdown: %#v", stats.ByStatus)
	}

	successRuns, err := st.ListSearchRuns(models.AdminSearchRunFilter{
		Days:   30,
		Status: "success",
		UserID: userID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListSearchRuns() error = %v", err)
	}
	if len(successRuns) != 1 {
		t.Fatalf("expected one filtered success run, got %d", len(successRuns))
	}
	if successRuns[0].Status != "success" {
		t.Fatalf("expected status=success, got %q", successRuns[0].Status)
	}
}

func TestAdminAuditLogAndSearchControlPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-admin-audit.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("audit@example.com", "hash", "Audit User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	searchID, err := st.CreateSearchConfig(models.SearchSpec{
		UserID:        userID,
		Name:          "sony a6000",
		Query:         "sony a6000",
		MarketplaceID: "marktplaats",
		CountryCode:   "NL",
		CategoryID:    487,
		Enabled:       true,
		CheckInterval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateSearchConfig() error = %v", err)
	}

	if err := st.SetSearchEnabled(searchID, false); err != nil {
		t.Fatalf("SetSearchEnabled(false) error = %v", err)
	}
	if err := st.SetSearchNextRunAt(searchID, time.Now().UTC()); err != nil {
		t.Fatalf("SetSearchNextRunAt() error = %v", err)
	}
	search, err := st.GetSearchConfigByID(searchID)
	if err != nil {
		t.Fatalf("GetSearchConfigByID() error = %v", err)
	}
	if search == nil {
		t.Fatalf("search config not found")
	}
	if search.Enabled {
		t.Fatalf("expected search to be disabled")
	}
	if search.NextRunAt.IsZero() {
		t.Fatalf("expected next_run_at to be set")
	}

	if err := st.RecordAdminAuditLog(models.AdminAuditLogEntry{
		ActorUserID: "admin-user",
		Action:      "search_run_triggered",
		TargetType:  "search",
		TargetID:    "123",
		BeforeJSON:  `{"enabled":true}`,
		AfterJSON:   `{"enabled":false}`,
	}); err != nil {
		t.Fatalf("RecordAdminAuditLog() error = %v", err)
	}
	logs, err := st.ListAdminAuditLog(10)
	if err != nil {
		t.Fatalf("ListAdminAuditLog() error = %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one audit log entry, got %d", len(logs))
	}
	if logs[0].Action != "search_run_triggered" {
		t.Fatalf("expected action search_run_triggered, got %q", logs[0].Action)
	}
}

func TestAIUsagePersistsMissionContextAndUserAggregation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "xolto-ai-usage-mission.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("usage@example.com", "hash", "Usage User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	missionID, err := st.UpsertMission(models.Mission{
		UserID:        userID,
		Name:          "Sony Mission",
		TargetQuery:   "sony a6000",
		Status:        "active",
		Urgency:       "flexible",
		Category:      "camera",
		CategoryID:    487,
		SearchQueries: []string{"sony a6000"},
		Active:        true,
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}

	if err := st.RecordAIUsage(models.AIUsageEntry{
		UserID:           userID,
		MissionID:        missionID,
		CallType:         "reasoner",
		Model:            "gpt-5-mini",
		PromptTokens:     120,
		CompletionTokens: 80,
		TotalTokens:      200,
		LatencyMs:        420,
		Success:          true,
	}); err != nil {
		t.Fatalf("RecordAIUsage(mission) error = %v", err)
	}
	if err := st.RecordAIUsage(models.AIUsageEntry{
		UserID:           userID,
		MissionID:        0,
		CallType:         "brief_parser",
		Model:            "gpt-5-mini",
		PromptTokens:     60,
		CompletionTokens: 40,
		TotalTokens:      100,
		LatencyMs:        210,
		Success:          true,
	}); err != nil {
		t.Fatalf("RecordAIUsage(user) error = %v", err)
	}

	entries, err := st.GetAIUsageTimeline(7)
	if err != nil {
		t.Fatalf("GetAIUsageTimeline() error = %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 usage entries, got %d", len(entries))
	}
	var foundMission bool
	for _, entry := range entries {
		if entry.UserID != userID {
			continue
		}
		if entry.MissionID == missionID && entry.CallType == "reasoner" {
			foundMission = true
			break
		}
	}
	if !foundMission {
		t.Fatalf("expected mission-scoped usage entry for user %q mission %d", userID, missionID)
	}

	users, err := st.ListAllUsers()
	if err != nil {
		t.Fatalf("ListAllUsers() error = %v", err)
	}
	var summary *models.AdminUserSummary
	for i := range users {
		if users[i].ID == userID {
			summary = &users[i]
			break
		}
	}
	if summary == nil {
		t.Fatalf("user summary not found for %q", userID)
	}
	if summary.AICallCount != 2 {
		t.Fatalf("expected ai_call_count=2, got %d", summary.AICallCount)
	}
	if summary.AITokens != 300 {
		t.Fatalf("expected ai_tokens=300, got %d", summary.AITokens)
	}
}

// TestRecordPriceMarketplaceIsolation verifies that price_history rows are
// scoped by marketplace_id: GetMarketAverage only returns data for the
// requested marketplace and does not contaminate across markets.
func TestRecordPriceMarketplaceIsolation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "price-isolation-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	const query = "sony a6400"
	const catID = 0
	const minSamples = 3

	// Insert 3 NL prices (average 100_00 = €100).
	for _, p := range []int{9000, 10000, 11000} {
		if err := st.RecordPrice(query, catID, "marktplaats", p); err != nil {
			t.Fatalf("RecordPrice(marktplaats) error = %v", err)
		}
	}

	// Insert 3 BG prices (average 60_00 = €60).
	for _, p := range []int{5000, 6000, 7000} {
		if err := st.RecordPrice(query, catID, "olxbg", p); err != nil {
			t.Fatalf("RecordPrice(olxbg) error = %v", err)
		}
	}

	// NL market average should be ~10000, not contaminated by BG prices.
	nlAvg, ok, err := st.GetMarketAverage(query, catID, "marktplaats", minSamples)
	if err != nil {
		t.Fatalf("GetMarketAverage(marktplaats) error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for marktplaats, got false")
	}
	if nlAvg != 10000 {
		t.Errorf("expected marktplaats avg=10000, got %d", nlAvg)
	}

	// BG market average should be ~6000, not contaminated by NL prices.
	bgAvg, ok, err := st.GetMarketAverage(query, catID, "olxbg", minSamples)
	if err != nil {
		t.Fatalf("GetMarketAverage(olxbg) error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for olxbg, got false")
	}
	if bgAvg != 6000 {
		t.Errorf("expected olxbg avg=6000, got %d", bgAvg)
	}
}

// TestRecordPriceEmptyMarketplaceID verifies that an empty marketplaceID is
// treated as a valid bucket (legacy rows default to ''). Querying with '' only
// returns rows recorded with ''.
func TestRecordPriceEmptyMarketplaceID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "price-empty-mp-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	const query = "sony a6400"
	const catID = 0
	const minSamples = 2

	// Insert 2 legacy rows (empty marketplace_id).
	for _, p := range []int{8000, 12000} {
		if err := st.RecordPrice(query, catID, "", p); err != nil {
			t.Fatalf("RecordPrice('') error = %v", err)
		}
	}

	// Insert 2 rows for a real marketplace.
	for _, p := range []int{5000, 7000} {
		if err := st.RecordPrice(query, catID, "olxbg", p); err != nil {
			t.Fatalf("RecordPrice(olxbg) error = %v", err)
		}
	}

	// Querying '' should only return the legacy rows (avg 10000).
	emptyAvg, ok, err := st.GetMarketAverage(query, catID, "", minSamples)
	if err != nil {
		t.Fatalf("GetMarketAverage('') error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for '' marketplace, got false")
	}
	if emptyAvg != 10000 {
		t.Errorf("expected '' avg=10000, got %d", emptyAvg)
	}

	// Querying 'olxbg' should not see legacy rows (avg 6000).
	bgAvg, ok, err := st.GetMarketAverage(query, catID, "olxbg", minSamples)
	if err != nil {
		t.Fatalf("GetMarketAverage(olxbg) error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for olxbg, got false")
	}
	if bgAvg != 6000 {
		t.Errorf("expected olxbg avg=6000, got %d", bgAvg)
	}
}

// TestUpdateOutreachStatusValidStatuses verifies that each of the five valid
// outreach status values can be persisted and read back via GetListing.
func TestUpdateOutreachStatusValidStatuses(t *testing.T) {
	validStatuses := []string{"none", "sent", "replied", "won", "lost"}

	for _, status := range validStatuses {
		t.Run(status, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "outreach-status-test.db")
			st, err := New(dbPath)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer st.Close()

			userID := "u-" + status
			itemID := "item-" + status

			l := models.Listing{
				ItemID:        itemID,
				Title:         "Test listing " + status,
				Price:         10000,
				PriceType:     "fixed",
				MarketplaceID: "olxbg",
			}
			if err := st.SaveListing(userID, l, "query", models.ScoredListing{}); err != nil {
				t.Fatalf("SaveListing() error = %v", err)
			}

			if err := st.UpdateOutreachStatus(context.Background(), userID, itemID, status); err != nil {
				t.Fatalf("UpdateOutreachStatus(%q) error = %v", status, err)
			}

			got, err := st.GetListing(userID, itemID)
			if err != nil {
				t.Fatalf("GetListing() error = %v", err)
			}
			if got == nil {
				t.Fatalf("GetListing() returned nil")
			}
			if got.OutreachStatus != status {
				t.Errorf("expected OutreachStatus=%q, got %q", status, got.OutreachStatus)
			}
		})
	}
}

// TestUpdateOutreachStatusDefaultIsNone verifies that a freshly saved listing
// has OutreachStatus == "none" before any PATCH has been applied.
func TestUpdateOutreachStatusDefaultIsNone(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "outreach-default-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	l := models.Listing{
		ItemID:        "default-status-item",
		Title:         "Default status test",
		Price:         5000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}
	if err := st.SaveListing("u1", l, "q", models.ScoredListing{}); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	got, err := st.GetListing("u1", "default-status-item")
	if err != nil {
		t.Fatalf("GetListing() error = %v", err)
	}
	if got == nil {
		t.Fatalf("GetListing() returned nil")
	}
	if got.OutreachStatus != "none" {
		t.Errorf("expected default OutreachStatus=none, got %q", got.OutreachStatus)
	}
}

// TestUpdateOutreachStatusNotFound verifies that UpdateOutreachStatus returns
// ErrListingNotFound when the listing does not belong to the given user.
func TestUpdateOutreachStatusNotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "outreach-notfound-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	err = st.UpdateOutreachStatus(context.Background(), "no-such-user", "no-such-item", "sent")
	if !errors.Is(err, ErrListingNotFound) {
		t.Fatalf("expected ErrListingNotFound, got %v", err)
	}
}

// TestListShortlistEntriesIncludesConditionAndOutreachStatus verifies that
// GetShortlist joins the listings table and surfaces condition and
// outreach_status on the returned ShortlistEntry structs.
func TestListShortlistEntriesIncludesConditionAndOutreachStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shortlist-join-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	const userID = "u-join-1"
	const rawItemID = "item-join-1"

	// Save the listing with condition="used" and marketplace_id="olxbg".
	err = st.SaveListing(userID, models.Listing{
		ItemID:        rawItemID,
		Title:         "Test Phone",
		Price:         50000,
		PriceType:     "fixed",
		Condition:     "used",
		MarketplaceID: "olxbg",
	}, "test query", models.ScoredListing{
		RecommendedAction: "buy",
	})
	if err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	// Set outreach_status to "sent".
	if err := st.UpdateOutreachStatus(context.Background(), userID, rawItemID, "sent"); err != nil {
		t.Fatalf("UpdateOutreachStatus() error = %v", err)
	}

	// Save a shortlist entry for the same item.
	if err := st.SaveShortlistEntry(models.ShortlistEntry{
		UserID:  userID,
		ItemID:  rawItemID,
		Title:   "Test Phone",
		Status:  "watching",
	}); err != nil {
		t.Fatalf("SaveShortlistEntry() error = %v", err)
	}

	entries, err := st.GetShortlist(userID)
	if err != nil {
		t.Fatalf("GetShortlist() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Condition != "used" {
		t.Errorf("Condition: got %q, want %q", got.Condition, "used")
	}
	if got.OutreachStatus != "sent" {
		t.Errorf("OutreachStatus: got %q, want %q", got.OutreachStatus, "sent")
	}
}

// TestListShortlistEntriesDefaultsWhenNoListing verifies that GetShortlist
// returns empty string for Condition and "none" for OutreachStatus when there
// is no corresponding listing row for a shortlist entry.
func TestListShortlistEntriesDefaultsWhenNoListing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shortlist-nolisting-test.db")
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	const userID = "u-nolisting-1"

	// Save a shortlist entry WITHOUT a corresponding listing.
	if err := st.SaveShortlistEntry(models.ShortlistEntry{
		UserID: userID,
		ItemID: "orphan-item",
		Title:  "Orphan Entry",
		Status: "watching",
	}); err != nil {
		t.Fatalf("SaveShortlistEntry() error = %v", err)
	}

	entries, err := st.GetShortlist(userID)
	if err != nil {
		t.Fatalf("GetShortlist() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Condition != "" {
		t.Errorf("Condition: got %q, want %q", got.Condition, "")
	}
	if got.OutreachStatus != "none" {
		t.Errorf("OutreachStatus: got %q, want %q", got.OutreachStatus, "none")
	}
}
