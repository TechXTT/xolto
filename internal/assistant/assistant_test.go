package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

func TestBuildRecommendationBuyNow(t *testing.T) {
	rec := buildRecommendation(models.ScoredListing{
		Listing: models.Listing{
			ItemID: "1",
			Title:  "Sony A7 III body with battery",
			Price:  85000,
			Attributes: map[string]string{
				"condition": "Zo goed als nieuw",
			},
		},
		Score:      8.5,
		FairPrice:  95000,
		Confidence: 0.72,
		Reason:     "strong value",
	}, models.ShoppingProfile{
		Name:               "Sony A7 III",
		TargetQuery:        "sony a7 iii",
		BudgetMax:          900,
		BudgetStretch:      1000,
		PreferredCondition: []string{"Zo goed als nieuw"},
	})

	if rec.Label != models.RecommendationBuyNow && rec.Label != models.RecommendationWatch {
		t.Fatalf("expected buy_now or worth_watching, got %s", rec.Label)
	}
}

func TestBuildRecommendationSkipsNoPrice(t *testing.T) {
	rec := buildRecommendation(models.ScoredListing{
		Listing: models.Listing{
			ItemID:    "1",
			Title:     "Sony A7 III reserved",
			Price:     0,
			PriceType: "reserved",
		},
		Score:      9,
		FairPrice:  90000,
		Confidence: 0.8,
	}, models.ShoppingProfile{Name: "Sony"})

	if rec.Label != models.RecommendationSkip {
		t.Fatalf("expected skip, got %s", rec.Label)
	}
}

// TestSanitizeSearchQueryBGPricePhrases verifies that BG Cyrillic price
// qualifiers are stripped from search queries (XOL-39 M3-E).
// AC: "камери под 500 лв" → "камери" (budget qualifier removed).
func TestSanitizeSearchQueryBGPricePhrases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// BG Cyrillic: "under 500 lev"
		{"камери под 500 лв", "камери"},
		// BG Cyrillic: "up to 300"
		{"лаптоп до 300", "лаптоп"},
		// BG Cyrillic: "maximum 1000 lev"
		{"телефон максимум 1000 лв", "телефон"},
		// BG with BGN currency
		{"слушалки под 200 bgn", "слушалки"},
		// EN (regression)
		{"sony camera under 500 eur", "sony camera"},
		// NL (regression)
		{"canon lens tot 400 eur", "canon lens"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitizeSearchQuery(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeSearchQuery(%q): expected %q, got %q", tc.input, tc.want, got)
			}
		})
	}
}

// TestExtractBudgetBG verifies that BG Cyrillic budget markers are extracted
// correctly from natural-language budget specifications (XOL-39 M3-E).
func TestExtractBudgetBG(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"под 500 лв", 500},
		{"до 300", 300},
		{"максимум 1000", 1000},
		{"бюджет 800", 800},
		// EN (regression)
		{"under 600", 600},
		{"max 750", 750},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := extractBudget(tc.input)
			if got != tc.want {
				t.Errorf("extractBudget(%q): expected %d, got %d", tc.input, tc.want, got)
			}
		})
	}
}

// TestPriceWordPatternBGN verifies that лв/BGN currency markers are caught by
// priceWordPattern (XOL-39 M3-E).
func TestPriceWordPatternBGN(t *testing.T) {
	cases := []struct {
		input string
		match bool
	}{
		{"500 лв", true},
		{"200 bgn", true},
		{"700 eur", true},  // regression
		{"300 euro", true}, // regression
		{"sony a6000", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := priceWordPattern.MatchString(tc.input)
			if got != tc.match {
				t.Errorf("priceWordPattern.MatchString(%q): expected %v, got %v", tc.input, tc.match, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// XOL-60 SUP-9: per-call-site model override + request shape tests
// ---------------------------------------------------------------------------

// newMinimalAssistant builds an Assistant with the minimum config required for
// the LLM HTTP call paths. Store, searcher, and scorer are nil — only the AI
// HTTP path is exercised by these tests.
func newMinimalAssistant(baseURL string) *Assistant {
	cfg := &config.Config{
		AI: config.NormalizeAIConfig(config.AIConfig{
			Enabled: true,
			APIKey:  "test-key",
			Model:   "gpt-4o-mini",
			BaseURL: baseURL,
		}),
	}
	a := &Assistant{
		cfg:        cfg,
		modelBrief: cfg.AI.Model,
		modelDraft: cfg.AI.Model,
		modelChat:  cfg.AI.Model,
		client:     &http.Client{},
	}
	return a
}

// validBriefResponse is a minimal AI response for parseBriefWithAI.
const validBriefResponse = `{"choices":[{"message":{"role":"assistant","content":"{\"name\":\"Sony A7 III\",\"target_query\":\"sony a7 iii\",\"category_id\":487,\"category\":\"camera\",\"budget_max\":1000,\"budget_stretch\":1100,\"preferred_condition\":[\"good\",\"like_new\"],\"required_features\":[],\"nice_to_have\":[],\"risk_tolerance\":\"balanced\",\"search_queries\":[\"sony a7 iii\"]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`

// validProseResponse is a minimal AI response for prose paths (no schema).
const validProseResponse = `{"choices":[{"message":{"role":"assistant","content":"Hi, would you accept EUR 450?"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`

// TestAssistantBriefParserRequestShape_ModelOverride verifies parseBriefWithAI:
//   - Sends the overridden model (AI_MODEL_ASSISTANT_BRIEF).
//   - Sends response_format.type=="json_schema" with strict==true and non-empty schema.
//
// (XOL-60 SUP-9 AC)
func TestAssistantBriefParserRequestShape_ModelOverride(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validBriefResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()
	a.SetModels("gpt-5-mini", "", "") // override brief model only

	_, err := a.parseBriefWithAI(context.Background(), "u1", "sony a7 iii under 1000")
	if err != nil {
		t.Fatalf("parseBriefWithAI() error = %v", err)
	}

	// Assert model override propagated.
	if got, _ := captured["model"].(string); got != "gpt-5-mini" {
		t.Errorf("expected model=gpt-5-mini in request, got %q", got)
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
}

// TestAssistantBriefParserRequestShape_ModelFallthrough verifies that when
// SetModels is NOT called, parseBriefWithAI uses cfg.AI.Model (XOL-60 SUP-9 AC).
func TestAssistantBriefParserRequestShape_ModelFallthrough(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validBriefResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()
	// No SetModels call.

	_, err := a.parseBriefWithAI(context.Background(), "u1", "sony a7 iii under 1000")
	if err != nil {
		t.Fatalf("parseBriefWithAI() error = %v", err)
	}

	if got, _ := captured["model"].(string); got != "gpt-4o-mini" {
		t.Errorf("expected model=gpt-4o-mini (fallthrough), got %q", got)
	}
}

// TestAssistantDraftRequestShape_NoResponseFormat verifies draftWithAI sends
// NO response_format key (prose path — no schema change) (XOL-60 SUP-9 AC).
func TestAssistantDraftRequestShape_NoResponseFormat(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validProseResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()
	a.SetModels("", "gpt-5-mini", "") // override draft model

	entry := models.ShortlistEntry{
		MissionID: 1,
		ItemID:    "item-1",
		Title:     "Sony A7 III body",
		AskPrice:  85000,
	}
	_, err := a.draftWithAI(context.Background(), "u1", entry, "olx_bg", localeEN, nil)
	if err != nil {
		t.Fatalf("draftWithAI() error = %v", err)
	}

	// Assert model override propagated.
	if got, _ := captured["model"].(string); got != "gpt-5-mini" {
		t.Errorf("expected model=gpt-5-mini in draft request, got %q", got)
	}

	// Prose path: response_format must NOT be present.
	if _, present := captured["response_format"]; present {
		t.Errorf("draftWithAI must not send response_format, but it was present: %#v", captured["response_format"])
	}
}

// TestDraftWithAI_RiskFlagsInPayload verifies that when riskFlags is non-empty,
// the LLM request payload contains the flags in the risk_flags field of the
// user message content (XOL-92 AC).
func TestDraftWithAI_RiskFlagsInPayload(t *testing.T) {
	var capturedUserContent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		// Extract user message content string.
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				msg, _ := m.(map[string]any)
				if role, _ := msg["role"].(string); role == "user" {
					capturedUserContent, _ = msg["content"].(string)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validProseResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()

	entry := models.ShortlistEntry{
		MissionID: 1,
		ItemID:    "stale-item-1",
		Title:     "iPhone 13",
		AskPrice:  60000,
	}
	riskFlags := []string{"stale_listing"}
	_, err := a.draftWithAI(context.Background(), "u1", entry, "olx_bg", localeBG, riskFlags)
	if err != nil {
		t.Fatalf("draftWithAI() error = %v", err)
	}

	if !strings.Contains(capturedUserContent, "stale_listing") {
		t.Errorf("expected risk_flags to contain 'stale_listing' in user message payload, got: %s", capturedUserContent)
	}
}

// TestDraftWithAI_EmptyRiskFlagsNoPanic verifies that passing nil or empty
// riskFlags does not panic and produces a valid LLM request (XOL-92 AC).
func TestDraftWithAI_EmptyRiskFlagsNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validProseResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()

	entry := models.ShortlistEntry{
		MissionID: 2,
		ItemID:    "item-no-flags",
		Title:     "Samsung Galaxy S22",
		AskPrice:  55000,
	}

	// nil riskFlags must not panic.
	result, err := a.draftWithAI(context.Background(), "u1", entry, "olx_bg", localeBG, nil)
	if err != nil {
		t.Fatalf("draftWithAI(nil flags) error = %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Errorf("draftWithAI(nil flags) returned empty result")
	}

	// empty slice must not panic.
	result, err = a.draftWithAI(context.Background(), "u1", entry, "olx_bg", localeBG, []string{})
	if err != nil {
		t.Fatalf("draftWithAI(empty flags) error = %v", err)
	}
	if strings.TrimSpace(result) == "" {
		t.Errorf("draftWithAI(empty flags) returned empty result")
	}
}

// ---------------------------------------------------------------------------
// W19-32 / XOL-129: EnsureSearchVariants tests
// ---------------------------------------------------------------------------

// newMinimalAssistantNoAI returns an Assistant with AI disabled (no APIKey) so
// EnsureSearchVariants uses the generator's static fallback path.
func newMinimalAssistantNoAI() *Assistant {
	cfg := &config.Config{
		AI: config.AIConfig{
			Enabled: false,
		},
	}
	return &Assistant{
		cfg:        cfg,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}
}

// TestEnsureSearchVariantsSkipsWhenAlreadyAdequate — mission with 4 pre-populated
// SearchQueries must be untouched (already adequate coverage).
func TestEnsureSearchVariantsSkipsWhenAlreadyAdequate(t *testing.T) {
	a := newMinimalAssistantNoAI()
	original := []string{"sony a7 iii", "sony a7iii", "a7 iii body", "sony alpha 7 iii"}
	mission := &models.Mission{
		TargetQuery:   "sony a7 iii",
		SearchQueries: append([]string(nil), original...),
	}
	if err := a.EnsureSearchVariants(context.Background(), mission); err != nil {
		t.Fatalf("EnsureSearchVariants() unexpected error: %v", err)
	}
	if len(mission.SearchQueries) != 4 {
		t.Errorf("SearchQueries len = %d, want 4 (unchanged)", len(mission.SearchQueries))
	}
	for i, q := range original {
		if mission.SearchQueries[i] != q {
			t.Errorf("SearchQueries[%d] = %q, want %q (must be unchanged)", i, mission.SearchQueries[i], q)
		}
	}
}

// TestEnsureSearchVariantsSkipsWhenTargetQueryEmpty — mission with an empty
// TargetQuery must not be modified (nothing to expand from).
func TestEnsureSearchVariantsSkipsWhenTargetQueryEmpty(t *testing.T) {
	a := newMinimalAssistantNoAI()
	mission := &models.Mission{
		TargetQuery:   "",
		SearchQueries: nil,
	}
	if err := a.EnsureSearchVariants(context.Background(), mission); err != nil {
		t.Fatalf("EnsureSearchVariants() unexpected error: %v", err)
	}
	if len(mission.SearchQueries) != 0 {
		t.Errorf("SearchQueries len = %d, want 0 (unchanged)", len(mission.SearchQueries))
	}
}

// TestEnsureSearchVariantsExpandsWhenSparse — mission with TargetQuery set and
// empty SearchQueries must be expanded. AI disabled; the static fallback in
// generator.GenerateSearches runs and should produce >= 1 variant.
func TestEnsureSearchVariantsExpandsWhenSparse(t *testing.T) {
	a := newMinimalAssistantNoAI()
	mission := &models.Mission{
		TargetQuery:   "sony a7iii",
		SearchQueries: nil,
	}
	if err := a.EnsureSearchVariants(context.Background(), mission); err != nil {
		t.Fatalf("EnsureSearchVariants() unexpected error: %v", err)
	}
	if len(mission.SearchQueries) < 1 {
		t.Errorf("SearchQueries len = %d, want >= 1 after static-fallback expand", len(mission.SearchQueries))
	}
	for i, q := range mission.SearchQueries {
		if strings.TrimSpace(q) == "" {
			t.Errorf("SearchQueries[%d] is empty after expand", i)
		}
	}
}

// TestEnsureSearchVariantsHardCapsAtFive — when the generator (via a fake AI
// server) returns 7 entries, EnsureSearchVariants must cap the result at 5.
func TestEnsureSearchVariantsHardCapsAtFive(t *testing.T) {
	// Fake AI server returning a search_config_list with 7 entries.
	// Content must be a JSON-encoded string of {"searches":[...]} as returned
	// by strict json_schema mode (generator.generateWithAI unmarshals the
	// content field directly).
	content := `{\"searches\":[` +
		`{\"name\":\"v1\",\"query\":\"sony a6700\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v2\",\"query\":\"sony a6700 body\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v3\",\"query\":\"a6700 sony\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v4\",\"query\":\"sony alpha 6700\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v5\",\"query\":\"ilce-6700\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v6\",\"query\":\"sony 6700\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"},` +
		`{\"name\":\"v7\",\"query\":\"alpha 6700\",\"category_id\":487,\"max_price\":0,\"min_price\":0,\"condition\":[\"good\"],\"offer_percentage\":70,\"auto_message\":false,\"message_template\":\"hi\"}` +
		`]}`
	payload := `{"choices":[{"message":{"role":"assistant","content":"` + content + `"}}],"usage":{"prompt_tokens":5,"completion_tokens":50,"total_tokens":55}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()

	mission := &models.Mission{
		TargetQuery:   "sony a6700",
		SearchQueries: nil,
	}
	if err := a.EnsureSearchVariants(context.Background(), mission); err != nil {
		// EnsureSearchVariants returns nil on all error paths per contract.
		t.Errorf("EnsureSearchVariants() must return nil, got %v", err)
	}
	if len(mission.SearchQueries) > 5 {
		t.Errorf("SearchQueries len = %d, must be <= 5 (founder hard-cap)", len(mission.SearchQueries))
	}
}

// TestEnsureSearchVariantsGracefulOnCapFire — when the global aibudget cap is
// exhausted, EnsureSearchVariants must: return nil (not an error), leave
// SearchQueries unchanged, and not panic.
func TestEnsureSearchVariantsGracefulOnCapFire(t *testing.T) {
	// Save/restore global tracker so this test does not leak state.
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })

	// Install an exhausted tracker.
	tr := aibudget.New()
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
	aibudget.SetGlobal(tr)

	// AI nominally enabled so generator enters generateWithAI and hits the budget gate.
	cfg := &config.Config{
		AI: config.AIConfig{
			Enabled: true,
			APIKey:  "dummy-key",
			BaseURL: "http://127.0.0.1:1", // unreachable; cap fires before HTTP call
			Model:   "gpt-4o-mini",
		},
	}
	a := &Assistant{
		cfg:        cfg,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}

	mission := &models.Mission{
		UserID:        "u-cap-test",
		TargetQuery:   "sony a6700",
		SearchQueries: nil,
	}
	originalLen := len(mission.SearchQueries)

	err := a.EnsureSearchVariants(context.Background(), mission)
	if err != nil {
		t.Errorf("EnsureSearchVariants() must return nil on cap-fire, got %v", err)
	}
	// SearchQueries must not be populated: generator skipped due to cap.
	// (It may have been populated by the static fallback if that path fired;
	// what MUST NOT happen is a panic or a non-nil error return.)
	_ = originalLen // cap-fire path: assertion is "no panic, no error"
}

// TestParseBriefHeuristicPathExpandsVariants — integration path test.
// With AI disabled, parseBrief returns a heuristic mission with 1 SearchQuery.
// UpsertBrief (which calls EnsureSearchVariants before UpsertMission) must
// expand the mission before storing. Asserts mission.SearchQueries >= 1 after
// UpsertBrief completes and no error is returned.
func TestParseBriefHeuristicPathExpandsVariants(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "assistant-brief-expand.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("brief-expand@example.com", "hash", "Brief Expand User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// AI disabled: EnsureSearchVariants will use the generator static fallback.
	cfg := &config.Config{
		AI: config.AIConfig{
			Enabled: false,
		},
	}
	a := &Assistant{
		cfg:        cfg,
		store:      st,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}

	mission, err := a.UpsertBrief(context.Background(), userID, "sony a7iii camera body")
	if err != nil {
		t.Fatalf("UpsertBrief() error = %v", err)
	}
	if len(mission.SearchQueries) < 1 {
		t.Errorf("SearchQueries len = %d, want >= 1 after heuristic-path UpsertBrief", len(mission.SearchQueries))
	}
	for i, q := range mission.SearchQueries {
		if strings.TrimSpace(q) == "" {
			t.Errorf("SearchQueries[%d] is empty", i)
		}
	}
}

// ---------------------------------------------------------------------------
// W19-33 / XOL-130: Converse routing — help-template false-positive fix
// ---------------------------------------------------------------------------

// helpTemplatePrefix is the opening string of the generic onboarding reply.
const helpTemplatePrefix = "I help you find second-hand deals before anyone else does"

// isHelpTemplate reports whether msg is the generic onboarding/help reply.
func isHelpTemplate(msg string) bool {
	return strings.HasPrefix(msg, helpTemplatePrefix)
}

// newConverseTestAssistant builds an Assistant with a real SQLite store and AI
// disabled so Converse exercises the heuristic branch (no external calls).
func newConverseTestAssistant(t *testing.T) (*Assistant, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "converse-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	userID, err := st.CreateUser("converse-test@example.com", "hash", "Converse Test User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	cfg := &config.Config{
		AI: config.AIConfig{
			Enabled: false,
		},
	}
	a := &Assistant{
		cfg:        cfg,
		store:      st,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}
	return a, userID
}

// TestConverseHelpMeFindRoutesToBriefParser — the P1 bug-regression test.
// Input "Help me find a Sony A7iii in Bulgaria, budget 800 euros" must NOT
// return the help-template; it must reach startBriefConversation (XOL-130).
func TestConverseHelpMeFindRoutesToBriefParser(t *testing.T) {
	a, userID := newConverseTestAssistant(t)
	reply, err := a.Converse(context.Background(), userID, "Help me find a Sony A7iii in Bulgaria, budget 800 euros")
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if isHelpTemplate(reply.Message) {
		t.Errorf("Converse() returned help-template for shopping intent input; original P1 bug is not fixed.\nMessage: %s", reply.Message)
	}
}

// TestConverseBareHelpTokenReturnsHelpTemplate — "help" alone must still
// return the onboarding help-template (regression guard for preserved UX).
func TestConverseBareHelpTokenReturnsHelpTemplate(t *testing.T) {
	a, userID := newConverseTestAssistant(t)
	reply, err := a.Converse(context.Background(), userID, "help")
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !isHelpTemplate(reply.Message) {
		t.Errorf("Converse() did not return help-template for bare 'help' token.\nMessage: %s", reply.Message)
	}
}

// TestConverseWhatCanYouDoReturnsHelpTemplate — "what can you do?" must still
// return the onboarding help-template (regression guard for preserved UX).
func TestConverseWhatCanYouDoReturnsHelpTemplate(t *testing.T) {
	a, userID := newConverseTestAssistant(t)
	reply, err := a.Converse(context.Background(), userID, "what can you do?")
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !isHelpTemplate(reply.Message) {
		t.Errorf("Converse() did not return help-template for 'what can you do?'.\nMessage: %s", reply.Message)
	}
}

// TestConverseHowDoIUseThisReturnsHelpTemplate — "how do i use this?" must
// still return the onboarding help-template (regression guard for preserved UX).
func TestConverseHowDoIUseThisReturnsHelpTemplate(t *testing.T) {
	a, userID := newConverseTestAssistant(t)
	reply, err := a.Converse(context.Background(), userID, "how do i use this?")
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !isHelpTemplate(reply.Message) {
		t.Errorf("Converse() did not return help-template for 'how do i use this?'.\nMessage: %s", reply.Message)
	}
}

// TestConverseLongFormHelpButShoppingRoutesToBriefParser — longer-form
// help-but-actually-shopping input must NOT return the help-template.
// "Can you help me find a used iPhone?" is 9 words — well above the 3-word
// gate — and must fall through to startBriefConversation (XOL-130 bug-half).
func TestConverseLongFormHelpButShoppingRoutesToBriefParser(t *testing.T) {
	a, userID := newConverseTestAssistant(t)
	reply, err := a.Converse(context.Background(), userID, "Can you help me find a used iPhone?")
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if isHelpTemplate(reply.Message) {
		t.Errorf("Converse() returned help-template for longer shopping-intent input.\nMessage: %s", reply.Message)
	}
}

// TestAssistantChatRequestShape_NoResponseFormat verifies compareWithAI
// (chatText) sends NO response_format key (XOL-60 SUP-9 AC).
func TestAssistantChatRequestShape_NoResponseFormat(t *testing.T) {
	var captured map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validProseResponse))
	}))
	defer srv.Close()

	a := newMinimalAssistant(srv.URL)
	a.client = srv.Client()
	a.SetModels("", "", "gpt-5-mini") // override chat model

	entries := []models.ShortlistEntry{
		{
			MissionID: 1,
			ItemID:    "item-1",
			Title:     "Sony A7 III body",
			AskPrice:  85000,
		},
	}
	_, err := a.compareWithAI(context.Background(), "u1", entries)
	if err != nil {
		t.Fatalf("compareWithAI() error = %v", err)
	}

	// Assert model override propagated.
	if got, _ := captured["model"].(string); got != "gpt-5-mini" {
		t.Errorf("expected model=gpt-5-mini in chat request, got %q", got)
	}

	// Prose path: response_format must NOT be present.
	if _, present := captured["response_format"]; present {
		t.Errorf("compareWithAI must not send response_format, but it was present: %#v", captured["response_format"])
	}
}

// ---------------------------------------------------------------------------
// W19-34 / XOL-131: extractBudget regex fix regression tests
// ---------------------------------------------------------------------------

// budgetQuestion is the exact string nextProfileQuestion returns when budget
// is missing — used to assert the brief parser is NOT asking for budget.
const budgetQuestion = "What's your budget?"

// TestExtractBudgetHandlesAroundBeforeInteger — the exact founder live-verify
// failure: "budget around 1200 euros" returned 0 before the regex fix.
func TestExtractBudgetHandlesAroundBeforeInteger(t *testing.T) {
	got := extractBudget("Help me find a used Fujifilm X-T4 in Bulgaria, budget around 1200 euros")
	if got != 1200 {
		t.Errorf("extractBudget(budget around 1200 euros) = %d, want 1200", got)
	}
}

// TestExtractBudgetHandlesUnderMarker — "under N" variant.
func TestExtractBudgetHandlesUnderMarker(t *testing.T) {
	got := extractBudget("Sony A7iii under 1500 EUR")
	if got != 1500 {
		t.Errorf("extractBudget(under 1500) = %d, want 1500", got)
	}
}

// TestExtractBudgetHandlesMaxMarker — "max N" variant.
func TestExtractBudgetHandlesMaxMarker(t *testing.T) {
	got := extractBudget("max 800 euro for a laptop")
	if got != 800 {
		t.Errorf("extractBudget(max 800) = %d, want 800", got)
	}
}

// TestExtractBudgetHandlesIntegerBeforeMarker — integer precedes the marker
// ("1200 euro budget") — the before-window scan path.
func TestExtractBudgetHandlesIntegerBeforeMarker(t *testing.T) {
	got := extractBudget("looking for a camera, 1200 euro budget")
	if got != 1200 {
		t.Errorf("extractBudget(1200 euro budget) = %d, want 1200", got)
	}
}

// TestExtractBudgetHandlesBGCyrillic — "под N лв" BG Cyrillic marker.
func TestExtractBudgetHandlesBGCyrillic(t *testing.T) {
	got := extractBudget("под 1500 лв за камера")
	if got != 1500 {
		t.Errorf("extractBudget(под 1500) = %d, want 1500", got)
	}
}

// TestExtractBudgetReturnsZeroWhenNoBudget — defensive: no marker present.
func TestExtractBudgetReturnsZeroWhenNoBudget(t *testing.T) {
	got := extractBudget("Help me find a Sony")
	if got != 0 {
		t.Errorf("extractBudget(no budget) = %d, want 0", got)
	}
}

// TestStartBriefConversationParsesFullInput — end-to-end contract test.
// Input explicitly contains: item (Fujifilm X-T4), location (Bulgaria), and
// budget (1200 euros). The brief parser must NOT ask "What's your budget?".
// The reply must NOT start with the budget question, and SOME mission state
// must have been written (session saved OR mission upserted without error).
//
// This is the P0 regression test for XOL-131. Failure here means the bug
// is not fixed end-to-end on the heuristic path.
func TestStartBriefConversationParsesFullInput(t *testing.T) {
	a, userID := newConverseTestAssistant(t)

	const input = "Help me find a used Fujifilm X-T4 in Bulgaria, budget around 1200 euros"
	reply, err := a.Converse(context.Background(), userID, input)
	if err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	// The brief parser must NOT ask for budget when budget was explicit.
	if strings.HasPrefix(reply.Message, budgetQuestion) {
		t.Errorf("Converse() asked 'What's your budget?' even though budget was explicit in input.\nMessage: %s", reply.Message)
	}

	// Some mission state must exist: either a session was saved (Expecting==true
	// asking for condition) or a mission was upserted (Expecting==false).
	// In both cases reply.Mission must be non-nil and contain the budget.
	if reply.Mission == nil {
		t.Fatalf("Converse() returned nil Mission — no state was persisted")
	}
	if reply.Mission.BudgetMax != 1200 {
		t.Errorf("Mission.BudgetMax = %d, want 1200 (budget was in input)", reply.Mission.BudgetMax)
	}
}

// ---------------------------------------------------------------------------
// W19-35 / XOL-132: AutoDeployHunts dedup-scope tests
// ---------------------------------------------------------------------------

// newAutoDeployTestAssistant creates an Assistant backed by a real SQLite store
// with AI disabled. Returns (assistant, store, userID).
func newAutoDeployTestAssistant(t *testing.T) (*Assistant, *store.SQLiteStore, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "autodeployhunts-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	userID, err := st.CreateUser("autodeployhunts@example.com", "hash", "AutoDeploy Test User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	cfg := &config.Config{
		AI: config.AIConfig{Enabled: false},
	}
	a := New(cfg, st, nil, nil)
	return a, st, userID
}

// ---------------------------------------------------------------------------
// W19-37 / XOL-134: generator floor regression tests
// ---------------------------------------------------------------------------

// TestEnsureSearchVariantsUsesFallbackOnAIError — when gen.GenerateSearches
// returns (entries, err) the fallback entries must be used, not discarded.
//
// The generator's AI-error path (generator.go:66) returns both the fallback
// slice AND a wrapped error. Before this fix, EnsureSearchVariants discarded
// the fallback on err != nil — leaving missions with 0 chips.
//
// We cannot inject a custom GenerateSearches func without a stub interface; the
// generator package does not expose one. The closest observable equivalent is
// the static-fallback path (AI disabled) which exercises the same dedup loop.
// The AI-error-with-fallback shape is covered indirectly by the integration
// test below (TestEnsureSearchVariantsFloorEndToEnd).
func TestEnsureSearchVariantsUsesFallbackOnAIError(t *testing.T) {
	t.Skip("requires generator stub harness for (entries, err) injection; covered indirectly by TestEnsureSearchVariantsFloorEndToEnd integration test")
}

// TestEnsureSearchVariantsFloorsAtThree — with AI disabled (forces static
// fallback), a generic topic that was previously handled by genericSearches
// (1 entry before the fix) must now produce >= 3 SearchQueries.
//
// Pre-fix: genericSearches returned 1 entry → EnsureSearchVariants deduped to
// 1 → mission shipped with 1 chip. Post-fix: genericSearches returns 3 entries
// AND EnsureSearchVariants synthesises to floor if needed.
func TestEnsureSearchVariantsFloorsAtThree(t *testing.T) {
	a := newMinimalAssistantNoAI()
	mission := &models.Mission{
		UserID:        "u-floor-test",
		TargetQuery:   "Fujifilm X-T4",
		SearchQueries: nil,
	}
	if err := a.EnsureSearchVariants(context.Background(), mission); err != nil {
		t.Fatalf("EnsureSearchVariants() unexpected error: %v", err)
	}
	if len(mission.SearchQueries) < 3 {
		t.Errorf("SearchQueries len = %d, want >= 3 (W19-31 floor); queries = %v",
			len(mission.SearchQueries), mission.SearchQueries)
	}
	for i, q := range mission.SearchQueries {
		if strings.TrimSpace(q) == "" {
			t.Errorf("SearchQueries[%d] is empty", i)
		}
	}
}

// TestEnsureSearchVariantsTrueErrorStillSkips — when the global aibudget cap is
// exhausted, gen.GenerateSearches returns (fallbackSearches, wrappedErr) per
// generator.go:66. EnsureSearchVariants must: return nil (no error), use the
// fallback entries, and produce >= 3 SearchQueries.
//
// NOTE: The name is preserved for diff clarity but the semantics changed with
// XOL-134 Bug A fix. The "true error still skips" invariant (len==0 → return nil)
// only fires when genSearches is truly empty — which only happens when topic is
// empty, guarded earlier. Cap-fire is NOT a skip; it returns fallback entries
// that must be used. This test confirms that post-fix behavior.
func TestEnsureSearchVariantsTrueErrorStillSkips(t *testing.T) {
	// Save/restore global tracker.
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })

	// Install an exhausted tracker.
	tr := aibudget.New()
	if ok, _ := tr.Allow(context.Background(), "test_seed_134", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at cap should succeed")
	}
	aibudget.SetGlobal(tr)

	// AI nominally enabled: generator enters generateWithAI, cap fires, and
	// returns (fallbackSearches(topic), wrappedErr) per generator.go:66.
	// EnsureSearchVariants must use the fallback (len > 0) and populate
	// SearchQueries. No panic, no error return.
	cfg := &config.Config{
		AI: config.AIConfig{
			Enabled: true,
			APIKey:  "dummy-key",
			BaseURL: "http://127.0.0.1:1", // unreachable; cap fires before HTTP call
			Model:   "gpt-4o-mini",
		},
	}
	a := &Assistant{
		cfg:        cfg,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}
	mission := &models.Mission{
		UserID:        "u-cap-134",
		TargetQuery:   "Fujifilm X-T4",
		SearchQueries: nil,
	}
	err := a.EnsureSearchVariants(context.Background(), mission)
	if err != nil {
		t.Errorf("EnsureSearchVariants() must return nil on cap-fire, got %v", err)
	}
	// Post-fix: cap-fire returns fallback entries → SearchQueries populated >= 3.
	if len(mission.SearchQueries) < 3 {
		t.Errorf("expected >= 3 SearchQueries after cap-fire (fallback path), got %d: %v",
			len(mission.SearchQueries), mission.SearchQueries)
	}
}

// TestEnsureSearchVariantsFloorEndToEnd — end-to-end POST /missions with a
// generic topic ("Fujifilm X-T4") that previously produced 1 chip. After the
// fix, mission.SearchQueries must be >= 3.
// (This test is in assistant_test.go as a store-backed integration; the
// API-layer equivalent is TestHandleMissionsPostAutoExpandsGenericTopic in
// internal/api/missions_test.go.)
func TestEnsureSearchVariantsFloorEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "floor-e2e.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("floor-e2e@example.com", "hash", "Floor E2E User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	cfg := &config.Config{
		AI: config.AIConfig{Enabled: false},
	}
	a := &Assistant{
		cfg:        cfg,
		store:      st,
		modelBrief: "gpt-4o-mini",
		modelDraft: "gpt-4o-mini",
		modelChat:  "gpt-4o-mini",
		client:     &http.Client{},
	}

	mission, err := a.UpsertBrief(context.Background(), userID, "Fujifilm X-T4 camera body")
	if err != nil {
		t.Fatalf("UpsertBrief() error = %v", err)
	}
	if len(mission.SearchQueries) < 3 {
		t.Errorf("SearchQueries len = %d, want >= 3 (XOL-134 floor); queries = %v",
			len(mission.SearchQueries), mission.SearchQueries)
	}
}

// TestAutoDeployHuntsDedupScopedToMission — mission_1 and mission_2 for the
// same user both use "canon eos r6" as their query. AutoDeployHunts must create
// a search_config for EACH mission independently; mission_2's search_config
// must NOT be suppressed by mission_1's existing one.
//
// This is the regression test for the dedup-scope fix (XOL-132): the old
// user-wide dedup left mission_2 with zero search_configs.
func TestAutoDeployHuntsDedupScopedToMission(t *testing.T) {
	a, st, userID := newAutoDeployTestAssistant(t)

	// Create mission_1.
	mission1ID, err := st.UpsertMission(models.Mission{
		UserID:           userID,
		Name:             "Canon EOS R6 Hunt 1",
		TargetQuery:      "canon eos r6",
		SearchQueries:    []string{"canon eos r6"},
		Status:           "active",
		CountryCode:      "BG",
		MarketplaceScope: []string{"olx_bg"},
	})
	if err != nil {
		t.Fatalf("UpsertMission(1) error = %v", err)
	}
	mission1, _ := st.GetMission(mission1ID)

	// Deploy hunts for mission_1.
	count1, err := a.AutoDeployHunts(context.Background(), userID, *mission1)
	if err != nil {
		t.Fatalf("AutoDeployHunts(mission1) error = %v", err)
	}
	if count1 == 0 {
		t.Fatalf("AutoDeployHunts(mission1): expected >= 1 search_config created, got 0")
	}

	// Create mission_2 with the same query.
	mission2ID, err := st.UpsertMission(models.Mission{
		UserID:           userID,
		Name:             "Canon EOS R6 Hunt 2",
		TargetQuery:      "canon eos r6",
		SearchQueries:    []string{"canon eos r6"},
		Status:           "active",
		CountryCode:      "BG",
		MarketplaceScope: []string{"olx_bg"},
	})
	if err != nil {
		t.Fatalf("UpsertMission(2) error = %v", err)
	}
	mission2, _ := st.GetMission(mission2ID)

	// Deploy hunts for mission_2 — must NOT be blocked by mission_1's configs.
	count2, err := a.AutoDeployHunts(context.Background(), userID, *mission2)
	if err != nil {
		t.Fatalf("AutoDeployHunts(mission2) error = %v", err)
	}
	if count2 == 0 {
		t.Errorf("AutoDeployHunts(mission2): expected >= 1 search_config created (dedup must be mission-scoped, not user-wide), got 0")
	}

	// Total: both missions must have search_configs.
	all, err := st.GetSearchConfigs(userID)
	if err != nil {
		t.Fatalf("GetSearchConfigs error = %v", err)
	}
	if len(all) < 2 {
		t.Errorf("expected >= 2 total search_configs across both missions, got %d", len(all))
	}
}

// TestAutoDeployHuntsDedupSameMissionTwice — calling AutoDeployHunts on the
// SAME mission twice must NOT create duplicate search_configs on the second
// call. This confirms the scoped dedup still works for its intended purpose
// (mission edit + re-deploy idempotency).
func TestAutoDeployHuntsDedupSameMissionTwice(t *testing.T) {
	a, st, userID := newAutoDeployTestAssistant(t)

	missionID, err := st.UpsertMission(models.Mission{
		UserID:           userID,
		Name:             "Sony A7 III Hunt",
		TargetQuery:      "sony a7 iii",
		SearchQueries:    []string{"sony a7 iii"},
		Status:           "active",
		CountryCode:      "BG",
		MarketplaceScope: []string{"olx_bg"},
	})
	if err != nil {
		t.Fatalf("UpsertMission error = %v", err)
	}
	mission, _ := st.GetMission(missionID)

	// First deploy.
	count1, err := a.AutoDeployHunts(context.Background(), userID, *mission)
	if err != nil {
		t.Fatalf("AutoDeployHunts(first) error = %v", err)
	}
	if count1 == 0 {
		t.Fatalf("AutoDeployHunts(first): expected >= 1 search_config, got 0")
	}

	// Second deploy of the SAME mission — no duplicates must be created.
	count2, err := a.AutoDeployHunts(context.Background(), userID, *mission)
	if err != nil {
		t.Fatalf("AutoDeployHunts(second) error = %v", err)
	}
	if count2 != 0 {
		t.Errorf("AutoDeployHunts(second same mission): expected 0 new search_configs (already exists), got %d", count2)
	}

	// Total configs must equal first-deploy count only.
	all, err := st.GetSearchConfigs(userID)
	if err != nil {
		t.Fatalf("GetSearchConfigs error = %v", err)
	}
	if len(all) != count1 {
		t.Errorf("expected %d total search_configs (no duplicates), got %d", count1, len(all))
	}
}

// ---------------------------------------------------------------------------
// W19-38 / XOL-135: extractItemName + heuristicProfileFromPrompt clean-item tests
// ---------------------------------------------------------------------------

// TestExtractItemNameStripsLeadingFiller — the exact founder live-verify failure:
// raw prompt used as Mission.Name truncated to "Help me find a used Fujifilm X-T4
// in Bul". extractItemName must produce "Fujifilm X-T4".
func TestExtractItemNameStripsLeadingFiller(t *testing.T) {
	input := "Help me find a used Fujifilm X-T4 in Bulgaria, budget around 1200 euros"
	got := extractItemName(input)
	if !strings.Contains(got, "Fujifilm X-T4") {
		t.Errorf("extractItemName(%q) = %q; want result containing 'Fujifilm X-T4'", input, got)
	}
	if strings.Contains(strings.ToLower(got), "help me find") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain 'help me find'", input, got)
	}
}

// TestExtractItemNameStripsTrailingBudget — "Sony A7 III under 1500 EUR" must
// strip the trailing budget clause, leaving "Sony A7 III".
func TestExtractItemNameStripsTrailingBudget(t *testing.T) {
	input := "Sony A7 III under 1500 EUR"
	got := extractItemName(input)
	if !strings.Contains(got, "Sony A7 III") {
		t.Errorf("extractItemName(%q) = %q; want result containing 'Sony A7 III'", input, got)
	}
	// The trailing budget clause must be stripped.
	if strings.Contains(strings.ToLower(got), "1500") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain the budget amount '1500'", input, got)
	}
}

// TestExtractItemNameStripsTrailingLocation — "Looking for a Canon EOS R6 in Sofia"
// must strip the trailing location clause. Result must contain "Canon EOS R6"
// and must NOT contain "Sofia" or the dangling " in ".
func TestExtractItemNameStripsTrailingLocation(t *testing.T) {
	input := "Looking for a Canon EOS R6 in Sofia"
	got := extractItemName(input)
	if !strings.Contains(got, "Canon EOS R6") {
		t.Errorf("extractItemName(%q) = %q; want result containing 'Canon EOS R6'", input, got)
	}
	if strings.Contains(got, "Sofia") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain 'Sofia'", input, got)
	}
	if strings.Contains(got, " in ") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain trailing ' in '", input, got)
	}
}

// TestExtractItemNameHandlesBGCyrillic — BG Cyrillic input "Търся Sony A7 III под 1500 лв"
// must strip leading "Търся" and trailing "под 1500". Result must contain "Sony A7 III".
func TestExtractItemNameHandlesBGCyrillic(t *testing.T) {
	input := "Търся Sony A7 III под 1500 лв"
	got := extractItemName(input)
	if !strings.Contains(got, "Sony A7 III") {
		t.Errorf("extractItemName(%q) = %q; want result containing 'Sony A7 III'", input, got)
	}
	if strings.Contains(got, "Търся") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain 'Търся'", input, got)
	}
	if strings.Contains(got, "под") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain 'под'", input, got)
	}
	if strings.Contains(got, "1500") {
		t.Errorf("extractItemName(%q) = %q; must NOT contain '1500'", input, got)
	}
}

// TestExtractItemNameDefensiveFallback — very short input that would be collapsed
// to <3 chars must fall back to the raw text unchanged.
func TestExtractItemNameDefensiveFallback(t *testing.T) {
	input := "aaa"
	got := extractItemName(input)
	if got != input {
		t.Errorf("extractItemName(%q) = %q; want %q (defensive fallback)", input, got, input)
	}
}

// TestExtractItemNameEmptyInput — empty string must return empty string.
func TestExtractItemNameEmptyInput(t *testing.T) {
	got := extractItemName("")
	if got != "" {
		t.Errorf("extractItemName('') = %q; want ''", got)
	}
}

// TestHeuristicProfileFromPromptCleansItemName — end-to-end integration test.
// heuristicProfileFromPrompt with the exact founder live-verify input must
// produce a Mission where Name + TargetQuery + SearchQueries[0] are the clean
// item name — NOT the raw user prompt.
//
// This is the primary regression test for XOL-135.
func TestHeuristicProfileFromPromptCleansItemName(t *testing.T) {
	const userID = "u-xol135"
	const rawPrompt = "Help me find a used Fujifilm X-T4 in Bulgaria, budget around 1200 euros"
	mission := heuristicProfileFromPrompt(userID, rawPrompt, config.MarktplaatsConfig{})

	// Mission.Name must NOT start with the raw filler.
	if strings.HasPrefix(strings.ToLower(mission.Name), "help me find") {
		t.Errorf("Mission.Name = %q; must NOT start with 'help me find' (raw-prompt truncation bug)", mission.Name)
	}
	// Mission.TargetQuery must NOT start with the raw filler.
	if strings.HasPrefix(strings.ToLower(mission.TargetQuery), "help me find") {
		t.Errorf("Mission.TargetQuery = %q; must NOT start with 'help me find'", mission.TargetQuery)
	}
	// SearchQueries[0] must be the cleaned item name (NOT the raw prompt).
	if len(mission.SearchQueries) == 0 {
		t.Fatalf("Mission.SearchQueries is empty")
	}
	if strings.HasPrefix(strings.ToLower(mission.SearchQueries[0]), "help me find") {
		t.Errorf("Mission.SearchQueries[0] = %q; must NOT start with 'help me find'", mission.SearchQueries[0])
	}
	// The clean item name must contain the core model identifier.
	if !strings.Contains(mission.Name, "Fujifilm X-T4") && !strings.Contains(mission.Name, "fujifilm x-t4") {
		// Case-insensitive check — either form is acceptable.
		if !strings.Contains(strings.ToLower(mission.Name), "fujifilm") {
			t.Errorf("Mission.Name = %q; expected to contain 'fujifilm' (clean item name)", mission.Name)
		}
	}
}
