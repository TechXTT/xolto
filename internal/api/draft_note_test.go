package api

import (
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
	"github.com/TechXTT/xolto/internal/store"
)

// saveDraftNoteTestListing stores a listing under userID+itemID using
// SaveListing so GetListing can retrieve it during the handler test.
func saveDraftNoteTestListing(t *testing.T, st *store.SQLiteStore, userID, itemID string, listing models.Listing, scored models.ScoredListing) {
	t.Helper()
	listing.ItemID = itemID
	if err := st.SaveListing(userID, listing, "test query", scored); err != nil {
		t.Fatalf("SaveListing(%q) error = %v", itemID, err)
	}
}

func newDraftNoteTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "draft-note-test.db")
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
	return st, srv, userID, token
}

func TestDraftNoteRejectsNonPost(t *testing.T) {
	_, srv, _, token := newDraftNoteTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/draft-note", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", res.Code)
	}
}

func TestDraftNoteRequiresAuth(t *testing.T) {
	_, srv, _, _ := newDraftNoteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/draft-note",
		strings.NewReader(`{"verdict":"buy","listing_id":"item1"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
}

func TestDraftNoteRejectsInvalidVerdict(t *testing.T) {
	_, srv, _, token := newDraftNoteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/draft-note",
		strings.NewReader(`{"verdict":"watch","listing_id":"item1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid verdict, got %d", res.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "verdict") {
		t.Fatalf("expected error mentioning 'verdict', got %q", errMsg)
	}
}

func TestDraftNoteRejectsMissingListingID(t *testing.T) {
	_, srv, _, token := newDraftNoteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/draft-note",
		strings.NewReader(`{"verdict":"buy","listing_id":""}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing listing_id, got %d", res.Code)
	}
}

func TestDraftNoteReturns404ForUnknownListing(t *testing.T) {
	_, srv, _, token := newDraftNoteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/draft-note",
		strings.NewReader(`{"verdict":"buy","listing_id":"nonexistent_item"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown listing, got %d", res.Code)
	}
}

// TestDraftNoteVerdictShapeMatrix exercises all four valid verdict values and
// asserts the shape field routes correctly.
func TestDraftNoteVerdictShapeMatrix(t *testing.T) {
	st, srv, userID, token := newDraftNoteTestServer(t)

	listing := models.Listing{
		Title:       "de Sony A6400 body",
		Description: "Camera in goede staat.",
		Price:       55000,
		Condition:   "good",
		FairPrice:   45000,
		RiskFlags:   []string{},
		MarketplaceID: "marktplaats",
	}
	scored := models.ScoredListing{
		Score:             8.5,
		FairPrice:         45000,
		OfferPrice:        43000,
		Confidence:        0.8,
		RecommendedAction: "buy",
		RiskFlags:         []string{},
	}
	const itemID = "mp_sony_test_001"
	saveDraftNoteTestListing(t, st, userID, itemID, listing, scored)

	cases := []struct {
		verdict       string
		expectedShape string
	}{
		{"buy", "buy"},
		{"negotiate", "negotiate"},
		{"ask_seller", "ask_seller"},
		{"skip", "generic"},
	}

	for _, c := range cases {
		body := `{"verdict":"` + c.verdict + `","listing_id":"` + itemID + `"}`
		req := httptest.NewRequest(http.MethodPost, "/draft-note", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("verdict=%s: expected 200, got %d body=%s", c.verdict, res.Code, res.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
			t.Fatalf("verdict=%s: decode error = %v", c.verdict, err)
		}
		shape, _ := resp["shape"].(string)
		if shape != c.expectedShape {
			t.Errorf("verdict=%s: expected shape %q, got %q", c.verdict, c.expectedShape, shape)
		}
		text, _ := resp["text"].(string)
		if strings.TrimSpace(text) == "" {
			t.Errorf("verdict=%s: text must not be empty", c.verdict)
		}
		lang, _ := resp["lang"].(string)
		if lang != "nl" && lang != "en" {
			t.Errorf("verdict=%s: lang must be nl or en, got %q", c.verdict, lang)
		}
		questions, hasQuestions := resp["questions"]
		if !hasQuestions {
			t.Errorf("verdict=%s: response missing 'questions' field", c.verdict)
		}
		if hasQuestions {
			qSlice, ok := questions.([]any)
			if !ok {
				t.Errorf("verdict=%s: 'questions' must be a JSON array, got %T", c.verdict, questions)
			} else if c.verdict == "ask_seller" {
				if len(qSlice) == 0 {
					t.Errorf("verdict=ask_seller: expected at least 1 question (generic fallback), got empty")
				}
			} else {
				// buy, negotiate, skip: questions must be present but may be empty
				_ = qSlice
			}
		}
	}
}
