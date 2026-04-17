package store

// Postgres integration tests for ListRecentListingsPaginated.
//
// These tests execute real SQL against a live Postgres instance. They are
// gated on the TEST_POSTGRES_DSN environment variable and skip silently when
// that variable is unset, so they do not break the standard `go test ./...`
// run on machines without a Postgres instance.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable" \
//	  go test ./internal/store/ -run TestPostgres -v
//
// The tests reproduce the SQLSTATE 42P18 bug introduced in Phase 3 by the
// off-by-one in the positional-placeholder counter (n := 3 instead of n := 2).

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// openTestPostgres opens a PostgresStore against TEST_POSTGRES_DSN, runs
// migrations, and returns the store. The caller must call st.Close().
// Skips the test if the env var is unset.
func openTestPostgres(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set — skipping Postgres integration tests")
	}
	ctx := context.Background()
	st, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPostgres() error = %v", err)
	}
	return st
}

// createPGUser creates a unique test user in Postgres and returns the userID.
// Uses a random suffix so parallel test runs do not collide.
func createPGUser(t *testing.T, st *PostgresStore) string {
	t.Helper()
	suffix := fmt.Sprintf("%08x", rand.Int31())
	email := "pgtest-" + suffix + "@example.com"
	userID, err := st.CreateUser(email, "hash", "PG Test")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	return userID
}

// insertPGFixtures inserts the standard 12-item fixture set used by the SQLite
// filter tests, adapted for a Postgres store and given userID.
func insertPGFixtures(t *testing.T, st *PostgresStore, userID string) {
	t.Helper()
	type fix struct {
		itemID      string
		marketplace string
		condition   string
		score       float64
		offerPrice  int
	}
	fixtures := []fix{
		{"pgf-0", "marktplaats", "new", 9.0, 50000},
		{"pgf-1", "marktplaats", "new", 8.0, 45000},
		{"pgf-2", "marktplaats", "like_new", 7.5, 40000},
		{"pgf-3", "marktplaats", "good", 7.0, 30000},
		{"pgf-4", "marktplaats", "fair", 6.0, 25000},
		{"pgf-5", "vinted_nl", "like_new", 5.5, 35000},
		{"pgf-6", "vinted_nl", "good", 5.0, 20000},
		{"pgf-7", "vinted_nl", "fair", 4.0, 15000},
		{"pgf-8", "olxbg", "good", 8.5, 18000},
		{"pgf-9", "olxbg", "fair", 7.5, 12000},
		{"pgf-10", "olxbg", "like_new", 3.0, 8000},
		{"pgf-11", "vinted_dk", "good", 2.0, 6000},
	}
	for _, f := range fixtures {
		l := models.Listing{
			ItemID:        f.itemID,
			Title:         "PG " + f.itemID,
			Price:         f.offerPrice + 5000,
			PriceType:     "fixed",
			MarketplaceID: f.marketplace,
			Condition:     f.condition,
		}
		scored := models.ScoredListing{
			Score:      f.score,
			OfferPrice: f.offerPrice,
		}
		if err := st.SaveListing(userID, l, "pg filter query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", f.itemID, err)
		}
	}
}

// ---------------------------------------------------------------
// Core regression: no-filter path must not 500
// This is the exact scenario that caused SQLSTATE 42P18 in prod.
// ---------------------------------------------------------------

func TestPostgresNoFilterBaseline(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// No filters — this was the failing path (n=3 caused a $3 gap with only 2
	// base args, making Postgres emit 42P18 indeterminate_datatype for $3).
	listings, total, err := st.ListRecentListingsPaginated(userID, 20, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("no-filter baseline: unexpected error = %v", err)
	}
	if total != 12 {
		t.Fatalf("no-filter baseline: expected total=12, got %d", total)
	}
	if len(listings) != 12 {
		t.Fatalf("no-filter baseline: expected 12 listings, got %d", len(listings))
	}
}

func TestPostgresMissionIDFilter(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// missionID=0 means "all missions" — must return all 12 rows.
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("missionID=0: unexpected error = %v", err)
	}
	if total != 12 {
		t.Fatalf("missionID=0: expected total=12, got %d", total)
	}
	if len(listings) != 12 {
		t.Fatalf("missionID=0: expected 12 listings, got %d", len(listings))
	}
}

// ---------------------------------------------------------------
// Individual filter coverage
// ---------------------------------------------------------------

func TestPostgresFilterMarket(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Market: "marktplaats"})
	if err != nil {
		t.Fatalf("market=marktplaats error = %v", err)
	}
	if total != 5 {
		t.Fatalf("market=marktplaats: expected total=5, got %d", total)
	}
	for _, l := range listings {
		if l.MarketplaceID != "marktplaats" {
			t.Errorf("market=marktplaats: item %q has marketplace %q", l.ItemID, l.MarketplaceID)
		}
	}
}

func TestPostgresFilterCondition(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// condition=good → pgf-3, pgf-6, pgf-8, pgf-11 = 4
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Condition: "good"})
	if err != nil {
		t.Fatalf("condition=good error = %v", err)
	}
	if total != 4 {
		t.Fatalf("condition=good: expected total=4, got %d", total)
	}
	for _, l := range listings {
		if l.Condition != "good" {
			t.Errorf("condition=good: item %q has condition %q", l.ItemID, l.Condition)
		}
	}
}

func TestPostgresFilterMinScore(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// min_score=7 → pgf-0(9),pgf-1(8),pgf-2(7.5),pgf-3(7),pgf-8(8.5),pgf-9(7.5) = 6
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{MinScore: 7})
	if err != nil {
		t.Fatalf("min_score=7 error = %v", err)
	}
	if total != 6 {
		t.Fatalf("min_score=7: expected total=6, got %d", total)
	}
	for _, l := range listings {
		if l.Score < 7.0 {
			t.Errorf("min_score=7: item %q has score %v < 7", l.ItemID, l.Score)
		}
	}
}

func TestPostgresFilterSortScore(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "score"})
	if err != nil {
		t.Fatalf("sort=score error = %v", err)
	}
	if total != 12 {
		t.Fatalf("sort=score: expected total=12, got %d", total)
	}
	var prev float64 = 11.0
	for _, l := range listings {
		if l.Score > prev {
			t.Errorf("sort=score: item %q has score %v > previous %v (not DESC)", l.ItemID, l.Score, prev)
		}
		prev = l.Score
	}
}

func TestPostgresFilterSortPriceAsc(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "price_asc"})
	if err != nil {
		t.Fatalf("sort=price_asc error = %v", err)
	}
	if total != 12 {
		t.Fatalf("sort=price_asc: expected total=12, got %d", total)
	}
	// All zero-price rows must appear after non-zero rows.
	seenNonZero := false
	inZeroZone := false
	for _, l := range listings {
		if l.OfferPrice == 0 {
			inZeroZone = true
		} else {
			if inZeroZone {
				t.Errorf("sort=price_asc: non-zero offer_price %d appeared after zero-price rows for item %q", l.OfferPrice, l.ItemID)
			}
			seenNonZero = true
		}
		_ = seenNonZero
	}
}

func TestPostgresFilterSortPriceDescZeroLast(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	// Insert a real price, a zero price, and ensure zero comes last.
	fixtures := []struct {
		id    string
		price int
		score float64
	}{
		{"zp-pg-real", 25000, 5.0},
		{"zp-pg-zero", 0, 5.0},
	}
	for _, f := range fixtures {
		l := models.Listing{
			ItemID:        f.id,
			Title:         "ZP " + f.id,
			Price:         30000,
			PriceType:     "fixed",
			MarketplaceID: "marktplaats",
			Condition:     "good",
		}
		scored := models.ScoredListing{Score: f.score, OfferPrice: f.price}
		if err := st.SaveListing(userID, l, "zp pg query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", f.id, err)
		}
	}

	for _, sortMode := range []string{"price_asc", "price_desc"} {
		listings, _, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: sortMode})
		if err != nil {
			t.Fatalf("sort=%s error = %v", sortMode, err)
		}
		if len(listings) < 2 {
			t.Fatalf("sort=%s: expected at least 2 listings, got %d", sortMode, len(listings))
		}
		last := listings[len(listings)-1]
		if last.OfferPrice != 0 {
			t.Errorf("sort=%s: expected zero-price row last, got offer_price=%d for item %q", sortMode, last.OfferPrice, last.ItemID)
		}
	}
}

// ---------------------------------------------------------------
// Combined filter
// ---------------------------------------------------------------

func TestPostgresFilterCombined(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// market=marktplaats + condition=good + min_score=7 + sort=price_asc → pgf-3 only
	f := models.MatchesFilter{
		Market:    "marktplaats",
		Condition: "good",
		MinScore:  7,
		Sort:      "price_asc",
	}
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, f)
	if err != nil {
		t.Fatalf("combined filter error = %v", err)
	}
	if total != 1 {
		t.Fatalf("combined filter: expected total=1, got %d", total)
	}
	if len(listings) != 1 || listings[0].ItemID != "pgf-3" {
		t.Fatalf("combined filter: expected [pgf-3], got %v", listings)
	}
}

// ---------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------

func TestPostgresPagination(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	userID := createPGUser(t, st)
	insertPGFixtures(t, st, userID)

	// Paginate all 12 rows with limit=5 — no overlaps, no gaps.
	seen := map[string]bool{}
	var expectedTotal int

	for offset := 0; ; offset += 5 {
		page, total, err := st.ListRecentListingsPaginated(userID, 5, offset, 0, models.MatchesFilter{})
		if err != nil {
			t.Fatalf("pagination error at offset=%d: %v", offset, err)
		}
		if expectedTotal == 0 {
			expectedTotal = total
		} else if total != expectedTotal {
			t.Fatalf("pagination: total changed from %d to %d at offset=%d", expectedTotal, total, offset)
		}
		for _, l := range page {
			if seen[l.ItemID] {
				t.Fatalf("pagination: duplicate item %q at offset=%d", l.ItemID, offset)
			}
			seen[l.ItemID] = true
		}
		if len(page) < 5 {
			break
		}
	}
	if len(seen) != 12 {
		t.Fatalf("pagination: expected 12 unique items, got %d", len(seen))
	}
}
