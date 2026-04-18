package support_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/linear"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/store"
	"github.com/TechXTT/xolto/internal/support"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockLLMClient returns a fixed response string for every call.
// It replaces the former mockLLMClient (XOL-59 SUP-8).
type mockLLMClient struct {
	// responses is a queue; each call pops the front. If empty, the last
	// response is repeated.
	responses []string
	calls     int
	err       error
}

func (m *mockLLMClient) Complete(_ context.Context, _ support.LLMRequest) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	if len(m.responses) == 0 {
		return `{"category":"general","market":"unknown","product_cat":"other","severity":"low","action_needed":"reply_only"}`, nil
	}
	idx := m.calls - 1
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	return m.responses[idx], nil
}

// mockPlainAPI records calls and returns configurable errors.
type mockPlainAPI struct {
	getThreadFn      func(ctx context.Context, threadID string) (plain.ThreadInfo, error)
	addLabelsCalls   int
	addNoteCalls     int
	setPriorityCalls int
	setPriorityArgs  []string // captures priority values
	lastNoteBody     string
	addLabelsErr     error
	addNoteErr       error
	setPriorityErr   error
}

func (m *mockPlainAPI) GetThread(ctx context.Context, threadID string) (plain.ThreadInfo, error) {
	if m.getThreadFn != nil {
		return m.getThreadFn(ctx, threadID)
	}
	return plain.ThreadInfo{
		ThreadID:      threadID,
		Subject:       "Test subject",
		CustomerEmail: "user@example.com",
		Body:          "I have a question about pricing.",
	}, nil
}
func (m *mockPlainAPI) AddLabels(_ context.Context, _ string, _ []string) error {
	m.addLabelsCalls++
	return m.addLabelsErr
}
func (m *mockPlainAPI) AddNote(_ context.Context, _ string, body string) error {
	m.addNoteCalls++
	m.lastNoteBody = body
	return m.addNoteErr
}
func (m *mockPlainAPI) SetPriority(_ context.Context, _ string, priority string) error {
	m.setPriorityCalls++
	m.setPriorityArgs = append(m.setPriorityArgs, priority)
	return m.setPriorityErr
}

// mockLinearMCP records calls and returns configurable results.
type mockLinearMCP struct {
	createIssueCalls int
	result           linear.CreateIssueResult
	err              error
}

func (m *mockLinearMCP) CreateIssue(_ context.Context, _ linear.CreateIssueInput) (linear.CreateIssueResult, error) {
	m.createIssueCalls++
	return m.result, m.err
}

// mockStore records AttachClassification + AttachLinearIssue calls.
type mockStore struct {
	classifyCalls    int
	linearIssueCalls int
	classifyErr      error
	linearIssueErr   error
	lastClassification store.Classification
	lastLinearIssue    string
}

func (m *mockStore) UpsertEventFromWebhook(_ context.Context, e store.SupportEvent) (store.SupportEvent, error) {
	return e, nil
}
func (m *mockStore) GetByPlainThreadID(_ context.Context, _ string) (*store.SupportEvent, error) {
	return nil, nil
}
func (m *mockStore) AttachClassification(_ context.Context, _ string, c store.Classification) error {
	m.classifyCalls++
	m.lastClassification = c
	return m.classifyErr
}
func (m *mockStore) AttachLinearIssue(_ context.Context, _ string, linearIssue string) error {
	m.linearIssueCalls++
	m.lastLinearIssue = linearIssue
	return m.linearIssueErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func pricingLLMResponse() string {
	return `{"category":"pricing","market":"olx_bg","product_cat":"phone","severity":"medium","action_needed":"backend_fix"}`
}

func generalLLMResponse() string {
	return `{"category":"general","market":"unknown","product_cat":"other","severity":"low","action_needed":"reply_only"}`
}

// buildWorker constructs a ClassifierWorker with the given mocks.
func buildWorker(
	st *mockStore,
	plainAPI *mockPlainAPI,
	linearMCP *mockLinearMCP,
	llm *mockLLMClient,
	smsCallback func(ctx context.Context, event store.SupportEvent) error,
) *support.ClassifierWorker {
	cfg := support.ClassifierConfig{
		Store:       st,
		PlainAPI:    plainAPI,
		LinearMCP:   linearMCP,
		LLM:         llm,
		LLMModel:    "gpt-5-nano",
		SMSCallback: smsCallback,
		AppEnv:      "test",
	}
	return support.NewClassifierWorker(cfg)
}

// runSingle sends one event through the worker and waits for it to be processed.
// It synchronises with the worker pool via worker.Wait() so the caller can
// safely read mock state immediately after runSingle returns.
func runSingle(t *testing.T, worker *support.ClassifierWorker, event store.SupportEvent) {
	t.Helper()
	ch := make(chan store.SupportEvent, 1)
	ch <- event
	close(ch)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	worker.Start(ctx, ch, 1)
	worker.Wait()
}

// ---------------------------------------------------------------------------
// AC-2: Full per-event flow with mocked MCP clients
// ---------------------------------------------------------------------------

func TestClassifierWorker_FullFlow_PricingEvent(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{
			IssueID:    "uuid-1",
			Identifier: "XOL-99",
			URL:        "https://linear.app/xolto/issue/XOL-99",
		},
	}
	// First LLM call = classifier; second = draft note.
	llm := &mockLLMClient{
		responses: []string{pricingLLMResponse(), "Your pricing concern has been logged."},
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-100",
		PlainThreadID: "th_pricing_001",
	}
	runSingle(t, worker, event)

	// Assertions:
	if st.classifyCalls != 1 {
		t.Errorf("expected 1 AttachClassification call, got %d", st.classifyCalls)
	}
	if st.lastClassification.Category != "pricing" {
		t.Errorf("expected category=pricing, got %q", st.lastClassification.Category)
	}
	if st.lastClassification.Market != "olx_bg" {
		t.Errorf("expected market=olx_bg, got %q", st.lastClassification.Market)
	}
	// pricing/backend_fix → ActionToLinearProject["backend_fix"] = "OLX BG trust"
	if linearMCP.createIssueCalls != 1 {
		t.Errorf("expected 1 CreateIssue call for backend_fix, got %d", linearMCP.createIssueCalls)
	}
	if st.linearIssueCalls != 1 {
		t.Errorf("expected 1 AttachLinearIssue call, got %d", st.linearIssueCalls)
	}
	if plainMCP.addLabelsCalls != 1 {
		t.Errorf("expected 1 AddLabels call, got %d", plainMCP.addLabelsCalls)
	}
	if plainMCP.addNoteCalls != 1 {
		t.Errorf("expected 1 AddNote call, got %d", plainMCP.addNoteCalls)
	}
}

// ---------------------------------------------------------------------------
// AC-3: Incident path — body containing "can't log in" → severity=incident
// → priority=urgent → SMS callback invoked
// ---------------------------------------------------------------------------

func TestClassifierWorker_IncidentPath_CantLogIn(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{
		getThreadFn: func(_ context.Context, threadID string) (plain.ThreadInfo, error) {
			return plain.ThreadInfo{
				ThreadID:      threadID,
				Subject:       "Problem with access",
				CustomerEmail: "user@example.com",
				Body:          "I can't log in to my xolto account since this morning.",
			}, nil
		},
	}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{Identifier: "XOL-100", URL: "https://linear.app/xolto/issue/XOL-100"},
	}
	// LLM says severity=medium but incident-keyword override should win.
	llm := &mockLLMClient{
		responses: []string{
			`{"category":"login","market":"olx_bg","product_cat":"other","severity":"medium","action_needed":"billing_auth_fix"}`,
			"We're sorry you're having trouble logging in. Our team is investigating.",
		},
	}

	smsCalled := 0
	smsCallback := func(_ context.Context, event store.SupportEvent) error {
		smsCalled++
		if event.Severity == nil || *event.Severity != "incident" {
			t.Errorf("expected SMS callback severity=incident, got %v", event.Severity)
		}
		return nil
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, smsCallback)

	event := store.SupportEvent{
		ID:            "evt-200",
		PlainThreadID: "th_incident_001",
	}
	runSingle(t, worker, event)

	// Severity must be upgraded to incident by keyword override.
	if st.lastClassification.Severity != "incident" {
		t.Errorf("expected severity=incident after keyword override, got %q", st.lastClassification.Severity)
	}
	// Plain priority must be set to urgent.
	if plainMCP.setPriorityCalls == 0 {
		t.Error("expected SetPriority to be called for incident")
	}
	if len(plainMCP.setPriorityArgs) == 0 || plainMCP.setPriorityArgs[0] != "urgent" {
		t.Errorf("expected priority=urgent, got %v", plainMCP.setPriorityArgs)
	}
	// SMS callback must have been invoked.
	if smsCalled == 0 {
		t.Error("expected SMS callback to be invoked for incident event")
	}
}

// ---------------------------------------------------------------------------
// AC-4: Non-code-fix path — category=general → no Linear issue created;
// Plain labels + draft still applied.
// ---------------------------------------------------------------------------

func TestClassifierWorker_GeneralCategory_NoLinearIssue(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{
		responses: []string{generalLLMResponse(), "Thank you for reaching out. How can we help?"},
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-300",
		PlainThreadID: "th_general_001",
	}
	runSingle(t, worker, event)

	// No Linear issue should be created for reply_only.
	if linearMCP.createIssueCalls != 0 {
		t.Errorf("expected 0 CreateIssue calls for general/reply_only, got %d", linearMCP.createIssueCalls)
	}
	// But Plain labels and draft note must still be applied.
	if plainMCP.addLabelsCalls != 1 {
		t.Errorf("expected 1 AddLabels call, got %d", plainMCP.addLabelsCalls)
	}
	if plainMCP.addNoteCalls != 1 {
		t.Errorf("expected 1 AddNote call (draft), got %d", plainMCP.addNoteCalls)
	}
	// Classification must be persisted.
	if st.classifyCalls != 1 {
		t.Errorf("expected 1 AttachClassification call, got %d", st.classifyCalls)
	}
	if st.lastClassification.Category != "general" {
		t.Errorf("expected category=general, got %q", st.lastClassification.Category)
	}
}

// ---------------------------------------------------------------------------
// AC-5: Classifier latency p95 < 60s — verify the worker completes well
// within the budget using instant mock responses.
// ---------------------------------------------------------------------------

func TestClassifierWorker_Latency(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{Identifier: "XOL-101", URL: "https://linear.app/xolto/issue/XOL-101"},
	}
	llm := &mockLLMClient{
		responses: []string{pricingLLMResponse(), "Pricing draft reply."},
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-latency",
		PlainThreadID: "th_latency_001",
	}

	ch := make(chan store.SupportEvent, 1)
	ch <- event
	close(ch)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startTS := time.Now()
	worker.Start(ctx, ch, 1)
	worker.Wait()
	elapsed := time.Since(startTS)

	if elapsed >= 60*time.Second {
		t.Errorf("classifier p95 latency exceeded 60s: took %v", elapsed)
	}
	if st.classifyCalls == 0 {
		t.Error("classification was never persisted within 55s budget")
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestClassifierWorker_LLMBadJSON — bad JSON from LLM falls back to default
// classification without crashing.
func TestClassifierWorker_LLMBadJSON_FallsBack(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{
		responses: []string{"Sorry, I cannot classify this.", "Draft reply."},
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-badjson",
		PlainThreadID: "th_badjson_001",
	}
	runSingle(t, worker, event)

	// Should still persist a fallback classification.
	if st.classifyCalls != 1 {
		t.Errorf("expected 1 AttachClassification call even on LLM bad JSON, got %d", st.classifyCalls)
	}
}

// TestClassifierWorker_GracefulShutdown — worker stops on ctx cancel.
func TestClassifierWorker_GracefulShutdown(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	ch := make(chan store.SupportEvent) // never sends
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx, ch, 2)
	cancel() // should cause workers to exit cleanly; no panic expected
	worker.Wait()
}

// TestClassifierWorker_SMSCallback_NotCalledForNonIncident verifies the
// SMS callback is NOT invoked when severity is below incident.
func TestClassifierWorker_SMSCallback_NotCalledForNonIncident(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{
		responses: []string{
			`{"category":"feature","market":"olx_bg","product_cat":"other","severity":"low","action_needed":"roadmap_candidate"}`,
			"Feature request noted.",
		},
	}

	smsCalled := 0
	smsCallback := func(_ context.Context, _ store.SupportEvent) error {
		smsCalled++
		return nil
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, smsCallback)

	event := store.SupportEvent{
		ID:            "evt-feature",
		PlainThreadID: "th_feature_001",
	}
	runSingle(t, worker, event)

	if smsCalled != 0 {
		t.Errorf("expected 0 SMS callback calls for low severity, got %d", smsCalled)
	}
}

// TestClassifierWorker_LLMError_ReturnsError — LLM returning an error causes
// processEvent to fail; store should not be called.
func TestClassifierWorker_LLMError_DoesNotPersist(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{
		err: errors.New("openai-compat: HTTP 500"),
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-llmerror",
		PlainThreadID: "th_llmerror_001",
	}
	runSingle(t, worker, event)

	// Wait a bit; store should not have been called.
	time.Sleep(80 * time.Millisecond)
	if st.classifyCalls != 0 {
		t.Errorf("expected 0 AttachClassification calls on LLM error, got %d", st.classifyCalls)
	}
}

// ---------------------------------------------------------------------------
// XOL-59 SUP-8: OpenAI-compatible request-shape guard tests
// ---------------------------------------------------------------------------

// openAIChatRequest mirrors the request body the classifier should send to
// /chat/completions. Used in the shape-guard test below.
type openAIChatRequest struct {
	Model          string             `json:"model"`
	Temperature    float64            `json:"temperature"`
	Messages       []map[string]any   `json:"messages"`
	ResponseFormat map[string]string  `json:"response_format"`
	MaxTokens      int                `json:"max_tokens"`
}

// TestClassifierRequestShape_OpenAICompatible asserts the classifier POSTs to
// /chat/completions with the correct model, response_format.type=="json_object",
// and temperature==0 for the classification call (XOL-59 SUP-8 AC).
func TestClassifierRequestShape_OpenAICompatible(t *testing.T) {
	// openAI-compatible stub response for the classifier call (first call).
	classifyResp := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]any{
					"content": `{"category":"general","market":"unknown","product_cat":"other","severity":"low","action_needed":"reply_only"}`,
				},
			},
		},
	}
	// Draft response (second call).
	draftResp := map[string]any{
		"choices": []map[string]any{
			{
				"message": map[string]any{
					"content": "Thank you for reaching out.",
				},
			},
		},
	}

	callCount := 0
	var capturedClassify openAIChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")

		// Only capture the first (classify) call for shape assertions.
		if callCount == 1 {
			_ = json.Unmarshal(raw, &capturedClassify)
			_ = json.NewEncoder(w).Encode(classifyResp)
			return
		}
		_ = json.NewEncoder(w).Encode(draftResp)
	}))
	defer srv.Close()

	// Build a real LLMClient pointing at the test server.
	llmClient := support.NewOpenAICompatClientWithHTTP("test-key", srv.URL, srv.Client())

	st := &mockStore{}
	plainMCP := &mockPlainAPI{}
	linearMCP := &mockLinearMCP{}

	cfg := support.ClassifierConfig{
		Store:     st,
		PlainAPI:  plainMCP,
		LinearMCP: linearMCP,
		LLM:       llmClient,
		LLMModel:  "gpt-5-nano",
		AppEnv:    "test",
	}
	worker := support.NewClassifierWorker(cfg)

	event := store.SupportEvent{
		ID:            "evt-shape",
		PlainThreadID: "th_shape_001",
	}
	runSingle(t, worker, event)

	// Assert endpoint: server only handles /chat/completions implicitly via
	// the srv URL; we assert the request model, response_format, and temperature.
	if capturedClassify.Model != "gpt-5-nano" {
		t.Errorf("expected model=gpt-5-nano, got %q", capturedClassify.Model)
	}
	if capturedClassify.Temperature != 0 {
		t.Errorf("expected temperature=0, got %v", capturedClassify.Temperature)
	}
	if capturedClassify.ResponseFormat["type"] != "json_object" {
		t.Errorf("expected response_format.type=json_object, got %q", capturedClassify.ResponseFormat["type"])
	}
	// Verify at least a system and user message are present.
	if len(capturedClassify.Messages) < 2 {
		t.Errorf("expected at least 2 messages (system+user), got %d", len(capturedClassify.Messages))
	}
}

// ---------------------------------------------------------------------------
// SUP-10 / SUP-60: getThread 401 error degrades gracefully
// ---------------------------------------------------------------------------

// TestClassifierWorker_GetThread401_DegradeGracefully verifies that when
// plain.Client.GetThread returns a 401 error (e.g. bad PLAIN_API_KEY), the
// classifier degrades gracefully: it does not panic, it classifies using the
// thread-ID fallback body, and it persists a classification.
// MCP-specific MCPCallError assertions are removed (MCP retired in SUP-10).
func TestClassifierWorker_GetThread401_DegradeGracefully(t *testing.T) {
	// Build a real httptest 401 server to produce a genuine error from the
	// GraphQL client.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	realClient := plain.New("bad-api-key")
	realClient.Endpoint = srv.URL
	realClient.HTTPClient = srv.Client()

	// Verify that GetThread on a 401 server returns an error.
	_, err := realClient.GetThread(context.Background(), "th_check")
	if err == nil {
		t.Fatal("expected 401 GetThread to return an error")
	}

	// Run the classifier with a mock that returns this error.
	// The worker must degrade gracefully and still persist a classification.
	capturedErr := err
	st := &mockStore{}
	plainAPI := &mockPlainAPI{
		getThreadFn: func(_ context.Context, _ string) (plain.ThreadInfo, error) {
			return plain.ThreadInfo{}, capturedErr
		},
	}
	linearMCP := &mockLinearMCP{}
	llm := &mockLLMClient{
		responses: []string{generalLLMResponse(), "Thank you for reaching out."},
	}
	worker := buildWorker(st, plainAPI, linearMCP, llm, nil)

	event := store.SupportEvent{
		ID:            "evt-gql401",
		PlainThreadID: "th_gql401_001",
	}
	runSingle(t, worker, event)

	// Classifier must have degraded gracefully and still persisted.
	if st.classifyCalls != 1 {
		t.Errorf("expected 1 AttachClassification call after getThread 401 error, got %d", st.classifyCalls)
	}
}

// TestClassifierIncidentKeywordOverride_WinsOverLLM verifies that the
// incident-keyword hard-floor override forces severity=incident even when the
// LLM returns a lower severity (e.g. "low"). The keyword check is run BEFORE
// trusting the LLM output, so safety does not depend on LLM quality.
func TestClassifierIncidentKeywordOverride_WinsOverLLM(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainAPI{
		getThreadFn: func(_ context.Context, threadID string) (plain.ThreadInfo, error) {
			// Body contains "can't log in" — a known incident keyword.
			return plain.ThreadInfo{
				ThreadID:      threadID,
				Subject:       "Access issue",
				CustomerEmail: "buyer@example.com",
				Body:          "Hi, I can't log in to my account today.",
			}, nil
		},
	}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{Identifier: "XOL-200", URL: "https://linear.app/xolto/issue/XOL-200"},
	}
	// LLM deliberately returns severity=low — keyword override must win.
	llm := &mockLLMClient{
		responses: []string{
			`{"category":"login","market":"olx_bg","product_cat":"other","severity":"low","action_needed":"billing_auth_fix"}`,
			"We are sorry you cannot log in. Our team is on it.",
		},
	}

	smsCalled := 0
	smsCallback := func(_ context.Context, event store.SupportEvent) error {
		smsCalled++
		return nil
	}

	worker := buildWorker(st, plainMCP, linearMCP, llm, smsCallback)

	event := store.SupportEvent{
		ID:            "evt-kwoverride",
		PlainThreadID: "th_kwoverride_001",
	}
	runSingle(t, worker, event)

	// Keyword override must have set severity=incident, regardless of LLM output.
	if st.lastClassification.Severity != "incident" {
		t.Errorf("expected severity=incident (keyword override), got %q", st.lastClassification.Severity)
	}
	// Priority must be bumped to urgent.
	if plainMCP.setPriorityCalls == 0 {
		t.Error("expected SetPriority to be called for incident keyword override")
	}
	if len(plainMCP.setPriorityArgs) == 0 || plainMCP.setPriorityArgs[0] != "urgent" {
		t.Errorf("expected priority=urgent, got %v", plainMCP.setPriorityArgs)
	}
	// SMS must fire.
	if smsCalled == 0 {
		t.Error("expected SMS callback invoked when keyword override triggers incident")
	}
}
