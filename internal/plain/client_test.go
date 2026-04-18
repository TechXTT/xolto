package plain_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/plain"
)

// newTestServer returns an httptest.Server that responds with the provided JSON
// body and status code for every request. The caller must call srv.Close().
func newTestServer(t *testing.T, statusCode int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestUpsertCustomer(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"upsertCustomer": map[string]any{
				"result": "CREATED",
				"customer": map[string]any{
					"id": "c_01234567",
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	result, err := client.UpsertCustomer(context.Background(), plain.UpsertCustomerInput{
		Email:    "user@example.com",
		FullName: "Test User",
	})
	if err != nil {
		t.Fatalf("UpsertCustomer() error = %v", err)
	}
	if result.CustomerID != "c_01234567" {
		t.Errorf("expected customer id c_01234567, got %q", result.CustomerID)
	}
}

// TestUpsertCustomerRequestShape asserts the exact GraphQL variables sent to
// Plain. It guards against schema drift that caused the SUP-7 500 incident
// (fullName wrapped in {value:...} and the non-existent verifiedAt field).
func TestUpsertCustomerRequestShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"upsertCustomer": map[string]any{
					"customer": map[string]any{"id": "c_1"},
				},
			},
		})
	}))
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	_, err := client.UpsertCustomer(context.Background(), plain.UpsertCustomerInput{
		Email:    "user@example.com",
		FullName: "Marto Testov",
	})
	if err != nil {
		t.Fatalf("UpsertCustomer() error = %v", err)
	}

	vars, ok := captured["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables missing in request body: %#v", captured)
	}
	input, ok := vars["input"].(map[string]any)
	if !ok {
		t.Fatalf("input missing: %#v", vars)
	}
	onCreate, ok := input["onCreate"].(map[string]any)
	if !ok {
		t.Fatalf("onCreate missing: %#v", input)
	}
	if got := onCreate["fullName"]; got != "Marto Testov" {
		t.Errorf("fullName = %#v; want plain string \"Marto Testov\"", got)
	}
	email, ok := onCreate["email"].(map[string]any)
	if !ok {
		t.Fatalf("email missing: %#v", onCreate)
	}
	if _, present := email["verifiedAt"]; present {
		t.Errorf("email.verifiedAt must not be sent — field does not exist on EmailAddressInput")
	}
	if email["email"] != "user@example.com" {
		t.Errorf("email.email = %#v; want user@example.com", email["email"])
	}
	if email["isVerified"] != true {
		t.Errorf("email.isVerified = %#v; want true", email["isVerified"])
	}
}

// TestUpsertCustomerOmitsEmptyFullName ensures we don't send an empty string for
// fullName when the user has no name — Plain may treat "" as invalid input.
func TestUpsertCustomerOmitsEmptyFullName(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"upsertCustomer": map[string]any{
					"customer": map[string]any{"id": "c_1"},
				},
			},
		})
	}))
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	_, err := client.UpsertCustomer(context.Background(), plain.UpsertCustomerInput{Email: "anon@example.com"})
	if err != nil {
		t.Fatalf("UpsertCustomer() error = %v", err)
	}

	onCreate := captured["variables"].(map[string]any)["input"].(map[string]any)["onCreate"].(map[string]any)
	if _, present := onCreate["fullName"]; present {
		t.Errorf("fullName should be omitted when empty; got %#v", onCreate["fullName"])
	}
}

func TestCreateThread(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"createThread": map[string]any{
				"thread": map[string]any{
					"id": "th_01234567",
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	result, err := client.CreateThread(context.Background(), plain.CreateThreadInput{
		CustomerID: "c_01234567",
		Subject:    "Test thread",
		Body:       "Hello from dash",
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if result.ThreadID != "th_01234567" {
		t.Errorf("expected thread id th_01234567, got %q", result.ThreadID)
	}
}

func TestAddLabel(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"addLabels": map[string]any{
				"labels": []map[string]any{{"id": "l_01"}},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	if err := client.AddLabel(context.Background(), "th_01234567", "lt_abc"); err != nil {
		t.Fatalf("AddLabel() error = %v", err)
	}
}

func TestAddNote(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"createNote": map[string]any{
				"note": map[string]any{"id": "n_01"},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	if err := client.AddNote(context.Background(), "th_01234567", "classifier draft reply"); err != nil {
		t.Fatalf("AddNote() error = %v", err)
	}
}

func TestSetPriority(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"updateThread": map[string]any{
				"thread": map[string]any{"id": "th_01234567"},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	if err := client.SetPriority(context.Background(), "th_01234567", plain.PriorityHigh); err != nil {
		t.Fatalf("SetPriority() error = %v", err)
	}
}

func TestGraphQLErrorPropagated(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"errors": []map[string]any{
			{"message": "authentication failed"},
		},
	})
	defer srv.Close()

	client := plain.New("bad-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	_, err := client.UpsertCustomer(context.Background(), plain.UpsertCustomerInput{Email: "x@x.com"})
	if err == nil {
		t.Fatal("expected error when graphql errors are present, got nil")
	}
}

func TestNon200StatusReturnsError(t *testing.T) {
	srv := newTestServer(t, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	defer srv.Close()

	client := plain.New("bad-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	_, err := client.UpsertCustomer(context.Background(), plain.UpsertCustomerInput{Email: "x@x.com"})
	if err == nil {
		t.Fatal("expected error on non-200 status, got nil")
	}
}

// ---------------------------------------------------------------------------
// GetThread tests (SUP-10)
// ---------------------------------------------------------------------------

func TestClient_GetThread_OK(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"thread": map[string]any{
				"id":    "th_01ABCDEF",
				"title": "Missing item in order",
				"customer": map[string]any{
					"fullName": "Ana Kostadinova",
					"email": map[string]any{
						"email": "ana@example.com",
					},
				},
				"timelineEntries": map[string]any{"edges": []any{}},
			},
		},
	})
	defer srv.Close()

	client := plain.New("test-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	info, err := client.GetThread(context.Background(), "th_01ABCDEF")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if info.ThreadID != "th_01ABCDEF" {
		t.Errorf("expected ThreadID=th_01ABCDEF, got %q", info.ThreadID)
	}
	if info.Subject != "Missing item in order" {
		t.Errorf("expected Subject=%q, got %q", "Missing item in order", info.Subject)
	}
	if info.CustomerEmail != "ana@example.com" {
		t.Errorf("expected CustomerEmail=ana@example.com, got %q", info.CustomerEmail)
	}
	if info.CustomerName != "Ana Kostadinova" {
		t.Errorf("expected CustomerName=Ana Kostadinova, got %q", info.CustomerName)
	}
	if info.Body != "" {
		t.Errorf("expected empty Body on empty timeline, got %q", info.Body)
	}
}

func TestClient_GetThread_WithBody(t *testing.T) {
	chatText := "битата батерия не държи заряд"
	emailText := "Hi, my battery drains in 1 hour"
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"thread": map[string]any{
				"id":    "th_bg",
				"title": "Battery issue",
				"customer": map[string]any{
					"fullName": "Ivan Petrov",
					"email":    map[string]any{"email": "ivan@example.com"},
				},
				"timelineEntries": map[string]any{
					"edges": []any{
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename": "ChatEntry",
							"chatText":   chatText,
						}}},
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename":  "EmailEntry",
							"textContent": emailText,
						}}},
					},
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("k")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	info, err := client.GetThread(context.Background(), "th_bg")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	want := chatText + "\n\n" + emailText
	if info.Body != want {
		t.Errorf("body mismatch\n  want=%q\n  got =%q", want, info.Body)
	}
}

func TestClient_GetThread_EmailFullTextContent(t *testing.T) {
	short := "short preview"
	full := "the complete email body content that exceeds the preview"
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"thread": map[string]any{
				"id":       "th_email",
				"title":    "Long email",
				"customer": map[string]any{"fullName": "x", "email": map[string]any{"email": "x@x"}},
				"timelineEntries": map[string]any{
					"edges": []any{
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename":         "EmailEntry",
							"textContent":        short,
							"fullTextContent":    full,
							"hasMoreTextContent": true,
						}}},
					},
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("k")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	info, err := client.GetThread(context.Background(), "th_email")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if info.Body != full {
		t.Errorf("expected fullTextContent when hasMoreTextContent=true, got %q", info.Body)
	}
}

func TestClient_GetThread_BodyTruncated(t *testing.T) {
	// 10 KB of text — exceeds the 8 KB cap.
	big := strings.Repeat("a", 10*1024)
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"thread": map[string]any{
				"id":       "th_big",
				"title":    "Large thread",
				"customer": map[string]any{"fullName": "", "email": map[string]any{"email": "x@x"}},
				"timelineEntries": map[string]any{
					"edges": []any{
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename": "ChatEntry",
							"chatText":   big,
						}}},
					},
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("k")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	info, err := client.GetThread(context.Background(), "th_big")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if len(info.Body) > 8*1024 {
		t.Errorf("body exceeds 8 KB cap: len=%d", len(info.Body))
	}
	if !strings.HasSuffix(info.Body, "...[truncated]") {
		t.Errorf("expected truncation marker suffix, got last 20 bytes=%q", info.Body[max(0, len(info.Body)-20):])
	}
}

func TestClient_GetThread_SkipNonCustomerEntries(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, map[string]any{
		"data": map[string]any{
			"thread": map[string]any{
				"id":       "th_mix",
				"title":    "Mixed",
				"customer": map[string]any{"fullName": "", "email": map[string]any{"email": "x@x"}},
				"timelineEntries": map[string]any{
					"edges": []any{
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename": "NoteEntry",
							"text":       "internal note — should not appear",
						}}},
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename": "ThreadStatusTransitionedEntry",
						}}},
						map[string]any{"node": map[string]any{"entry": map[string]any{
							"__typename": "ChatEntry",
							"chatText":   "actual customer message",
						}}},
					},
				},
			},
		},
	})
	defer srv.Close()

	client := plain.New("k")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	info, err := client.GetThread(context.Background(), "th_mix")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if info.Body != "actual customer message" {
		t.Errorf("expected only ChatEntry text, got %q", info.Body)
	}
}

func TestClient_GetThread_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"authentication failed"}]}`))
	}))
	defer srv.Close()

	client := plain.New("bad-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	_, err := client.GetThread(context.Background(), "th_check")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	// Error must mention the HTTP status.
	errStr := err.Error()
	if !containsAny(errStr, "401", "status") {
		t.Errorf("expected error to mention 401 status, got %q", errStr)
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 {
			idx := 0
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					idx = i
					_ = idx
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Preflight tests (SUP-10)
// ---------------------------------------------------------------------------

func TestClient_Preflight_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
	}))
	defer srv.Close()

	client := plain.New("valid-api-key")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	pf := client.Preflight(context.Background())
	if !pf.Configured {
		t.Error("expected Configured=true for non-empty key")
	}
	if pf.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", pf.StatusCode)
	}
	if pf.Err != nil {
		t.Errorf("expected no error on 2xx, got %v", pf.Err)
	}
	if pf.KeyLen == 0 {
		t.Error("expected KeyLen > 0")
	}
}

func TestClient_Preflight_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key"}`))
	}))
	defer srv.Close()

	client := plain.New("clearly-not-a-real-token")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	pf := client.Preflight(context.Background())
	if !pf.Configured {
		t.Error("expected Configured=true for non-empty key")
	}
	if pf.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusCode=401, got %d", pf.StatusCode)
	}
	if pf.BodySnippet == "" {
		t.Error("expected BodySnippet non-empty for 401 response")
	}
	if pf.Err == nil {
		t.Error("expected non-nil Err on 401")
	}
}

func TestClient_Preflight_EmptyKey(t *testing.T) {
	// No HTTP call should be made when the key is empty.
	// We confirm by pointing at a server that would panic if called.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("HTTP call must not be made when key is empty")
	}))
	defer srv.Close()

	client := plain.New("")
	client.Endpoint = srv.URL
	client.HTTPClient = srv.Client()

	pf := client.Preflight(context.Background())
	if pf.Configured {
		t.Error("expected Configured=false for empty key")
	}
	if pf.StatusCode != 0 {
		t.Errorf("expected StatusCode=0 for empty-key preflight, got %d", pf.StatusCode)
	}
	if pf.Err != nil {
		t.Errorf("expected no error for empty-key preflight, got %v", pf.Err)
	}
}
