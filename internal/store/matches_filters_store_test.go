package store

// Phase 3 store-layer tests for ListRecentListingsPaginated with filters.
//
// These tests exercise the SQLite implementation directly (no HTTP layer).
// The Postgres ORDER BY and WHERE clause construction is validated by unit
// tests of the postgresOrderBy helper (same logic; Postgres-specific NULLS
// LAST syntax is verified via query inspection rather than live Postgres
// integration, as no Postgres service is available in the test harness).

import (
	"path/filepath"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// setupFilterStore creates a fresh in-memory SQLite store and inserts the
// standard fixture set for filter tests.
//
// Fixture set (12 items — 2 per condition×market combination for tie-breaker
// coverage):
//
//	item_id     marketplace  condition  score  offerPrice
//	"sf-0"      marktplaats  new        9.0    50000
//	"sf-1"      marktplaats  new        8.0    45000  ← same condition, same mkt
//	"sf-2"      marktplaats  like_new   7.5    40000
//	"sf-3"      marktplaats  good       7.0    30000
//	"sf-4"      marktplaats  fair       6.0    25000
//	"sf-5"      vinted_nl    like_new   5.5    35000
//	"sf-6"      vinted_nl    good       5.0    20000
//	"sf-7"      vinted_nl    fair       4.0    15000
//	"sf-8"      olxbg        good       8.5    18000  ← same condition as sf-3
//	"sf-9"      olxbg        fair       7.5    12000  ← same score as sf-2
//	"sf-10"     olxbg        like_new   3.0    8000
//	"sf-11"     vinted_dk    good       2.0    6000
func setupFilterStore(t *testing.T) (st *SQLiteStore, userID string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "store-filter-test.db")
	var err error
	st, err = New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	userID, err = st.CreateUser("storefilter@example.com", "hash", "Store Filter")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	type fix struct {
		itemID     string
		marketplace string
		condition  string
		score      float64
		offerPrice int
	}
	fixtures := []fix{
		{"sf-0", "marktplaats", "new", 9.0, 50000},
		{"sf-1", "marktplaats", "new", 8.0, 45000},
		{"sf-2", "marktplaats", "like_new", 7.5, 40000},
		{"sf-3", "marktplaats", "good", 7.0, 30000},
		{"sf-4", "marktplaats", "fair", 6.0, 25000},
		{"sf-5", "vinted_nl", "like_new", 5.5, 35000},
		{"sf-6", "vinted_nl", "good", 5.0, 20000},
		{"sf-7", "vinted_nl", "fair", 4.0, 15000},
		{"sf-8", "olxbg", "good", 8.5, 18000},
		{"sf-9", "olxbg", "fair", 7.5, 12000},
		{"sf-10", "olxbg", "like_new", 3.0, 8000},
		{"sf-11", "vinted_dk", "good", 2.0, 6000},
	}
	for _, f := range fixtures {
		l := models.Listing{
			ItemID:        f.itemID,
			Title:         "Store " + f.itemID,
			Price:         f.offerPrice + 5000,
			PriceType:     "fixed",
			MarketplaceID: f.marketplace,
			Condition:     f.condition,
		}
		scored := models.ScoredListing{
			Score:      f.score,
			OfferPrice: f.offerPrice,
		}
		if err := st.SaveListing(userID, l, "filter query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", f.itemID, err)
		}
	}
	return st, userID
}

// ---------------------------------------------------------------
// SQLite store: filter correctness
// ---------------------------------------------------------------

func TestStoreFilterMinScore(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// min_score=7 → sf-0(9),sf-1(8),sf-2(7.5),sf-3(7),sf-8(8.5),sf-9(7.5) = 6
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{MinScore: 7})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 6 {
		t.Fatalf("min_score=7: expected total=6, got %d", total)
	}
	if len(listings) != 6 {
		t.Fatalf("min_score=7: expected 6 items, got %d", len(listings))
	}
	for _, l := range listings {
		if l.Score < 7.0 {
			t.Fatalf("min_score=7: item %q has score %v < 7", l.ItemID, l.Score)
		}
	}
}

func TestStoreFilterCondition(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// condition=good → sf-3, sf-6, sf-8, sf-11 = 4
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Condition: "good"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 4 {
		t.Fatalf("condition=good: expected total=4, got %d", total)
	}
	for _, l := range listings {
		if l.Condition != "good" {
			t.Fatalf("condition=good: item %q has condition %q", l.ItemID, l.Condition)
		}
	}
}

func TestStoreFilterMarket(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// market=marktplaats → sf-0..sf-4 = 5
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Market: "marktplaats"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 5 {
		t.Fatalf("market=marktplaats: expected total=5, got %d", total)
	}
	for _, l := range listings {
		if l.MarketplaceID != "marktplaats" {
			t.Fatalf("market=marktplaats: item %q has marketplace %q", l.ItemID, l.MarketplaceID)
		}
	}

	// market=olxbg → sf-8, sf-9, sf-10 = 3
	listingsOlx, totalOlx, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Market: "olxbg"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated(olxbg) error = %v", err)
	}
	if totalOlx != 3 {
		t.Fatalf("market=olxbg: expected total=3, got %d", totalOlx)
	}
	for _, l := range listingsOlx {
		if l.MarketplaceID != "olxbg" {
			t.Fatalf("market=olxbg: item %q has marketplace %q", l.ItemID, l.MarketplaceID)
		}
	}
}

func TestStoreFilterSortScore(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "score"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 12 {
		t.Fatalf("sort=score: expected total=12, got %d", total)
	}
	var prev float64 = 11.0
	for _, l := range listings {
		if l.Score > prev {
			t.Fatalf("sort=score: score %v > previous %v (not DESC) for item %q", l.Score, prev, l.ItemID)
		}
		prev = l.Score
	}
}

func TestStoreFilterSortPriceAsc(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "price_asc"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 12 {
		t.Fatalf("sort=price_asc: expected total=12, got %d", total)
	}
	prevPrice := -1
	for _, l := range listings {
		if l.OfferPrice != 0 {
			if prevPrice != -1 && l.OfferPrice < prevPrice {
				t.Fatalf("sort=price_asc: price %d < previous %d (not ASC) for item %q", l.OfferPrice, prevPrice, l.ItemID)
			}
			prevPrice = l.OfferPrice
		}
	}
}

func TestStoreFilterSortPriceDesc(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "price_desc"})
	if err != nil {
		t.Fatalf("ListRecentListingsPaginated() error = %v", err)
	}
	if total != 12 {
		t.Fatalf("sort=price_desc: expected total=12, got %d", total)
	}
	prevPrice := 1<<31 - 1
	for _, l := range listings {
		if l.OfferPrice != 0 {
			if l.OfferPrice > prevPrice {
				t.Fatalf("sort=price_desc: price %d > previous %d (not DESC) for item %q", l.OfferPrice, prevPrice, l.ItemID)
			}
			prevPrice = l.OfferPrice
		}
	}
}

func TestStoreFilterSortNewestIsDefault(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// Zero-value filter (sort="") must produce identical order to sort="newest".
	listingsDefault, _, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{})
	if err != nil {
		t.Fatalf("default filter error = %v", err)
	}
	listingsNewest, _, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "newest"})
	if err != nil {
		t.Fatalf("sort=newest error = %v", err)
	}
	if len(listingsDefault) != len(listingsNewest) {
		t.Fatalf("default vs newest: different counts %d vs %d", len(listingsDefault), len(listingsNewest))
	}
	for i := range listingsDefault {
		if listingsDefault[i].ItemID != listingsNewest[i].ItemID {
			t.Fatalf("default vs newest: differ at %d: %q vs %q", i, listingsDefault[i].ItemID, listingsNewest[i].ItemID)
		}
	}
}

func TestStoreFilterCombined(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// market=marktplaats + condition=good + min_score=7 → sf-3 only (score=7.0)
	f := models.MatchesFilter{Market: "marktplaats", Condition: "good", MinScore: 7, Sort: "price_asc"}
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, f)
	if err != nil {
		t.Fatalf("combined filter error = %v", err)
	}
	if total != 1 {
		t.Fatalf("combined: expected total=1, got %d", total)
	}
	if len(listings) != 1 || listings[0].ItemID != "sf-3" {
		t.Fatalf("combined: expected [sf-3], got %v", listings)
	}
}

func TestStoreFilterEmptyResult(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// market=vinted_dk + condition=new → no item in fixture
	f := models.MatchesFilter{Market: "vinted_dk", Condition: "new"}
	listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, f)
	if err != nil {
		t.Fatalf("empty result error = %v", err)
	}
	if total != 0 {
		t.Fatalf("empty result: expected total=0, got %d", total)
	}
	if listings != nil && len(listings) != 0 {
		t.Fatalf("empty result: expected nil/empty listings, got %v", listings)
	}
}

func TestStoreFilterPaginationNoOverlapNoGap(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// min_score=5 → sf-0(9),sf-1(8),sf-2(7.5),sf-3(7),sf-4(6),sf-5(5.5),
	//               sf-6(5.0),sf-8(8.5),sf-9(7.5) = 9 items.
	// (sf-7=4.0, sf-10=3.0, sf-11=2.0 excluded)
	// Page with limit=3 → 3 pages: [3]+[3]+[3].
	f := models.MatchesFilter{MinScore: 5}
	seen := map[string]bool{}
	var allIDs []string

	for offset := 0; ; offset += 3 {
		page, total, err := st.ListRecentListingsPaginated(userID, 3, offset, 0, f)
		if err != nil {
			t.Fatalf("pagination error at offset=%d: %v", offset, err)
		}
		if total != 9 {
			t.Fatalf("pagination: expected total=9, got %d at offset=%d", total, offset)
		}
		for _, l := range page {
			if seen[l.ItemID] {
				t.Fatalf("pagination: duplicate %q at offset=%d", l.ItemID, offset)
			}
			seen[l.ItemID] = true
			allIDs = append(allIDs, l.ItemID)
		}
		if len(page) < 3 {
			break
		}
	}
	if len(seen) != 9 {
		t.Fatalf("pagination: expected 9 unique items, got %d (ids=%v)", len(seen), allIDs)
	}
}

func TestStoreFilterTieBreakerStability(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	// Both sort=score runs must return identical item order.
	f := models.MatchesFilter{Sort: "score"}
	r1, _, err1 := st.ListRecentListingsPaginated(userID, 100, 0, 0, f)
	r2, _, err2 := st.ListRecentListingsPaginated(userID, 100, 0, 0, f)
	if err1 != nil || err2 != nil {
		t.Fatalf("error in stability test: %v / %v", err1, err2)
	}
	if len(r1) != len(r2) {
		t.Fatalf("stability: counts differ %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].ItemID != r2[i].ItemID {
			t.Fatalf("stability: differ at %d: %q vs %q", i, r1[i].ItemID, r2[i].ItemID)
		}
	}

	// Items that share score=7.5 (sf-2 and sf-9) must appear in consistent
	// item_id ASC order: sf-2 < sf-9 lexicographically.
	var sf2Pos, sf9Pos int = -1, -1
	for i, l := range r1 {
		if l.ItemID == "sf-2" {
			sf2Pos = i
		}
		if l.ItemID == "sf-9" {
			sf9Pos = i
		}
	}
	if sf2Pos == -1 || sf9Pos == -1 {
		t.Fatalf("stability: sf-2 or sf-9 not found in result")
	}
	// "sf-2" < "sf-9" lexicographically, so sf-2 must appear before sf-9
	// (tie-breaker item_id ASC within the same score=7.5 bucket).
	if sf2Pos > sf9Pos {
		t.Fatalf("stability: tie-breaker failure — sf-2 (pos=%d) after sf-9 (pos=%d)", sf2Pos, sf9Pos)
	}
}

func TestStoreFilterTotalIsIndependentOfOffset(t *testing.T) {
	st, userID := setupFilterStore(t)
	defer st.Close()

	f := models.MatchesFilter{Condition: "good"} // 4 items
	_, total0, err0 := st.ListRecentListingsPaginated(userID, 1, 0, 0, f)
	_, total1, err1 := st.ListRecentListingsPaginated(userID, 1, 1, 0, f)
	_, total3, err3 := st.ListRecentListingsPaginated(userID, 1, 3, 0, f)
	if err0 != nil || err1 != nil || err3 != nil {
		t.Fatalf("total independence errors: %v / %v / %v", err0, err1, err3)
	}
	if total0 != 4 || total1 != 4 || total3 != 4 {
		t.Fatalf("total not independent of offset: offset=0→%d, offset=1→%d, offset=3→%d", total0, total1, total3)
	}
}

// ---------------------------------------------------------------
// Zero-price grouping: SQLite semantic test
//
// Verifies that offer_price = 0 is treated as unknown/unparsed and
// therefore sorted at the end (with NULLs) under both price_asc and
// price_desc — matching the intended Postgres behaviour expressed by
// the CASE expression in postgresOrderBy.
//
// Fixture: three rows — one with a real price, one with price=0, one
// with price=NULL (stored as 0 internally; both expected at end).
// ---------------------------------------------------------------

// setupZeroPriceStore creates a three-item fixture:
//
//	"zp-real"  offer_price = 25000
//	"zp-zero"  offer_price = 0
//	"zp-null"  offer_price = 0  (represents a NULL/unparsed price)
func setupZeroPriceStore(t *testing.T) (st *SQLiteStore, userID string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "zero-price-test.db")
	var err error
	st, err = New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	userID, err = st.CreateUser("zeroprice@example.com", "hash", "Zero Price")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	type fix struct {
		itemID     string
		offerPrice int
	}
	fixtures := []fix{
		{"zp-real", 25000},
		{"zp-zero", 0},
		{"zp-null", 0},
	}
	for _, f := range fixtures {
		l := models.Listing{
			ItemID:        f.itemID,
			Title:         "ZP " + f.itemID,
			Price:         30000,
			PriceType:     "fixed",
			MarketplaceID: "marktplaats",
			Condition:     "good",
		}
		scored := models.ScoredListing{
			Score:      5.0,
			OfferPrice: f.offerPrice,
		}
		if err := st.SaveListing(userID, l, "zero price query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", f.itemID, err)
		}
	}
	return st, userID
}

// TestStoreZeroPriceSortedLast verifies that rows with offer_price = 0
// appear after rows with a real (non-zero) price under both price_asc
// and price_desc, on the SQLite store path.
func TestStoreZeroPriceSortedLast(t *testing.T) {
	st, userID := setupZeroPriceStore(t)
	defer st.Close()

	t.Run("price_asc: zero-price rows at end", func(t *testing.T) {
		listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "price_asc"})
		if err != nil {
			t.Fatalf("price_asc error = %v", err)
		}
		if total != 3 {
			t.Fatalf("price_asc: expected total=3, got %d", total)
		}
		// The real-priced item must come before both zero-priced items.
		realIdx := -1
		for i, l := range listings {
			if l.ItemID == "zp-real" {
				realIdx = i
			}
		}
		if realIdx != 0 {
			t.Errorf("price_asc: expected zp-real at index 0 (before zero-price rows), got index %d", realIdx)
		}
		// All items after the real-priced item must have offer_price = 0.
		for i := realIdx + 1; i < len(listings); i++ {
			if listings[i].OfferPrice != 0 {
				t.Errorf("price_asc: item %q at index %d has offer_price=%d, expected 0 (zero-price should be last)",
					listings[i].ItemID, i, listings[i].OfferPrice)
			}
		}
	})

	t.Run("price_desc: zero-price rows at end", func(t *testing.T) {
		listings, total, err := st.ListRecentListingsPaginated(userID, 100, 0, 0, models.MatchesFilter{Sort: "price_desc"})
		if err != nil {
			t.Fatalf("price_desc error = %v", err)
		}
		if total != 3 {
			t.Fatalf("price_desc: expected total=3, got %d", total)
		}
		// The real-priced item must come first in descending order too.
		realIdx := -1
		for i, l := range listings {
			if l.ItemID == "zp-real" {
				realIdx = i
			}
		}
		if realIdx != 0 {
			t.Errorf("price_desc: expected zp-real at index 0 (before zero-price rows), got index %d", realIdx)
		}
		// All items after the real-priced item must have offer_price = 0.
		for i := realIdx + 1; i < len(listings); i++ {
			if listings[i].OfferPrice != 0 {
				t.Errorf("price_desc: item %q at index %d has offer_price=%d, expected 0 (zero-price should be last)",
					listings[i].ItemID, i, listings[i].OfferPrice)
			}
		}
	})
}

// ---------------------------------------------------------------
// Postgres ORDER BY helper unit tests
// These verify the SQL clause strings that will be sent to Postgres
// without needing a live Postgres connection.
// ---------------------------------------------------------------

func TestPostgresOrderByHelper(t *testing.T) {
	cases := []struct {
		sort     string
		wantSQL  string
		wantNull string // "NULLS LAST" should appear for price sorts
	}{
		{"newest", "ORDER BY last_seen DESC, item_id ASC", ""},
		{"", "ORDER BY last_seen DESC, item_id ASC", ""},          // zero-value = newest
		{"score", "ORDER BY score DESC, item_id ASC", ""},
		{"price_asc", "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price ASC NULLS LAST, item_id ASC", "NULLS LAST"},
		{"price_desc", "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price DESC NULLS LAST, item_id ASC", "NULLS LAST"},
	}
	for _, tc := range cases {
		got := postgresOrderBy(tc.sort)
		if got != tc.wantSQL {
			t.Errorf("postgresOrderBy(%q) = %q, want %q", tc.sort, got, tc.wantSQL)
		}
		if tc.wantNull != "" {
			found := false
			for i := 0; i < len(got)-len(tc.wantNull)+1; i++ {
				if got[i:i+len(tc.wantNull)] == tc.wantNull {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("postgresOrderBy(%q): expected %q in output %q", tc.sort, tc.wantNull, got)
			}
		}
	}
}

func TestSQLiteOrderByHelper(t *testing.T) {
	cases := []struct {
		sort    string
		wantSQL string
	}{
		{"newest", "ORDER BY last_seen DESC, item_id ASC"},
		{"", "ORDER BY last_seen DESC, item_id ASC"},
		{"score", "ORDER BY score DESC, item_id ASC"},
		// SQLite price sorts use CASE workaround for NULLS LAST
		{"price_asc", "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price ASC, item_id ASC"},
		{"price_desc", "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price DESC, item_id ASC"},
	}
	for _, tc := range cases {
		got := sqliteOrderBy(tc.sort)
		if got != tc.wantSQL {
			t.Errorf("sqliteOrderBy(%q) = %q, want %q", tc.sort, got, tc.wantSQL)
		}
	}
}
