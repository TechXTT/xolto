package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/store"
)

// flushableRecorder embeds httptest.ResponseRecorder and explicitly implements
// http.Flusher so we can verify delegation through statusCapturingResponseWriter.
type flushableRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flushableRecorder) Flush() {
	r.flushed = true
	r.ResponseRecorder.Flush()
}

// Compile-time assertion: *statusCapturingResponseWriter must satisfy http.Flusher.
var _ http.Flusher = (*statusCapturingResponseWriter)(nil)

func TestStatusCapturingResponseWriterImplementsFlusher(t *testing.T) {
	inner := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &statusCapturingResponseWriter{ResponseWriter: inner}

	flusher, ok := any(w).(http.Flusher)
	if !ok {
		t.Fatal("statusCapturingResponseWriter does not implement http.Flusher")
	}
	flusher.Flush()
	if !inner.flushed {
		t.Fatal("Flush() did not delegate to the underlying ResponseWriter")
	}
}

func TestStatusCapturingResponseWriterFlushIsNoopWhenInnerNotFlusher(t *testing.T) {
	// Use a bare http.ResponseWriter that does NOT implement http.Flusher.
	// Calling Flush() must not panic.
	var inner http.ResponseWriter = httptest.NewRecorder()
	w := &statusCapturingResponseWriter{ResponseWriter: inner}
	w.Flush() // must not panic
}

func TestStatusCapturingResponseWriterUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	w := &statusCapturingResponseWriter{ResponseWriter: inner}
	if w.Unwrap() != inner {
		t.Fatal("Unwrap() did not return the underlying ResponseWriter")
	}
}

// TestRequestLoggingMiddlewarePreservesFlusher exercises the full middleware chain
// (matching server.go Handler()) against a handler that asserts http.Flusher is
// available. Before the fix this would observe ok=false and write a 500.
func TestRequestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	srv := &Server{}

	var flusherOK bool
	sseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, flusherOK = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with only requestLoggingMiddleware — the layer that introduces
	// statusCapturingResponseWriter and was hiding the Flusher interface.
	handler := srv.requestLoggingMiddleware(sseHandler)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if !flusherOK {
		t.Fatal("http.Flusher not available inside handler wrapped by requestLoggingMiddleware; SSE would return 500")
	}
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
}

// ---------------------------------------------------------------------------
// Support handler tests (XOL-53 SUP-2)
// ---------------------------------------------------------------------------

// webhookSignature computes the Plain-Signature header value for a given secret
// and body using the HMAC-SHA256 scheme.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newSupportTestServer returns a Server wired to a fresh SQLite store, ready
// for support handler tests.
func newSupportTestServer(t *testing.T, webhookSecret string) (*store.SQLiteStore, *Server, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "support-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	userID, err := st.CreateUser("support-user@example.com", "hash", "Support User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	srv := NewServer(config.ServerConfig{
		JWTSecret:          "test-secret",
		AppBaseURL:         "http://localhost:3000",
		PlainWebhookSecret: webhookSecret,
	}, st, nil, nil, nil, nil)
	return st, srv, userID
}

// plainTestServer returns an httptest.Server that responds to all requests
// with the given JSON body.
func plainTestServer(t *testing.T, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSupportWebhookWrongSignatureReturns401 (AC-3 negative path)
func TestSupportWebhookWrongSignatureReturns401(t *testing.T) {
	_, srv, _ := newSupportTestServer(t, "correct-secret")

	payload := `{"type":"thread.created","payload":{"thread":{"id":"th_abc"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/support/webhook", strings.NewReader(payload))
	req.Header.Set("Plain-Signature", "sha256=badhex0000")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong signature, got %d", res.Code)
	}
}

// TestSupportWebhookCorrectSignatureReturns200 (AC-3 positive path)
func TestSupportWebhookCorrectSignatureReturns200(t *testing.T) {
	_, srv, _ := newSupportTestServer(t, "test-secret")

	payload := []byte(`{"type":"thread.created","payload":{"thread":{"id":"th_xyz"}}}`)
	sig := webhookSignature("test-secret", payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/support/webhook", bytes.NewReader(payload))
	req.Header.Set("Plain-Signature", sig)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 on correct signature, got %d; body: %s", res.Code, res.Body.String())
	}
}

// TestSupportWebhookMissingSignatureReturns401
func TestSupportWebhookMissingSignatureReturns401(t *testing.T) {
	_, srv, _ := newSupportTestServer(t, "test-secret")

	payload := []byte(`{"type":"thread.created","payload":{"thread":{"id":"th_nosig"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/support/webhook", bytes.NewReader(payload))
	// No Plain-Signature header.
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when signature header is absent, got %d", res.Code)
	}
}

// TestSupportReportMissingSubjectReturns400 (AC-4)
func TestSupportReportMissingSubjectReturns400(t *testing.T) {
	_, srv, userID := newSupportTestServer(t, "secret")

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "support-user@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	body := `{"subject":"","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/support/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when subject is empty, got %d", res.Code)
	}
}

// TestSupportReportMissingMessageReturns400 (AC-4)
func TestSupportReportMissingMessageReturns400(t *testing.T) {
	_, srv, userID := newSupportTestServer(t, "secret")

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "support-user@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	body := `{"subject":"Help","message":""}`
	req := httptest.NewRequest(http.MethodPost, "/v1/support/report", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when message is empty, got %d", res.Code)
	}
}

// TestSupportReportEndToEnd exercises the /v1/support/report handler end-to-end
// with a mock Plain HTTP server. It verifies that the handler:
//   - calls Plain to create a thread
//   - persists a support_events row
//   - returns 200 with ok=true and a plain_thread_id
func TestSupportReportEndToEnd(t *testing.T) {
	// Stand up a mock Plain server that responds to all mutations.
	callCount := 0
	plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			// UpsertCustomer
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"upsertCustomer": map[string]any{
						"result":   "CREATED",
						"customer": map[string]any{"id": "c_test001"},
					},
				},
			})
		case 2:
			// CreateThread
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"createThread": map[string]any{
						"thread": map[string]any{"id": "th_e2e001"},
					},
				},
			})
		default:
			// Unexpected call
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer plainSrv.Close()

	st, srv, userID := newSupportTestServer(t, "secret")
	defer st.Close()

	// Wire the mock Plain server via client transport override.
	plainClient := plain.New("test-api-key")
	plainClient.Endpoint = plainSrv.URL
	plainClient.HTTPClient = plainSrv.Client()
	srv.plainClient = plainClient

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "support-user@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	reqBody := `{"subject":"Pricing wrong","message":"The price shown is incorrect","dash_context":{"mission_id":42}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/support/report", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", res.Code, res.Body.String())
	}

	var respBody map[string]any
	if err := json.NewDecoder(res.Body).Decode(&respBody); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if respBody["ok"] != true {
		t.Fatalf("expected ok=true, got %v", respBody["ok"])
	}
	if respBody["plain_thread_id"] != "th_e2e001" {
		t.Fatalf("expected plain_thread_id=th_e2e001, got %v", respBody["plain_thread_id"])
	}

	// Verify the row was persisted.
	event, err := st.GetByPlainThreadID(req.Context(), "th_e2e001")
	if err != nil {
		t.Fatalf("GetByPlainThreadID() error = %v", err)
	}
	if event == nil {
		t.Fatal("expected support event to be persisted in DB")
	}
	if event.IntakeSource != "dash_contact" {
		t.Errorf("expected intake_source=dash_contact, got %q", event.IntakeSource)
	}
}
