package plain_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
