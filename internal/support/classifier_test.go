package support_test

import (
	"context"
	"errors"
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

// mockAnthropicClient returns a fixed response string for every call.
type mockAnthropicClient struct {
	// responses is a queue; each call pops the front. If empty, the last
	// response is repeated.
	responses []string
	calls     int
	err       error
}

func (m *mockAnthropicClient) Complete(_ context.Context, _ support.AnthropicRequest) (string, error) {
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

// mockPlainMCP records calls and returns configurable errors.
type mockPlainMCP struct {
	getThreadFn   func(ctx context.Context, threadID string) (plain.ThreadInfo, error)
	addLabelsCalls int
	addNoteCalls   int
	setPriorityCalls int
	setPriorityArgs []string // captures priority values
	lastNoteBody   string
	addLabelsErr   error
	addNoteErr     error
	setPriorityErr error
}

func (m *mockPlainMCP) GetThread(ctx context.Context, threadID string) (plain.ThreadInfo, error) {
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
func (m *mockPlainMCP) AddLabels(_ context.Context, _ string, _ []string) error {
	m.addLabelsCalls++
	return m.addLabelsErr
}
func (m *mockPlainMCP) AddNote(_ context.Context, _ string, body string) error {
	m.addNoteCalls++
	m.lastNoteBody = body
	return m.addNoteErr
}
func (m *mockPlainMCP) SetPriority(_ context.Context, _ string, priority string) error {
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

func incidentLLMResponse() string {
	return `{"category":"login","market":"olx_bg","product_cat":"other","severity":"incident","action_needed":"billing_auth_fix"}`
}

// buildWorker constructs a ClassifierWorker with the given mocks.
func buildWorker(
	st *mockStore,
	plainMCP *mockPlainMCP,
	linearMCP *mockLinearMCP,
	llm *mockAnthropicClient,
	smsCallback func(ctx context.Context, event store.SupportEvent) error,
) *support.ClassifierWorker {
	cfg := support.ClassifierConfig{
		Store:       st,
		PlainMCP:    plainMCP,
		LinearMCP:   linearMCP,
		Anthropic:   llm,
		SMSCallback: smsCallback,
		AppEnv:      "test",
	}
	return support.NewClassifierWorker(cfg)
}

// runSingle sends one event through the worker and waits for it to be processed.
func runSingle(t *testing.T, worker *support.ClassifierWorker, event store.SupportEvent) {
	t.Helper()
	ch := make(chan store.SupportEvent, 1)
	ch <- event
	close(ch)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	worker.Start(ctx, ch, 1)

	// Wait until the channel is drained (worker closes) or timeout.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if len(ch) == 0 {
			// Give the worker a little time to finish processing.
			time.Sleep(50 * time.Millisecond)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// AC-2: Full per-event flow with mocked MCP clients
// ---------------------------------------------------------------------------

func TestClassifierWorker_FullFlow_PricingEvent(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{
			IssueID:    "uuid-1",
			Identifier: "XOL-99",
			URL:        "https://linear.app/xolto/issue/XOL-99",
		},
	}
	// First LLM call = classifier; second = draft note.
	llm := &mockAnthropicClient{
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
	plainMCP := &mockPlainMCP{
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
	llm := &mockAnthropicClient{
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
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{}
	llm := &mockAnthropicClient{
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
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{
		result: linear.CreateIssueResult{Identifier: "XOL-101", URL: "https://linear.app/xolto/issue/XOL-101"},
	}
	llm := &mockAnthropicClient{
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

	// Wait for processing to complete.
	deadline := time.Now().Add(55 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if st.classifyCalls >= 1 {
			break
		}
	}
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
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{}
	llm := &mockAnthropicClient{
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
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{}
	llm := &mockAnthropicClient{}

	worker := buildWorker(st, plainMCP, linearMCP, llm, nil)

	ch := make(chan store.SupportEvent) // never sends
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx, ch, 2)
	cancel() // should cause workers to exit cleanly; no panic expected
	time.Sleep(20 * time.Millisecond)
}

// TestClassifierWorker_SMSCallback_NotCalledForNonIncident verifies the
// SMS callback is NOT invoked when severity is below incident.
func TestClassifierWorker_SMSCallback_NotCalledForNonIncident(t *testing.T) {
	st := &mockStore{}
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{}
	llm := &mockAnthropicClient{
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
	plainMCP := &mockPlainMCP{}
	linearMCP := &mockLinearMCP{}
	llm := &mockAnthropicClient{
		err: errors.New("anthropic: HTTP 500"),
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
