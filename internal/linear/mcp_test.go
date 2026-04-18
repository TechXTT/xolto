package linear_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/linear"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newLinearServer returns a test server that responds with the given GraphQL
// data payload for every request.
func newLinearServer(t *testing.T, statusCode int, data any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		payload := map[string]any{"data": data}
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

// newLinearErrorServer responds with a top-level GraphQL errors array.
func newLinearErrorServer(t *testing.T, msg string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		payload := map[string]any{
			"errors": []map[string]any{{"message": msg}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

// ---------------------------------------------------------------------------
// CreateIssue — happy path
// ---------------------------------------------------------------------------

func TestLinearMCPClient_CreateIssue_WithTeamID(t *testing.T) {
	// Server responds to both the teams query (for team resolution) and the
	// createIssue mutation. We use a sequence counter to distinguish.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		callCount++

		// When TeamID is provided there is only one call (the mutation).
		payload := map[string]any{
			"data": map[string]any{
				"issueCreate": map[string]any{
					"success": true,
					"issue": map[string]any{
						"id":         "uuid-123",
						"identifier": "XOL-55",
						"url":        "https://linear.app/xolto/issue/XOL-55",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := linear.NewLinearMCPClient("lin_key")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	result, err := c.CreateIssue(context.Background(), linear.CreateIssueInput{
		Title:       "Pricing wrong on OLX BG",
		Description: "Thread: https://app.plain.com/threads/th_abc\nCategory: pricing",
		TeamID:      "team-uuid-001",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if result.Identifier != "XOL-55" {
		t.Errorf("expected Identifier=XOL-55, got %q", result.Identifier)
	}
	if result.URL == "" {
		t.Error("expected non-empty URL")
	}
}

func TestLinearMCPClient_CreateIssue_ResolvesTeamByName(t *testing.T) {
	// First request = teams query; second = mutation.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		callCount++

		var payload map[string]any
		if callCount == 1 {
			// Teams query response.
			payload = map[string]any{
				"data": map[string]any{
					"teams": map[string]any{
						"nodes": []map[string]any{
							{"id": "team-uuid-001", "name": "Xolto"},
						},
					},
				},
			}
		} else {
			// issueCreate mutation.
			payload = map[string]any{
				"data": map[string]any{
					"issueCreate": map[string]any{
						"success": true,
						"issue": map[string]any{
							"id":         "uuid-456",
							"identifier": "XOL-56",
							"url":        "https://linear.app/xolto/issue/XOL-56",
						},
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	c := linear.NewLinearMCPClient("lin_key")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	result, err := c.CreateIssue(context.Background(), linear.CreateIssueInput{
		Title:    "Support event routed",
		TeamName: "Xolto",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if result.Identifier != "XOL-56" {
		t.Errorf("expected Identifier=XOL-56, got %q", result.Identifier)
	}
}

// ---------------------------------------------------------------------------
// Error paths
// ---------------------------------------------------------------------------

func TestLinearMCPClient_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	c := linear.NewLinearMCPClient("lin_key")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	_, err := c.CreateIssue(context.Background(), linear.CreateIssueInput{
		Title:  "test",
		TeamID: "team-1",
	})
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if !errors.Is(err, linear.ErrLinearMCPRateLimited) {
		t.Errorf("expected ErrLinearMCPRateLimited, got %v", err)
	}
}

func TestLinearMCPClient_GraphQLErrors(t *testing.T) {
	srv := newLinearErrorServer(t, "authentication failed")
	defer srv.Close()

	c := linear.NewLinearMCPClient("bad-key")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	_, err := c.CreateIssue(context.Background(), linear.CreateIssueInput{
		Title:  "test",
		TeamID: "team-1",
	})
	if err == nil {
		t.Fatal("expected error on GraphQL error response, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected error to mention 'authentication failed', got %q", err.Error())
	}
}

func TestLinearMCPClient_CreateIssue_SuccessFalse(t *testing.T) {
	srv := newLinearServer(t, http.StatusOK, map[string]any{
		"issueCreate": map[string]any{
			"success": false,
			"issue":   nil,
		},
	})
	defer srv.Close()

	c := linear.NewLinearMCPClient("lin_key")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	_, err := c.CreateIssue(context.Background(), linear.CreateIssueInput{
		Title:  "test",
		TeamID: "team-1",
	})
	if err == nil {
		t.Fatal("expected error when issueCreate success=false, got nil")
	}
	if !errors.Is(err, linear.ErrLinearAPIError) {
		t.Errorf("expected ErrLinearAPIError, got %v", err)
	}
}
