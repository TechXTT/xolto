package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/replycopilot"
	"github.com/TechXTT/xolto/internal/store"
)

// stubReplyClassifier is a test double for replycopilot.Classifier.
type stubReplyClassifier struct {
	result replycopilot.ClassifyResult
	err    error
}

func (s *stubReplyClassifier) Classify(_ context.Context, _ string) (replycopilot.ClassifyResult, error) {
	return s.result, s.err
}

// newReplyCopilotTestServer creates a test server with a real SQLiteStore and
// an injected stub classifier. Returns the store, server, userID, and token.
func newReplyCopilotTestServer(t *testing.T, classifier replycopilot.Classifier) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "reply-copilot-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	userID, err := st.CreateUser("buyer@example.com", "hash", "Buyer")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "buyer@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, nil)
	// Inject the classifier after construction so tests can control it.
	srv.replyClassifier = classifier
	return st, srv, userID, token
}

func saveReplyCopilotTestListing(t *testing.T, st *store.SQLiteStore, userID, itemID string, listing models.Listing) {
	t.Helper()
	listing.ItemID = itemID
	scored := models.ScoredListing{
		Score:             7.0,
		FairPrice:         listing.FairPrice,
		Confidence:        0.8,
		RecommendedAction: "negotiate",
		RiskFlags:         []string{},
	}
	if err := st.SaveListing(userID, listing, "test query", scored); err != nil {
		t.Fatalf("SaveListing(%q) error = %v", itemID, err)
	}
}

func TestReplyCopilotRejectsNonPost(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{}}
	_, srv, _, token := newReplyCopilotTestServer(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/reply-copilot", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestReplyCopilotRequiresAuth(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{}}
	_, srv, _, _ := newReplyCopilotTestServer(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot",
		strings.NewReader(`{"listing_id":"item1","seller_reply":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
}

func TestReplyCopilotRejectsMissingListingID(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{}}
	_, srv, _, token := newReplyCopilotTestServer(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot",
		strings.NewReader(`{"listing_id":"","seller_reply":"hello there"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestReplyCopilotRejectsMissingSellerReply(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{}}
	_, srv, _, token := newReplyCopilotTestServer(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot",
		strings.NewReader(`{"listing_id":"item1","seller_reply":""}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestReplyCopilotReturns404ForUnknownListing(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{}}
	_, srv, _, token := newReplyCopilotTestServer(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot",
		strings.NewReader(`{"listing_id":"nonexistent_item","seller_reply":"I can go lower"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.Code)
	}
}

func TestReplyCopilotValidRequest_Returns200WithExpectedFields(t *testing.T) {
	stub := &stubReplyClassifier{result: replycopilot.ClassifyResult{
		Interpretation: replycopilot.InterpNegotiable,
		Confidence:     replycopilot.ConfMedium,
		Signals:        []string{},
	}}
	st, srv, userID, token := newReplyCopilotTestServer(t, stub)

	listing := models.Listing{
		Title:         "Sony A7 IV",
		Description:   "Camera in good condition.",
		Price:         180000,
		FairPrice:     160000,
		Condition:     "good",
		MarketplaceID: "marktplaats",
	}
	const itemID = "rc_test_001"
	saveReplyCopilotTestListing(t, st, userID, itemID, listing)

	body := `{"listing_id":"` + itemID + `","seller_reply":"I could come down a bit on the price."}`
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	requiredFields := []string{"interpretation", "recommended_action", "draft_next_message", "confidence", "signals", "lang"}
	for _, field := range requiredFields {
		if _, ok := resp[field]; !ok {
			t.Errorf("response missing field %q", field)
		}
	}

	if interp, _ := resp["interpretation"].(string); interp == "" {
		t.Error("interpretation must not be empty")
	}
	if action, _ := resp["recommended_action"].(string); action == "" {
		t.Error("recommended_action must not be empty")
	}
	if draft, _ := resp["draft_next_message"].(string); strings.TrimSpace(draft) == "" {
		t.Error("draft_next_message must not be empty")
	}
	if lang, _ := resp["lang"].(string); lang == "" {
		t.Error("lang must not be empty")
	}
	signals, ok := resp["signals"].([]any)
	if !ok {
		t.Errorf("signals must be a JSON array, got %T", resp["signals"])
	}
	_ = signals
}

func TestReplyCopilotNilClassifier_Returns503(t *testing.T) {
	// When no classifier is configured (nil), endpoint must return 503.
	// The 503 is returned before listing lookup, so no need to save a listing.
	st, srv, _, token := newReplyCopilotTestServer(t, nil)
	_ = st

	body := `{"listing_id":"any_item","seller_reply":"The price is the price."}`
	req := httptest.NewRequest(http.MethodPost, "/reply-copilot", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when classifier nil, got %d body=%s", res.Code, res.Body.String())
	}
}
