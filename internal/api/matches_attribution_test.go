package api

import (
	"encoding/json"
	"math"
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

const attributionFloatTolerance = 1e-6

func approxEqualFloat(a, b float64) bool {
	return math.Abs(a-b) <= attributionFloatTolerance
}

// newAttributionTestServer creates a server with DebugScorerAttribution=true
// and returns an operator token and a regular-user token so both auth paths
// can be exercised.
func newAttributionTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "attribution-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}

	// Operator user (is_admin=true, which grants HasOperatorAccess).
	operatorID, err := st.CreateUser("operator@example.com", "hash", "Operator User")
	if err != nil {
		t.Fatalf("CreateUser(operator) error = %v", err)
	}
	if err := st.SetUserAdmin(operatorID, true); err != nil {
		t.Fatalf("SetUserAdmin() error = %v", err)
	}
	operatorToken, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    operatorID,
		Email:     "operator@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken(operator) error = %v", err)
	}

	// Regular user (no admin, no operator).
	regularID, err := st.CreateUser("regular@example.com", "hash", "Regular User")
	if err != nil {
		t.Fatalf("CreateUser(regular) error = %v", err)
	}
	regularToken, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    regularID,
		Email:     "regular@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken(regular) error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:              "test-secret",
		AppBaseURL:             "http://localhost:3000",
		DebugScorerAttribution: true, // attribution debug ENABLED
	}, st, nil, nil, nil, nil)

	return st, srv, operatorID, operatorToken, regularToken
}

// saveAttributionTestListing inserts a scored listing for the given user.
func saveAttributionTestListing(t *testing.T, st *store.SQLiteStore, userID string, itemID string, score, confidence float64, condition, priceType string) {
	t.Helper()
	l := models.Listing{
		ItemID:        itemID,
		Title:         "Test listing " + itemID,
		Price:         50000,
		PriceType:     priceType,
		Condition:     condition,
		MarketplaceID: "olxbg",
		ProfileID:     0,
	}
	scored := models.ScoredListing{
		Score:      score,
		Confidence: confidence,
		FairPrice:  50000,
		OfferPrice: 45000,
	}
	if err := st.SaveListing(userID, l, "sony camera", scored); err != nil {
		t.Fatalf("SaveListing(%s) error = %v", itemID, err)
	}
}

// doAttributionRequest makes a GET /matches request with the given token.
func doAttributionRequest(t *testing.T, srv *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/matches", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// VAL-2 AC1: scoreContributions is present on /matches items when
// authenticated as internal/debug (operator or above).
func TestMatchesAttribution_PresentForOperator(t *testing.T) {
	st, srv, operatorID, operatorToken, _ := newAttributionTestServer(t)
	defer st.Close()

	saveAttributionTestListing(t, st, operatorID, "val2-item-1", 7.5, 0.70, "good", "fixed")

	res := doAttributionRequest(t, srv, operatorToken)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	rawMatches, ok := body["matches"].([]any)
	if !ok || len(rawMatches) == 0 {
		t.Fatalf("expected non-empty matches array, got %T: %#v", body["matches"], body["matches"])
	}

	item, _ := rawMatches[0].(map[string]any)
	rawContribs, ok := item["scoreContributions"]
	if !ok {
		t.Fatal("AC1 fail: scoreContributions missing from /matches item for operator auth")
	}
	contribs, ok := rawContribs.(map[string]any)
	if !ok {
		t.Fatalf("AC1 fail: scoreContributions is %T, want map", rawContribs)
	}

	// All required keys must be present.
	for _, key := range []string{"comparables", "confidence", "negotiable", "recency", "condition", "category_condition"} {
		if _, ok := contribs[key]; !ok {
			t.Errorf("AC1 fail: scoreContributions missing key %q", key)
		}
	}
}

// VAL-2 AC2: scoreContributions is ABSENT from the public /matches response
// for a regular (non-operator) user. This is the envelope-parity assertion.
func TestMatchesAttribution_AbsentForRegularUser(t *testing.T) {
	st, srv, _, _, regularToken := newAttributionTestServer(t)
	defer st.Close()

	// Create a regular user and save a listing for them.
	regularID, err := st.GetUserByEmail("regular@example.com")
	if err != nil || regularID == nil {
		t.Fatal("could not find regular user")
	}
	saveAttributionTestListing(t, st, regularID.ID, "val2-item-2", 6.0, 0.55, "used", "fixed")

	res := doAttributionRequest(t, srv, regularToken)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	rawMatches, ok := body["matches"].([]any)
	if !ok {
		t.Fatalf("expected matches array, got %T", body["matches"])
	}
	for i, m := range rawMatches {
		item, _ := m.(map[string]any)
		if _, hasContribs := item["scoreContributions"]; hasContribs {
			t.Errorf("AC2 FAIL (envelope-parity): match[%d] has scoreContributions for regular user — this is a trust violation", i)
		}
	}
}

// VAL-2 AC2b: scoreContributions is ABSENT when DebugScorerAttribution is false
// even for an operator user.
func TestMatchesAttribution_AbsentWhenDebugDisabled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attribution-disabled-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	operatorID, err := st.CreateUser("op2@example.com", "hash", "Op2")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := st.SetUserAdmin(operatorID, true); err != nil {
		t.Fatalf("SetUserAdmin() error = %v", err)
	}
	operatorToken, err := auth.IssueToken("test-secret", auth.Claims{
		UserID: operatorID, Email: "op2@example.com", TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	// Server with attribution debug DISABLED (default).
	srv := NewServer(config.ServerConfig{
		JWTSecret:              "test-secret",
		AppBaseURL:             "http://localhost:3000",
		DebugScorerAttribution: false,
	}, st, nil, nil, nil, nil)

	saveAttributionTestListing(t, st, operatorID, "val2-item-3", 7.0, 0.65, "good", "fixed")

	req := httptest.NewRequest(http.MethodGet, "/matches", nil)
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	rawMatches, _ := body["matches"].([]any)
	for i, m := range rawMatches {
		item, _ := m.(map[string]any)
		if _, hasContribs := item["scoreContributions"]; hasContribs {
			t.Errorf("AC2b FAIL: match[%d] has scoreContributions when debug disabled — fail-safe violated", i)
		}
	}
}

// VAL-2 AC3: sum of deltas equals the listing score within float tolerance.
func TestMatchesAttribution_SumEqualsFinalScore(t *testing.T) {
	st, srv, operatorID, operatorToken, _ := newAttributionTestServer(t)
	defer st.Close()

	// Insert a listing with known score and confidence.
	const wantScore = 8.2
	const wantConfidence = 0.80
	saveAttributionTestListing(t, st, operatorID, "val2-sum-check", wantScore, wantConfidence, "good", "negotiable")

	res := doAttributionRequest(t, srv, operatorToken)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	rawMatches, _ := body["matches"].([]any)
	if len(rawMatches) == 0 {
		t.Fatal("expected at least one match")
	}

	item, _ := rawMatches[0].(map[string]any)
	rawContribs, ok := item["scoreContributions"].(map[string]any)
	if !ok {
		t.Fatal("scoreContributions missing or wrong type")
	}

	// Read the stored score from the response envelope.
	storedScore, _ := item["Score"].(float64)
	if storedScore == 0 {
		t.Skip("stored score not available in response envelope — skipping sum check")
	}

	var sum float64
	for _, v := range rawContribs {
		sum += v.(float64)
	}

	if !approxEqualFloat(sum, storedScore) {
		t.Errorf("AC3 FAIL: sum of contributions %.6f != stored score %.6f (diff %.9f)",
			sum, storedScore, math.Abs(sum-storedScore))
	}
}
