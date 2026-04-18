package reasoner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stubServer returns an httptest.Server that returns the given HTTP status and body.
func stubServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

// openAIResponse wraps verdicts into a minimal OpenAI-compatible
// chat completion response body.
func openAIResponse(verdicts []map[string]string) string {
	type verdictsWrapper struct {
		Verdicts []map[string]string `json:"verdicts"`
	}
	payload := verdictsWrapper{Verdicts: verdicts}
	content, _ := json.Marshal(payload)

	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": string(content), "role": "assistant"}},
		},
		"usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 20},
	}
	raw, _ := json.Marshal(resp)
	return string(raw)
}

// newTestEvaluator builds a MustHaveEvaluatorLLM that talks to srv.
func newTestEvaluator(t *testing.T, srv *httptest.Server, store MustHaveStore) *MustHaveEvaluatorLLM {
	t.Helper()
	e := NewMustHaveEvaluatorLLM(MustHaveEvaluatorConfig{
		APIKey:                    "test-key",
		BaseURL:                   srv.URL,
		Model:                     "gpt-5-nano",
		MaxCallsPerMissionPerHour: 100,
		HTTPClient:                srv.Client(),
		Store:                     store,
	})
	if e == nil {
		t.Fatal("NewMustHaveEvaluatorLLM returned nil unexpectedly")
	}
	return e
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestMustHaveEvaluatorHappyPath verifies that a well-formed LLM response
// produces the expected verdict map.
func TestMustHaveEvaluatorHappyPath(t *testing.T) {
	srv := stubServer(t, 200, openAIResponse([]map[string]string{
		{"text": "battery >=90%", "status": "missed"},
		{"text": "original charger", "status": "unknown"},
	}))
	defer srv.Close()

	e := newTestEvaluator(t, srv, nil)
	ctx := context.Background()
	verdicts, err := e.Evaluate(ctx, "listing-1", 10, "user-1", "iPhone 12", "Battery health 68%.", []string{"battery >=90%", "original charger"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdicts["battery >=90%"] != "missed" {
		t.Errorf("battery >=90%%: expected missed, got %q", verdicts["battery >=90%"])
	}
	if verdicts["original charger"] != "unknown" {
		t.Errorf("original charger: expected unknown, got %q", verdicts["original charger"])
	}
}

// TestMustHaveEvaluatorMalformedJSON verifies that a malformed LLM response
// returns an error and does not fabricate verdicts.
func TestMustHaveEvaluatorMalformedJSON(t *testing.T) {
	malformedBody := `{"choices":[{"message":{"content":"not json at all","role":"assistant"}}]}`
	srv := stubServer(t, 200, malformedBody)
	defer srv.Close()

	e := newTestEvaluator(t, srv, nil)
	_, err := e.Evaluate(context.Background(), "listing-2", 11, "user-1", "Title", "Desc", []string{"battery >=90%"})
	if err == nil {
		t.Fatal("expected error on malformed LLM JSON, got nil")
	}
}

// TestMustHaveEvaluatorHTTP429 verifies that an HTTP 429 response returns an
// error (no verdicts fabricated).
func TestMustHaveEvaluatorHTTP429(t *testing.T) {
	srv := stubServer(t, 429, `{"error":"rate_limit_exceeded"}`)
	defer srv.Close()

	e := newTestEvaluator(t, srv, nil)
	_, err := e.Evaluate(context.Background(), "listing-3", 12, "user-1", "Title", "Desc", []string{"battery >=90%"})
	if err == nil {
		t.Fatal("expected error on HTTP 429, got nil")
	}
}

// TestMustHaveEvaluatorHTTP500 verifies that an HTTP 5xx response returns an
// error.
func TestMustHaveEvaluatorHTTP500(t *testing.T) {
	srv := stubServer(t, 500, `{"error":"internal_server_error"}`)
	defer srv.Close()

	e := newTestEvaluator(t, srv, nil)
	_, err := e.Evaluate(context.Background(), "listing-4", 13, "user-1", "Title", "Desc", []string{"NL seller"})
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
}

// TestMustHaveEvaluatorBulgarianCyrillic verifies that Bulgarian Cyrillic text
// in the listing is forwarded to the LLM request body intact (UTF-8 preserved).
func TestMustHaveEvaluatorBulgarianCyrillic(t *testing.T) {
	bgTitle := "iPhone 14 Pro, отлично състояние"
	bgDescription := "Батерия 62%, продавач от Пловдив. Без зарядно."
	bgMustHave := "батерия ≥80%"

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, openAIResponse([]map[string]string{
			{"text": bgMustHave, "status": "missed"},
		}))
	}))
	defer srv.Close()

	e := newTestEvaluator(t, srv, nil)
	verdicts, err := e.Evaluate(context.Background(), "bg-listing-1", 20, "user-bg", bgTitle, bgDescription, []string{bgMustHave})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdicts[bgMustHave] != "missed" {
		t.Errorf("bg must-have: expected missed, got %q", verdicts[bgMustHave])
	}

	// Assert the request body contains the BG Cyrillic strings intact.
	bodyStr := string(capturedBody)
	if !strings.Contains(bodyStr, bgTitle) {
		t.Errorf("request body does not contain BG Cyrillic title %q", bgTitle)
	}
	if !strings.Contains(bodyStr, bgMustHave) {
		t.Errorf("request body does not contain BG Cyrillic must-have %q", bgMustHave)
	}
}

// TestMustHaveEvaluatorRateLimitExhausted verifies that once the per-mission
// hourly ceiling is exceeded, Evaluate returns ErrMustHaveRateLimited.
func TestMustHaveEvaluatorRateLimitExhausted(t *testing.T) {
	srv := stubServer(t, 200, openAIResponse([]map[string]string{
		{"text": "battery >=80%", "status": "unknown"},
	}))
	defer srv.Close()

	e := NewMustHaveEvaluatorLLM(MustHaveEvaluatorConfig{
		APIKey:                    "test-key",
		BaseURL:                   srv.URL,
		Model:                     "gpt-5-nano",
		MaxCallsPerMissionPerHour: 2, // very low ceiling for testing
		HTTPClient:                srv.Client(),
	})
	if e == nil {
		t.Fatal("NewMustHaveEvaluatorLLM returned nil")
	}

	ctx := context.Background()
	missionID := int64(99)
	mustHaves := []string{"battery >=80%"}

	// First two calls should succeed.
	for i := 0; i < 2; i++ {
		if _, err := e.Evaluate(ctx, "listing-rl", missionID, "user-1", "Title", "Desc", mustHaves); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}

	// Third call should be rate-limited.
	_, err := e.Evaluate(ctx, "listing-rl", missionID, "user-1", "Title", "Desc", mustHaves)
	if !errors.Is(err, ErrMustHaveRateLimited) {
		t.Errorf("expected ErrMustHaveRateLimited after ceiling exceeded, got %v", err)
	}
}

// TestMustHaveEvaluatorCacheHit verifies that a cached result is returned
// without an LLM call on a repeated request.
func TestMustHaveEvaluatorCacheHit(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, openAIResponse([]map[string]string{
			{"text": "battery >=90%", "status": "missed"},
		}))
	}))
	defer srv.Close()

	store := &inMemoryMustHaveStore{}
	e := newTestEvaluator(t, srv, store)

	ctx := context.Background()
	listingID := "cache-listing-1"
	missionID := int64(33)
	mustHaves := []string{"battery >=90%"}

	// First call — should hit LLM.
	v1, err := e.Evaluate(ctx, listingID, missionID, "user-1", "Title", "Desc", mustHaves)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if v1["battery >=90%"] != "missed" {
		t.Errorf("first call: expected missed, got %q", v1["battery >=90%"])
	}
	if callCount != 1 {
		t.Errorf("expected 1 LLM call after first Evaluate, got %d", callCount)
	}

	// Second call — should hit cache, no LLM call.
	v2, err := e.Evaluate(ctx, listingID, missionID, "user-1", "Title", "Desc", mustHaves)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if v2["battery >=90%"] != "missed" {
		t.Errorf("second call: expected missed from cache, got %q", v2["battery >=90%"])
	}
	if callCount != 1 {
		t.Errorf("expected still 1 LLM call after cache hit, got %d", callCount)
	}
}

// TestMustHaveEvaluatorEmptyAPIKey verifies that NewMustHaveEvaluatorLLM
// returns nil when APIKey is empty (disabled degrade path).
func TestMustHaveEvaluatorEmptyAPIKey(t *testing.T) {
	e := NewMustHaveEvaluatorLLM(MustHaveEvaluatorConfig{
		APIKey: "",
		Model:  "gpt-5-nano",
	})
	if e != nil {
		t.Errorf("expected nil evaluator when API key is empty, got non-nil")
	}
}

// TestMustHaveEvaluatorRateLimitReset verifies that the sliding window resets
// after an hour.
func TestMustHaveEvaluatorRateLimitReset(t *testing.T) {
	srv := stubServer(t, 200, openAIResponse([]map[string]string{
		{"text": "battery >=80%", "status": "unknown"},
	}))
	defer srv.Close()

	now := time.Now()
	e := NewMustHaveEvaluatorLLM(MustHaveEvaluatorConfig{
		APIKey:                    "test-key",
		BaseURL:                   srv.URL,
		Model:                     "gpt-5-nano",
		MaxCallsPerMissionPerHour: 1,
		HTTPClient:                srv.Client(),
	})
	if e == nil {
		t.Fatal("nil evaluator")
	}
	// Override now function so we can simulate time passing.
	e.nowFn = func() time.Time { return now }

	ctx := context.Background()
	missionID := int64(77)
	mustHaves := []string{"battery >=80%"}

	// First call succeeds.
	if _, err := e.Evaluate(ctx, "rl-reset", missionID, "u1", "T", "D", mustHaves); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call at same time — rate limited.
	if _, err := e.Evaluate(ctx, "rl-reset", missionID, "u1", "T", "D", mustHaves); !errors.Is(err, ErrMustHaveRateLimited) {
		t.Errorf("expected rate limit, got %v", err)
	}

	// Advance clock by 61 minutes — window should reset.
	e.nowFn = func() time.Time { return now.Add(61 * time.Minute) }
	if _, err := e.Evaluate(ctx, "rl-reset", missionID, "u1", "T", "D", mustHaves); err != nil {
		t.Fatalf("after reset: expected success, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// inMemoryMustHaveStore — test-only MustHaveStore implementation
// ---------------------------------------------------------------------------

type inMemoryMustHaveStore struct {
	mu    sync.Mutex
	cache map[string]string
}

func (s *inMemoryMustHaveStore) init() {
	if s.cache == nil {
		s.cache = make(map[string]string)
	}
}

func (s *inMemoryMustHaveStore) SetAIScoreCache(cacheKey string, _ float64, reasoning string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.cache[cacheKey] = reasoning
	return nil
}

func (s *inMemoryMustHaveStore) GetAIScoreCache(cacheKey string, _ int) (float64, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	v, ok := s.cache[cacheKey]
	return 0, v, ok, nil
}
