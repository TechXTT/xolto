package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

// patchJSON sends a PATCH request with a JSON body to the server handler.
func patchJSON(srv *Server, token, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// TestPatchOutreachStatusInvalidStatus verifies that PATCH /listings/:id/outreach-status
// returns 400 when the status value is not in the valid enum.
func TestPatchOutreachStatusInvalidStatus(t *testing.T) {
	st, srv, userID, token := newOutreachTestServer(t)
	defer st.Close()

	// Save a listing so the endpoint can find it.
	l := models.Listing{
		ItemID:        "listing-status-test",
		Title:         "Test listing",
		Price:         5000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}
	if err := st.SaveListing(userID, l, "query", models.ScoredListing{Score: 5.0}); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	res := patchJSON(srv, token, "/listings/listing-status-test/outreach-status", map[string]any{
		"status": "invalid_value",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d body=%s", res.Code, res.Body.String())
	}
}

// TestPatchOutreachStatusUnknownListing verifies that PATCH returns 404 when
// the listing does not exist for the authenticated user.
func TestPatchOutreachStatusUnknownListing(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := patchJSON(srv, token, "/listings/does-not-exist/outreach-status", map[string]any{
		"status": "sent",
	})
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown listing, got %d body=%s", res.Code, res.Body.String())
	}
}

// TestPatchOutreachStatusValidTransitions verifies that each valid status value
// is accepted and reflected in the response and the store.
func TestPatchOutreachStatusValidTransitions(t *testing.T) {
	validStatuses := []string{"none", "sent", "replied", "won", "lost"}

	for _, status := range validStatuses {
		t.Run(status, func(t *testing.T) {
			st, srv, userID, token := newOutreachTestServer(t)
			defer st.Close()

			itemID := "listing-" + status
			l := models.Listing{
				ItemID:        itemID,
				Title:         "Listing " + status,
				Price:         10000,
				PriceType:     "fixed",
				MarketplaceID: "olxbg",
			}
			if err := st.SaveListing(userID, l, "q", models.ScoredListing{}); err != nil {
				t.Fatalf("SaveListing() error = %v", err)
			}

			res := patchJSON(srv, token, "/listings/"+itemID+"/outreach-status", map[string]any{
				"status": status,
			})
			if res.Code != http.StatusOK {
				t.Fatalf("expected 200 for status=%q, got %d body=%s", status, res.Code, res.Body.String())
			}

			var body map[string]any
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got, _ := body["outreach_status"].(string); got != status {
				t.Errorf("expected outreach_status=%q in response, got %q", status, got)
			}

			// Verify the field is persisted and emitted via GetListing.
			stored, err := st.GetListing(userID, itemID)
			if err != nil {
				t.Fatalf("GetListing() error = %v", err)
			}
			if stored == nil {
				t.Fatalf("GetListing() returned nil")
			}
			if stored.OutreachStatus != status {
				t.Errorf("expected stored OutreachStatus=%q, got %q", status, stored.OutreachStatus)
			}
		})
	}
}

// TestPatchOutreachStatusAppearsInMatchesResponse verifies that outreach_status
// is included in the /matches response envelope for a listing that has a
// non-default status.
func TestPatchOutreachStatusAppearsInMatchesResponse(t *testing.T) {
	st, srv, userID, token := newMatchesTestServer(t)
	defer st.Close()

	l := models.Listing{
		ItemID:        "matches-status-listing",
		Title:         "Matches status test",
		Price:         8000,
		PriceType:     "fixed",
		MarketplaceID: "olxbg",
	}
	if err := st.SaveListing(userID, l, "q", models.ScoredListing{Score: 6.0}); err != nil {
		t.Fatalf("SaveListing() error = %v", err)
	}

	// Update status to "sent" via the PATCH endpoint.
	patchRes := patchJSON(srv, token, "/listings/matches-status-listing/outreach-status", map[string]any{
		"status": "sent",
	})
	if patchRes.Code != http.StatusOK {
		t.Fatalf("PATCH expected 200, got %d body=%s", patchRes.Code, patchRes.Body.String())
	}

	// Fetch /matches and confirm outreach_status is "sent".
	matchesRes := doMatchesRequest(t, srv, token, "")
	if matchesRes.Code != http.StatusOK {
		t.Fatalf("/matches expected 200, got %d", matchesRes.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(matchesRes.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	matchesRaw, _ := body["matches"].([]any)
	if len(matchesRaw) == 0 {
		t.Fatal("expected at least one item in matches[]")
	}

	var found map[string]any
	for _, raw := range matchesRaw {
		m, _ := raw.(map[string]any)
		if m["ItemID"] == "matches-status-listing" {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatal("matches-status-listing not found in /matches response")
	}
	if got, _ := found["OutreachStatus"].(string); got != "sent" {
		t.Errorf("expected OutreachStatus=sent in /matches, got %q", got)
	}
}
