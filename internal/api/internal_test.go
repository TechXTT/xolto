package api

// VAL-1a: tests for GET /internal/calibration/summary.
//
// Covers:
//   - unauthenticated request → 401
//   - authenticated regular user → 403
//   - authenticated operator → 200 with correct summary shape
//   - window / marketplace query params respected
//   - envelope-parity: public GET /matches response does NOT contain
//     any calibration-related field

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/store"
)

// newCalibrationTestServer creates a minimal test server with one operator
// user and one regular user. Returns (store, server, operatorID, regularUserID).
func newCalibrationTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cal-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Create operator user (role = "operator").
	operatorID, err := st.CreateUser("operator@example.com", "hash", "Operator")
	if err != nil {
		t.Fatalf("CreateUser(operator) error = %v", err)
	}
	if err := st.UpdateUserRole(operatorID, "operator"); err != nil {
		t.Fatalf("UpdateUserRole(operator) error = %v", err)
	}

	// Create regular user.
	regularID, err := st.CreateUser("user@example.com", "hash", "Regular")
	if err != nil {
		t.Fatalf("CreateUser(regular) error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:          "test-secret",
		AppBaseURL:         "http://localhost:3000",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}, st, nil, nil, nil, nil)

	return st, srv, operatorID, regularID
}

func issueTokenForUser(t *testing.T, userID, email string) string {
	t.Helper()
	tok, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     email,
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	return tok
}

func TestCalibrationSummaryUnauthenticated(t *testing.T) {
	_, srv, _, _ := newCalibrationTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/internal/calibration/summary", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d, want 401", res.Code)
	}
}

func TestCalibrationSummaryRegularUserForbidden(t *testing.T) {
	_, srv, _, regularID := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, regularID, "user@example.com")
	req := httptest.NewRequest(http.MethodGet, "/internal/calibration/summary", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Errorf("regular user: status = %d, want 403", res.Code)
	}
}

func TestCalibrationSummaryOperatorOK(t *testing.T) {
	st, srv, operatorID, _ := newCalibrationTestServer(t)

	// Seed a scoring event so the summary is non-empty.
	ctx := context.Background()
	if err := st.WriteScoringEvent(ctx, store.ScoringEvent{
		ListingID:     "int-test-1",
		Marketplace:   "olxbg",
		Score:         8.0,
		Verdict:       "buy",
		Confidence:    0.80,
		Contributions: map[string]float64{"comparables": 2.0},
		ScorerVersion: store.ScorerVersionV1,
	}); err != nil {
		t.Fatalf("WriteScoringEvent() error = %v", err)
	}

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	req := httptest.NewRequest(http.MethodGet, "/internal/calibration/summary?window=1d&marketplace=olxbg", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("operator: status = %d, want 200; body = %s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("ok field = false, want true")
	}
	summaryRaw, hasSummary := body["summary"]
	if !hasSummary {
		t.Fatal("response missing 'summary' key")
	}
	summaryMap, ok := summaryRaw.(map[string]any)
	if !ok {
		t.Fatalf("summary is not an object: %T", summaryRaw)
	}

	// Shape assertions.
	requiredKeys := []string{
		"window_days", "total_events", "verdict_counts",
		"confidence_histogram", "fair_price_delta", "outcome_attribution",
	}
	for _, k := range requiredKeys {
		if _, has := summaryMap[k]; !has {
			t.Errorf("summary missing key %q", k)
		}
	}

	// total_events must be at least 1.
	if total, _ := summaryMap["total_events"].(float64); total < 1 {
		t.Errorf("total_events = %v, want >= 1", total)
	}
}

func TestCalibrationSummaryWindowParam(t *testing.T) {
	_, srv, operatorID, _ := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	for _, window := range []string{"1d", "7d", "30d", "90d", ""} {
		url := "/internal/calibration/summary"
		if window != "" {
			url += "?window=" + window
		}
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Errorf("window=%q: status = %d, want 200", window, res.Code)
		}
	}
}

func TestCalibrationSummaryMethodNotAllowed(t *testing.T) {
	_, srv, operatorID, _ := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	req := httptest.NewRequest(http.MethodPost, "/internal/calibration/summary", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", res.Code)
	}
}

// TestMatchesEnvelopeParity asserts that a regular GET /matches response
// contains NO calibration-related fields (VAL-1a envelope-parity guardrail).
// This complements the VAL-2 scoreContributions parity test.
func TestMatchesEnvelopeParity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "parity-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("parity@example.com", "hash", "Parity User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, nil)

	tok := issueTokenForUser(t, userID, "parity@example.com")
	req := httptest.NewRequest(http.MethodGet, "/matches", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("GET /matches: status = %d, want 200", res.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// None of these calibration-internal fields must appear in the public response.
	calibrationOnlyFields := []string{
		"summary",
		"scoring_events",
		"calibration",
		"verdict_counts",
		"confidence_histogram",
		"fair_price_delta",
		"outcome_attribution",
		"window_days",
	}
	for _, field := range calibrationOnlyFields {
		if _, found := body[field]; found {
			t.Errorf("/matches response contains calibration field %q — must NOT be present", field)
		}
	}

	// Also assert at listing level (listings is an array).
	if listings, ok := body["listings"].([]any); ok {
		for i, raw := range listings {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range calibrationOnlyFields {
				if _, found := item[field]; found {
					t.Errorf("/matches listings[%d] contains calibration field %q", i, field)
				}
			}
		}
	}
}

// TestParseCalibrationWindow covers the window string parser.
func TestParseCalibrationWindow(t *testing.T) {
	cases := []struct {
		input string
		want  time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"14d", 14 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
		{"", 7 * 24 * time.Hour},        // default
		{"invalid", 7 * 24 * time.Hour}, // default
		{"365d", 7 * 24 * time.Hour},    // not explicitly supported → default
	}
	for _, tc := range cases {
		got := parseCalibrationWindow(tc.input)
		if got != tc.want {
			t.Errorf("parseCalibrationWindow(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
