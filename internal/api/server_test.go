package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TechXTT/marktbot/internal/auth"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/store"
)

type stubRunner struct {
	userID string
	calls  int
}

func (r *stubRunner) RunAllNow(context.Context) error { return nil }

func (r *stubRunner) RunUserNow(_ context.Context, userID string) error {
	r.calls++
	r.userID = userID
	return nil
}

func TestHandleRunAllSearchesRequiresAuthAndTriggersRunner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "api-server-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("runner@example.com", "hash", "Runner User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	runner := &stubRunner{}
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, runner, nil)

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/searches/run", nil)
	unauthorizedRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthorizedRes, unauthorizedReq)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", unauthorizedRes.Code)
	}

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "runner@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/searches/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected ok status, got %d", res.Code)
	}
	if runner.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", runner.calls)
	}
	if runner.userID != userID {
		t.Fatalf("expected runner user %q, got %q", userID, runner.userID)
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("expected ok response body, got %#v", body)
	}
}
