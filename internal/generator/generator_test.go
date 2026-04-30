package generator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TechXTT/xolto/internal/config"
)

// validGeneratorResponse is a minimal AI response that satisfies the
// strict json_schema parser path used by generateWithAI.
const validGeneratorResponse = `{"choices":[{"message":{"role":"assistant","content":"{\"searches\":[{\"name\":\"Sony A7 III\",\"query\":\"sony a7 iii\",\"category_id\":487,\"max_price\":1100,\"min_price\":500,\"condition\":[\"good\",\"like_new\"],\"offer_percentage\":75,\"auto_message\":false,\"message_template\":\"Hi, would you accept EUR {{.OfferPrice}}?\"}]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`

// TestGeneratorRequestShape_ModelOverride verifies that:
//   - When SetModel is called, the outgoing request body carries the overridden model.
//   - The request body has response_format.type=="json_schema".
//   - The request body has response_format.json_schema.strict==true.
//   - The schema object is non-empty.
//
// (XOL-60 SUP-9 AC)
func TestGeneratorRequestShape_ModelOverride(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validGeneratorResponse))
	}))
	defer srv.Close()

	gen := New(config.AIConfig{
		Enabled: true,
		APIKey:  "test-key",
		Model:   "gpt-4o-mini",
		BaseURL: srv.URL,
	})
	gen.SetModel("gpt-5-nano") // per-call-site override
	gen.client = srv.Client()

	searches, err := gen.GenerateSearches(context.Background(), "sony camera")
	if err != nil {
		t.Fatalf("GenerateSearches() error = %v", err)
	}
	if len(searches) == 0 {
		t.Fatal("expected at least one search result")
	}

	// Assert model override propagated.
	if got, _ := captured["model"].(string); got != "gpt-5-nano" {
		t.Errorf("expected model=gpt-5-nano in request, got %q", got)
	}

	// Assert response_format.type == "json_schema".
	rf, ok := captured["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %#v", captured["response_format"])
	}
	if got := rf["type"]; got != "json_schema" {
		t.Errorf("expected response_format.type=json_schema, got %q", got)
	}

	// Assert response_format.json_schema.strict == true.
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema missing or wrong type: %#v", rf["json_schema"])
	}
	if got := js["strict"]; got != true {
		t.Errorf("expected response_format.json_schema.strict=true, got %v", got)
	}

	// Assert schema is non-empty.
	schema, ok := js["schema"].(map[string]any)
	if !ok || len(schema) == 0 {
		t.Errorf("expected non-empty schema, got %#v", js["schema"])
	}

	// gpt-5 compliance (XOL-66): temperature must be absent (gpt-5 rejects non-default),
	// and max_completion_tokens must be present.
	if _, hasTemp := captured["temperature"]; hasTemp {
		t.Errorf("expected temperature absent from request (gpt-5 rejects non-default), got %v", captured["temperature"])
	}
	if got, _ := captured["max_completion_tokens"].(float64); got != 2048 {
		t.Errorf("expected max_completion_tokens=2048, got %v", captured["max_completion_tokens"])
	}
}

// TestGenericSearchesReturnsThreeEntries — W19-37 / XOL-134 regression.
// Before the fix, genericSearches returned 1 entry for non-sony/non-camera
// inputs. After the fix it must return exactly 3 deterministic entries.
// Uses AI-disabled config so GenerateSearches follows the static fallback path.
func TestGenericSearchesReturnsThreeEntries(t *testing.T) {
	gen := New(config.AIConfig{
		Enabled: false,
	})
	// "Fujifilm X-T4" is not matched by sonyCameraSearches or canonSearches,
	// so it falls through to genericSearches.
	searches, err := gen.GenerateSearches(context.Background(), "Fujifilm X-T4")
	if err != nil {
		t.Fatalf("GenerateSearches() unexpected error: %v", err)
	}
	if len(searches) < 3 {
		t.Errorf("GenerateSearches(Fujifilm X-T4) returned %d entries, want >= 3 (XOL-134 floor)", len(searches))
		for i, s := range searches {
			t.Logf("  searches[%d]: name=%q query=%q", i, s.Name, s.Query)
		}
	}
}

// TestGeneratorRequestShape_ModelFallthrough verifies that when SetModel is NOT
// called, the outgoing request uses cfg.Model (XOL-60 SUP-9 AC).
func TestGeneratorRequestShape_ModelFallthrough(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validGeneratorResponse))
	}))
	defer srv.Close()

	gen := New(config.AIConfig{
		Enabled: true,
		APIKey:  "test-key",
		Model:   "gpt-4o-mini",
		BaseURL: srv.URL,
	})
	// No SetModel — should fall through to cfg.Model.
	gen.client = srv.Client()

	_, err := gen.GenerateSearches(context.Background(), "sony camera")
	if err != nil {
		t.Fatalf("GenerateSearches() error = %v", err)
	}

	if got, _ := captured["model"].(string); got != "gpt-4o-mini" {
		t.Errorf("expected model=gpt-4o-mini (fallthrough), got %q", got)
	}
}
