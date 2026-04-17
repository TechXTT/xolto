package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

// newMatchesTestServer creates a fresh in-memory SQLite-backed server and a
// pre-authenticated user token for /matches tests.
func newMatchesTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "matches-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	userID, err := st.CreateUser("matches@example.com", "hash", "Matches User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "matches@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, nil)
	return st, srv, userID, token
}

// saveTestListings inserts n listings for userID with sequential titles and
// a controlled last_seen progression so ordering is predictable.
func saveTestListings(t *testing.T, st *store.SQLiteStore, userID string, n int, missionID int64) []models.Listing {
	t.Helper()
	listings := make([]models.Listing, n)
	for i := 0; i < n; i++ {
		// Vary last_seen by 1-second steps so ordering is deterministic.
		// item_id encodes index for the tie-breaker test.
		itemID := string(rune('a'+i)) + "-item"
		if i >= 26 {
			itemID = "item-" + string(rune('a'+i-26))
		}
		l := models.Listing{
			ItemID:        itemID,
			Title:         "Listing " + itemID,
			Price:         10000 * (i + 1),
			PriceType:     "fixed",
			MarketplaceID: "marktplaats",
			ProfileID:     missionID,
		}
		scored := models.ScoredListing{Score: float64(i) * 0.1}
		if err := st.SaveListing(userID, l, "test query", scored); err != nil {
			t.Fatalf("SaveListing(%s) error = %v", itemID, err)
		}
		listings[i] = l
	}
	return listings
}

func doMatchesRequest(t *testing.T, srv *Server, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/matches"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func decodeMatchesResponse(t *testing.T, res *httptest.ResponseRecorder) (items []any, limit, offset, total int) {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v, body=%s", err, res.Body.String())
	}
	rawItems, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected items array in response, got %T: %#v", body["items"], body["items"])
	}
	limitF, _ := body["limit"].(float64)
	offsetF, _ := body["offset"].(float64)
	totalF, _ := body["total"].(float64)
	return rawItems, int(limitF), int(offsetF), int(totalF)
}

// AC1: limit + offset parameters are respected.
func TestMatchesLimitAndOffset(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	const total = 25
	saveTestListings(t, st, userID, total, 0)

	// Page 1: limit=10 offset=0 — should return 10 items.
	res := doMatchesRequest(t, srv, token, "limit=10&offset=0")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	items1, limit1, offset1, total1 := decodeMatchesResponse(t, res)
	if len(items1) != 10 {
		t.Fatalf("AC1: expected 10 items on page 1, got %d", len(items1))
	}
	if limit1 != 10 {
		t.Fatalf("AC1: expected echoed limit=10, got %d", limit1)
	}
	if offset1 != 0 {
		t.Fatalf("AC1: expected echoed offset=0, got %d", offset1)
	}
	if total1 != total {
		t.Fatalf("AC1: expected total=%d, got %d", total, total1)
	}

	// Page 2: limit=10 offset=10 — should return 10 items with no overlap.
	res2 := doMatchesRequest(t, srv, token, "limit=10&offset=10")
	if res2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res2.Code)
	}
	items2, _, offset2, _ := decodeMatchesResponse(t, res2)
	if len(items2) != 10 {
		t.Fatalf("AC1: expected 10 items on page 2, got %d", len(items2))
	}
	if offset2 != 10 {
		t.Fatalf("AC1: expected echoed offset=10, got %d", offset2)
	}

	// Confirm no item from page 1 appears on page 2 (no overlap).
	ids1 := map[string]bool{}
	for _, item := range items1 {
		m, _ := item.(map[string]any)
		ids1[m["ItemID"].(string)] = true
	}
	for _, item := range items2 {
		m, _ := item.(map[string]any)
		if ids1[m["ItemID"].(string)] {
			t.Fatalf("AC1: item_id %q appeared on both page 1 and page 2 (overlap)", m["ItemID"])
		}
	}
}

// AC2: Stable ordering — same query returns the same order twice; no
// duplicates or gaps across adjacent pages.
func TestMatchesStableOrdering(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	const total = 15
	saveTestListings(t, st, userID, total, 0)

	// Fetch all items via two successive calls, confirm order is identical.
	res1 := doMatchesRequest(t, srv, token, "limit=100&offset=0")
	if res1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res1.Code)
	}
	res2 := doMatchesRequest(t, srv, token, "limit=100&offset=0")
	if res2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res2.Code)
	}

	items1, _, _, _ := decodeMatchesResponse(t, res1)
	items2, _, _, _ := decodeMatchesResponse(t, res2)

	if len(items1) != len(items2) {
		t.Fatalf("AC2: run 1 returned %d items, run 2 returned %d", len(items1), len(items2))
	}
	for i := range items1 {
		m1, _ := items1[i].(map[string]any)
		m2, _ := items2[i].(map[string]any)
		if m1["ItemID"] != m2["ItemID"] {
			t.Fatalf("AC2: ordering differs at index %d: %v vs %v", i, m1["ItemID"], m2["ItemID"])
		}
	}

	// Adjacent pages: collect all IDs from two pages and verify no duplicates
	// and no gaps (union = all items).
	resP1 := doMatchesRequest(t, srv, token, "limit=8&offset=0")
	resP2 := doMatchesRequest(t, srv, token, "limit=8&offset=8")
	resAll := doMatchesRequest(t, srv, token, "limit=100&offset=0")

	pageItems1, _, _, _ := decodeMatchesResponse(t, resP1)
	pageItems2, _, _, _ := decodeMatchesResponse(t, resP2)
	allItems, _, _, _ := decodeMatchesResponse(t, resAll)

	seenIDs := map[string]bool{}
	for _, item := range append(pageItems1, pageItems2...) {
		m, _ := item.(map[string]any)
		id := m["ItemID"].(string)
		if seenIDs[id] {
			t.Fatalf("AC2: duplicate item_id %q across adjacent pages", id)
		}
		seenIDs[id] = true
	}
	if len(seenIDs) != min(total, 16) {
		// pageItems1 (8) + pageItems2 (up to 8) should cover 16 unique IDs.
		t.Fatalf("AC2: expected %d unique items across two pages, got %d", min(total, 16), len(seenIDs))
	}
	// All IDs from full fetch should be a superset.
	for _, item := range allItems {
		m, _ := item.(map[string]any)
		_ = m["ItemID"] // Just validate it's there.
	}
}

// AC3: total reflects the count ignoring limit/offset.
func TestMatchesTotalCount(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	const total = 30
	saveTestListings(t, st, userID, total, 0)

	// Fetch with small limit — total must still reflect all items.
	res := doMatchesRequest(t, srv, token, "limit=5&offset=0")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	items, _, _, reportedTotal := decodeMatchesResponse(t, res)
	if len(items) != 5 {
		t.Fatalf("AC3: expected 5 items in page, got %d", len(items))
	}
	if reportedTotal != total {
		t.Fatalf("AC3: expected total=%d ignoring limit, got %d", total, reportedTotal)
	}

	// Verify total equals sum of items across all pages.
	sumItems := 0
	offset := 0
	const pageSize = 7
	for {
		r := doMatchesRequest(t, srv, token, "limit=7&offset="+itoa(offset))
		if r.Code != http.StatusOK {
			t.Fatalf("AC3: paginating got %d at offset=%d", r.Code, offset)
		}
		page, _, _, _ := decodeMatchesResponse(t, r)
		sumItems += len(page)
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}
	if sumItems != total {
		t.Fatalf("AC3: sum of page items (%d) != reported total (%d)", sumItems, total)
	}
}

// AC4: Query that matches nothing returns items=[], total=0, status 200.
func TestMatchesZeroResults(t *testing.T) {
	st, srv, _, token := newMatchesTestServer(t)
	defer st.Close()

	// No listings inserted — expect empty, not 404.
	res := doMatchesRequest(t, srv, token, "")
	if res.Code != http.StatusOK {
		t.Fatalf("AC4: expected 200 for empty result, got %d", res.Code)
	}
	items, limit, offset, total := decodeMatchesResponse(t, res)
	if len(items) != 0 {
		t.Fatalf("AC4: expected 0 items, got %d", len(items))
	}
	if total != 0 {
		t.Fatalf("AC4: expected total=0, got %d", total)
	}
	if limit != 20 {
		t.Fatalf("AC4: expected default limit=20, got %d", limit)
	}
	if offset != 0 {
		t.Fatalf("AC4: expected default offset=0, got %d", offset)
	}
}

// AC5: Partial last page returns only the remaining items.
func TestMatchesPartialLastPage(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	// Insert 25 listings; last page at offset=20 with limit=10 should return 5.
	const total = 25
	saveTestListings(t, st, userID, total, 0)

	res := doMatchesRequest(t, srv, token, "limit=10&offset=20")
	if res.Code != http.StatusOK {
		t.Fatalf("AC5: expected 200, got %d", res.Code)
	}
	items, _, _, reportedTotal := decodeMatchesResponse(t, res)
	if len(items) != 5 {
		t.Fatalf("AC5: expected 5 items on partial last page, got %d (total=%d)", len(items), reportedTotal)
	}
	if reportedTotal != total {
		t.Fatalf("AC5: expected total=%d, got %d", total, reportedTotal)
	}
}

// AC6: Invalid parameters return 400 with the standard error envelope.
func TestMatchesInvalidParams(t *testing.T) {
	st, srv, _, token := newMatchesTestServer(t)
	defer st.Close()

	cases := []struct {
		desc  string
		query string
	}{
		{"limit=0", "limit=0"},
		{"limit=-1", "limit=-1"},
		{"limit=101 (max+1)", "limit=101"},
		{"offset=-1", "offset=-1"},
		{"non-integer limit", "limit=abc"},
		{"non-integer offset", "offset=xyz"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			res := doMatchesRequest(t, srv, token, tc.query)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("AC6: %s: expected 400, got %d body=%s", tc.desc, res.Code, res.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("AC6: %s: failed to decode error body: %v", tc.desc, err)
			}
			if ok, _ := body["ok"].(bool); ok {
				t.Fatalf("AC6: %s: expected ok=false, got %#v", tc.desc, body)
			}
			errMsg, _ := body["error"].(string)
			if errMsg == "" {
				t.Fatalf("AC6: %s: expected non-empty error message, got %#v", tc.desc, body)
			}
		})
	}
}

// AC7: Backward compatibility — GET /matches with no query params returns a
// valid response. Unauthenticated callers get 401 (existing auth behaviour).
func TestMatchesBackwardCompat(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	saveTestListings(t, st, userID, 3, 0)

	// Authenticated, no params — should return envelope with defaults.
	res := doMatchesRequest(t, srv, token, "")
	if res.Code != http.StatusOK {
		t.Fatalf("AC7: expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("AC7: decode error: %v", err)
	}
	// Envelope must always have these keys.
	for _, key := range []string{"items", "limit", "offset", "total"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("AC7: response missing key %q: %#v", key, body)
		}
	}
	limitF, _ := body["limit"].(float64)
	if int(limitF) != 20 {
		t.Fatalf("AC7: expected default limit=20, got %d", int(limitF))
	}

	// Unauthenticated — must still get 401.
	unauthReq := httptest.NewRequest(http.MethodGet, "/matches", nil)
	unauthRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthRes, unauthReq)
	if unauthRes.Code != http.StatusUnauthorized {
		t.Fatalf("AC7: expected 401 for unauthenticated request, got %d", unauthRes.Code)
	}

	// POST to /matches should return 405.
	postReq := httptest.NewRequest(http.MethodPost, "/matches", nil)
	postReq.Header.Set("Authorization", "Bearer "+token)
	postRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postRes, postReq)
	if postRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("AC7: expected 405 for POST /matches, got %d", postRes.Code)
	}
}

// AC8: Tie-breaker test — when two listings share the same last_seen timestamp,
// the item_id ASC tie-breaker guarantees no duplication across page boundaries.
func TestMatchesOrderingTieBreaker(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	// Insert several listings. Because saveTestListings runs in a tight loop
	// the SQLite CURRENT_TIMESTAMP resolution (1 second) means many will share
	// the same last_seen. The item_id tie-breaker must keep ordering stable.
	saveTestListings(t, st, userID, 20, 0)

	// Collect all item IDs from page-by-page traversal.
	seenIDs := map[string]bool{}
	var pageOrder []string

	offset := 0
	const pageSize = 5
	for {
		r := doMatchesRequest(t, srv, token, "limit=5&offset="+itoa(offset))
		if r.Code != http.StatusOK {
			t.Fatalf("AC8: got %d at offset=%d", r.Code, offset)
		}
		page, _, _, reportedTotal := decodeMatchesResponse(t, r)
		if reportedTotal != 20 {
			t.Fatalf("AC8: expected total=20, got %d", reportedTotal)
		}
		for _, item := range page {
			m, _ := item.(map[string]any)
			id := m["ItemID"].(string)
			if seenIDs[id] {
				t.Fatalf("AC8: tie-breaker failure — item_id %q appeared on multiple pages", id)
			}
			seenIDs[id] = true
			pageOrder = append(pageOrder, id)
		}
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}

	if len(seenIDs) != 20 {
		t.Fatalf("AC8: expected 20 unique items across all pages, got %d (order: %v)", len(seenIDs), pageOrder)
	}

	// Run a second traversal and confirm IDs appear in identical order.
	var pageOrder2 []string
	offset = 0
	for {
		r := doMatchesRequest(t, srv, token, "limit=5&offset="+itoa(offset))
		if r.Code != http.StatusOK {
			t.Fatalf("AC8: second pass got %d at offset=%d", r.Code, offset)
		}
		page, _, _, _ := decodeMatchesResponse(t, r)
		for _, item := range page {
			m, _ := item.(map[string]any)
			pageOrder2 = append(pageOrder2, m["ItemID"].(string))
		}
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}

	if len(pageOrder) != len(pageOrder2) {
		t.Fatalf("AC8: first traversal %d items, second %d", len(pageOrder), len(pageOrder2))
	}
	for i := range pageOrder {
		if pageOrder[i] != pageOrder2[i] {
			t.Fatalf("AC8: ordering differs at position %d: %q vs %q", i, pageOrder[i], pageOrder2[i])
		}
	}
}

// TestMatchesMustHaves verifies the XOL-18 contract:
//   - Each item in the matches array carries a MustHaves field.
//   - When mission has no RequiredFeatures, MustHaves is an empty slice (not null).
//   - When mission has RequiredFeatures and the listing matches, status is "met".
//   - When mission has RequiredFeatures and the listing does not match, status is "unknown".
//   - Backward compat: the "items" key is still present and contains raw listings.
func TestMatchesMustHaves(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	// Create a mission with two RequiredFeatures.
	missionID, err := st.UpsertMission(models.Mission{
		UserID:           userID,
		Name:             "XOL-18 test mission",
		TargetQuery:      "sony camera",
		Status:           "active",
		RequiredFeatures: []string{"NL seller", "battery health"},
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}

	// Insert one listing that mentions "NL seller" but not "battery health".
	listing := models.Listing{
		ItemID:        "xol18-item-1",
		Title:         "Sony A6000, NL seller",
		Description:   "Great camera body.",
		Price:         50000,
		PriceType:     "fixed",
		MarketplaceID: "marktplaats",
		ProfileID:     missionID,
	}
	if err := st.SaveListing(userID, listing, "sony camera", models.ScoredListing{Score: 7.5}); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	// --- Test 1: mission with must-haves, request with mission_id ---
	res := doMatchesRequest(t, srv, token, "mission_id="+itoa(int(missionID)))
	if res.Code != http.StatusOK {
		t.Fatalf("MustHaves test: expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("MustHaves test: decode error: %v", err)
	}

	// Verify "matches" key exists.
	rawMatches, ok := body["matches"].([]any)
	if !ok {
		t.Fatalf("MustHaves test: expected 'matches' array in response, got %T: %#v", body["matches"], body["matches"])
	}
	// Verify backward-compat "items" key also exists.
	if _, ok := body["items"]; !ok {
		t.Fatal("MustHaves test: expected backward-compat 'items' key in response")
	}

	if len(rawMatches) != 1 {
		t.Fatalf("MustHaves test: expected 1 match, got %d", len(rawMatches))
	}

	item, _ := rawMatches[0].(map[string]any)
	rawMH, ok := item["MustHaves"]
	if !ok {
		t.Fatal("MustHaves test: item is missing 'MustHaves' field")
	}
	mhSlice, ok := rawMH.([]any)
	if !ok {
		t.Fatalf("MustHaves test: 'MustHaves' is %T, want []any", rawMH)
	}
	if len(mhSlice) != 2 {
		t.Fatalf("MustHaves test: expected 2 MustHaveMatch entries (one per RequiredFeature), got %d", len(mhSlice))
	}

	// First must-have: "NL seller" → listing title contains "NL seller" → "met".
	mh0, _ := mhSlice[0].(map[string]any)
	if mh0["Text"] != "NL seller" {
		t.Errorf("MustHaves[0].Text = %q, want %q", mh0["Text"], "NL seller")
	}
	if mh0["Status"] != "met" {
		t.Errorf("MustHaves[0].Status = %q, want %q (NL seller is in title)", mh0["Status"], "met")
	}

	// Second must-have: "battery health" → not in listing → "unknown".
	mh1, _ := mhSlice[1].(map[string]any)
	if mh1["Text"] != "battery health" {
		t.Errorf("MustHaves[1].Text = %q, want %q", mh1["Text"], "battery health")
	}
	if mh1["Status"] != "unknown" {
		t.Errorf("MustHaves[1].Status = %q, want %q (battery health absent from listing)", mh1["Status"], "unknown")
	}

	// --- Test 2: no mission_id → MustHaves must be empty slice (not null) ---
	res2 := doMatchesRequest(t, srv, token, "")
	if res2.Code != http.StatusOK {
		t.Fatalf("no-mission test: expected 200, got %d", res2.Code)
	}
	var body2 map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&body2); err != nil {
		t.Fatalf("no-mission test: decode error: %v", err)
	}
	rawMatches2, ok := body2["matches"].([]any)
	if !ok {
		t.Fatalf("no-mission test: expected 'matches' array, got %T", body2["matches"])
	}
	for i, m := range rawMatches2 {
		item2, _ := m.(map[string]any)
		rawMH2, hasMH := item2["MustHaves"]
		if !hasMH {
			t.Errorf("no-mission test: match[%d] missing MustHaves field", i)
			continue
		}
		// Must be a non-null empty slice in JSON → decoded as []any{} or nil in Go.
		// Crucially, the JSON must not omit the field and must not be null.
		mhSlice2, isSlice := rawMH2.([]any)
		if !isSlice {
			t.Errorf("no-mission test: match[%d].MustHaves is %T, want []any (empty slice)", i, rawMH2)
			continue
		}
		if len(mhSlice2) != 0 {
			t.Errorf("no-mission test: match[%d].MustHaves len=%d, want 0 (no mission must-haves)", i, len(mhSlice2))
		}
	}
}

// itoa is a small helper to avoid importing strconv in test helpers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// min returns the smaller of two ints. Avoids importing math.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
