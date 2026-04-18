// Package support — classifier.go
//
// ClassifierWorker consumes the SUP-2 webhook channel, calls Claude to
// classify the support thread, applies the SUP-3 taxonomy, attaches Plain
// labels + Linear issue via MCP clients, posts a draft note, and persists
// results (XOL-55 SUP-4).
//
// Per-event flow (mirroring the spec):
//  1. GetThread from Plain MCP (body + customer metadata).
//  2. Build prompt + call Claude (claude-opus-4-7, temperature 0.1).
//  3. Run taxonomy.Classify (incident-keyword override + enum validation).
//  4. Apply Plain labels via MCP addLabels.
//  5. If severity == incident: bump priority to urgent + fire SMS callback.
//  6. If action_needed maps to a Linear project: create Linear issue.
//  7. Generate draft reply via Claude; post as Plain note (not sent message).
//  8. Persist via AttachClassification + AttachLinearIssue.
//  9. Log structured events with latency.
package support

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/linear"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/store"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrClassifierLLMTimeout is returned when the Claude API call exceeds the
// deadline. Callers use errors.Is to detect and log separately.
var ErrClassifierLLMTimeout = errors.New("support/classifier: LLM call timed out")

// ErrClassifierLLMBadJSON is returned when Claude's response cannot be parsed
// as a valid LLMClassification JSON object.
var ErrClassifierLLMBadJSON = errors.New("support/classifier: LLM returned invalid JSON")

// ---------------------------------------------------------------------------
// AnthropicClient interface
// ---------------------------------------------------------------------------

// AnthropicClient is the interface used to call Claude. Tests inject a mock;
// production uses the minimal HTTP implementation in this file.
type AnthropicClient interface {
	// Complete sends a prompt to Claude and returns the text response.
	Complete(ctx context.Context, req AnthropicRequest) (string, error)
}

// AnthropicRequest holds the parameters for a single Claude call.
type AnthropicRequest struct {
	// Model is the Claude model to use (e.g., "claude-opus-4-7").
	Model string
	// MaxTokens caps the response length.
	MaxTokens int
	// Temperature controls randomness (0.0–1.0).
	Temperature float64
	// System is the system prompt.
	System string
	// UserMessage is the user turn.
	UserMessage string
}

// ---------------------------------------------------------------------------
// Minimal Anthropic HTTP client (no SDK required)
// ---------------------------------------------------------------------------

const anthropicMessagesEndpoint = "https://api.anthropic.com/v1/messages"
const anthropicVersion = "2023-06-01"

// httpAnthropicClient is the production implementation of AnthropicClient.
type httpAnthropicClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

// NewAnthropicClient returns the production AnthropicClient backed by the
// Anthropic Messages API. endpoint may be empty (uses the default URL).
func NewAnthropicClient(apiKey, endpoint string) AnthropicClient {
	ep := anthropicMessagesEndpoint
	if strings.TrimSpace(endpoint) != "" {
		ep = endpoint
	}
	return &httpAnthropicClient{
		apiKey:     apiKey,
		endpoint:   ep,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewAnthropicClientWithHTTP is used in tests to inject a custom http.Client.
func NewAnthropicClientWithHTTP(apiKey, endpoint string, httpClient *http.Client) AnthropicClient {
	ep := anthropicMessagesEndpoint
	if strings.TrimSpace(endpoint) != "" {
		ep = endpoint
	}
	return &httpAnthropicClient{
		apiKey:     apiKey,
		endpoint:   ep,
		httpClient: httpClient,
	}
}

func (c *httpAnthropicClient) Complete(ctx context.Context, req AnthropicRequest) (string, error) {
	body := map[string]any{
		"model":       req.Model,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
		"system":      req.System,
		"messages": []map[string]any{
			{"role": "user", "content": req.UserMessage},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: %w", ErrClassifierLLMTimeout, ctx.Err())
		}
		return "", fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("anthropic: unmarshal response: %w", err)
	}
	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic: no text block in response")
}

// ---------------------------------------------------------------------------
// ClassifierConfig
// ---------------------------------------------------------------------------

// ClassifierConfig holds all dependencies for the ClassifierWorker.
type ClassifierConfig struct {
	// Store is used to persist classification and linear issue.
	Store store.SupportEventStore
	// PlainMCP is the Plain MCP client (addLabels, addNote, setPriority, getThread).
	PlainMCP plain.MCPClient
	// LinearMCP is the Linear MCP client (createIssue).
	LinearMCP linear.MCPClient
	// Anthropic is the LLM client.
	Anthropic AnthropicClient
	// SMSCallback is invoked on severity=incident events. It is the
	// SMSEscalator.NotifyIncident method (injected as func to avoid import cycle).
	SMSCallback func(ctx context.Context, event store.SupportEvent) error
	// LinearTeamID is the default Linear team ID for issue creation.
	// Optional: if empty the client resolves team by name "Xolto".
	LinearTeamID string
	// AppEnv is used to determine dry-run behaviour.
	AppEnv string
	// Logger is the structured logger; nil uses slog.Default().
	Logger *slog.Logger
}

// ---------------------------------------------------------------------------
// ClassifierWorker
// ---------------------------------------------------------------------------

// ClassifierWorker runs a pool of goroutines that each consume from the
// provided SupportEvent channel and execute the per-event classification flow.
type ClassifierWorker struct {
	cfg    ClassifierConfig
	logger *slog.Logger
}

// NewClassifierWorker constructs a ClassifierWorker from the given config.
func NewClassifierWorker(cfg ClassifierConfig) *ClassifierWorker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ClassifierWorker{cfg: cfg, logger: logger}
}

// Start launches numWorkers goroutines that each drain eventCh until it is
// closed or ctx is cancelled. It returns immediately; goroutines run in the
// background. All goroutines respect ctx.Done() for graceful shutdown.
func (w *ClassifierWorker) Start(ctx context.Context, eventCh <-chan store.SupportEvent, numWorkers int) {
	if numWorkers <= 0 {
		numWorkers = 2
	}
	for i := 0; i < numWorkers; i++ {
		go w.runWorker(ctx, eventCh)
	}
	w.logger.Info(
		"classifier worker pool started",
		"op", "classifier.pool.start",
		"num_workers", numWorkers,
	)
}

// runWorker is the goroutine body — drains eventCh until closed or cancelled.
func (w *ClassifierWorker) runWorker(ctx context.Context, eventCh <-chan store.SupportEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			if err := w.processEvent(ctx, event); err != nil {
				w.logger.Error(
					"classifier: event processing failed",
					"op", "classifier.event.error",
					"event_id", event.ID,
					"plain_thread_id", event.PlainThreadID,
					"error", err,
				)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Per-event flow
// ---------------------------------------------------------------------------

// processEvent executes the full SUP-4 per-event flow.
func (w *ClassifierWorker) processEvent(ctx context.Context, event store.SupportEvent) error {
	start := time.Now()

	// -------------------------------------------------------------------------
	// Step 1: Load full thread from Plain MCP.
	// -------------------------------------------------------------------------
	threadInfo, err := w.cfg.PlainMCP.GetThread(ctx, event.PlainThreadID)
	if err != nil {
		w.logger.Warn(
			"classifier: getThread failed, proceeding with stored data",
			"op", "classifier.get_thread.warn",
			"plain_thread_id", event.PlainThreadID,
			"error", err,
		)
		// Degrade gracefully: use the thread ID as body fallback so we can
		// still classify with minimal context.
		threadInfo = plain.ThreadInfo{
			ThreadID: event.PlainThreadID,
			Body:     fmt.Sprintf("Thread ID: %s", event.PlainThreadID),
		}
	}

	body := threadInfo.Body
	subject := threadInfo.Subject
	customerEmail := threadInfo.CustomerEmail

	// -------------------------------------------------------------------------
	// Step 2: Build prompt + call Claude.
	// -------------------------------------------------------------------------
	userMsg := BuildClassifierUserPrompt(body, subject, customerEmail)
	llmStart := time.Now()
	rawText, err := w.cfg.Anthropic.Complete(ctx, AnthropicRequest{
		Model:       "claude-opus-4-7",
		MaxTokens:   256,
		Temperature: 0.1,
		System:      ClassifierSystemPrompt(),
		UserMessage: userMsg,
	})
	llmLatency := time.Since(llmStart)
	if err != nil {
		return fmt.Errorf("classifier: LLM call failed: %w", err)
	}

	w.logger.Info(
		"support_event_classified",
		"op", "classifier.llm.complete",
		"plain_thread_id", event.PlainThreadID,
		"llm_latency_ms", llmLatency.Milliseconds(),
	)

	// -------------------------------------------------------------------------
	// Step 3: Parse + validate LLM output via taxonomy.Classify.
	// -------------------------------------------------------------------------
	var llmResult LLMClassification
	if err := parseLLMJSON(rawText, &llmResult); err != nil {
		// Log the parse failure but do NOT abort — fall back to safe defaults
		// so the thread still receives labels and a draft note (human triage).
		w.logger.Warn(
			"classifier: LLM returned bad JSON, falling back to defaults",
			"op", "classifier.llm.bad_json",
			"plain_thread_id", event.PlainThreadID,
			"error", fmt.Errorf("%w: %w", ErrClassifierLLMBadJSON, err),
		)
	}

	classification, classifyErr := Classify(body, subject, llmResult)
	if classifyErr != nil {
		w.logger.Warn(
			"classifier: taxonomy validation failed, defaulting to general/unknown",
			"op", "classifier.taxonomy.warn",
			"plain_thread_id", event.PlainThreadID,
			"error", classifyErr,
		)
		// Fall back to safe defaults so we still label the thread.
		classification = fallbackClassification()
	}

	// -------------------------------------------------------------------------
	// Step 4: Apply Plain labels.
	// -------------------------------------------------------------------------
	labelTypeIDs := classificationToLabelTypeIDs(classification)
	if len(labelTypeIDs) > 0 {
		if err := w.cfg.PlainMCP.AddLabels(ctx, event.PlainThreadID, labelTypeIDs); err != nil {
			w.logger.Warn(
				"classifier: addLabels failed (non-fatal)",
				"op", "classifier.add_labels.warn",
				"plain_thread_id", event.PlainThreadID,
				"error", err,
			)
		}
	}

	// -------------------------------------------------------------------------
	// Step 5: Incident path — bump priority + fire SMS callback.
	// -------------------------------------------------------------------------
	if classification.Severity == SeverityIncident {
		if err := w.cfg.PlainMCP.SetPriority(ctx, event.PlainThreadID, "urgent"); err != nil {
			w.logger.Warn(
				"classifier: setPriority urgent failed (non-fatal)",
				"op", "classifier.set_priority.warn",
				"plain_thread_id", event.PlainThreadID,
				"error", err,
			)
		}

		if w.cfg.SMSCallback != nil {
			// Enrich event with classification before passing to SMS callback.
			enrichedEvent := event
			sev := string(classification.Severity)
			cat := string(classification.Category)
			mkt := string(classification.Market)
			enrichedEvent.Severity = &sev
			enrichedEvent.Category = &cat
			enrichedEvent.Market = &mkt

			if err := w.cfg.SMSCallback(ctx, enrichedEvent); err != nil {
				w.logger.Error(
					"classifier: SMS callback failed",
					"op", "classifier.sms_callback.error",
					"plain_thread_id", event.PlainThreadID,
					"error", err,
				)
			}
		}
	}

	// -------------------------------------------------------------------------
	// Step 6: Linear issue creation if action_needed maps to a project.
	// -------------------------------------------------------------------------
	var linearIssueURL string
	if classification.LinearProject != "" && w.cfg.LinearMCP != nil {
		threadURL := fmt.Sprintf("https://app.plain.com/threads/%s", event.PlainThreadID)
		issueTitle := fmt.Sprintf("[xolto support] %s — %s", classification.Category, subject)
		issueBody := buildLinearIssueBody(classification, threadURL, customerEmail, body)

		result, err := w.cfg.LinearMCP.CreateIssue(ctx, linear.CreateIssueInput{
			Title:       issueTitle,
			Description: issueBody,
			TeamID:      w.cfg.LinearTeamID,
			TeamName:    "Xolto",
			ProjectName: classification.LinearProject,
		})
		if err != nil {
			w.logger.Warn(
				"classifier: createLinearIssue failed (non-fatal)",
				"op", "classifier.linear.warn",
				"plain_thread_id", event.PlainThreadID,
				"project", classification.LinearProject,
				"error", err,
			)
		} else {
			linearIssueURL = result.URL

			// Persist Linear issue URL.
			if err := w.cfg.Store.AttachLinearIssue(ctx, event.PlainThreadID, result.URL); err != nil {
				w.logger.Warn(
					"classifier: AttachLinearIssue failed (non-fatal)",
					"op", "classifier.store.linear_issue.warn",
					"plain_thread_id", event.PlainThreadID,
					"error", err,
				)
			}

			w.logger.Info(
				"support_event_routed",
				"op", "classifier.linear.created",
				"plain_thread_id", event.PlainThreadID,
				"linear_issue", result.Identifier,
				"linear_url", result.URL,
				"project", classification.LinearProject,
			)
		}
	}

	// -------------------------------------------------------------------------
	// Step 7: Generate draft reply + post as Plain note.
	// -------------------------------------------------------------------------
	langHint := detectThreadLang(body + " " + subject)
	draftPrompt := BuildDraftNotePrompt(
		body, subject, customerEmail, langHint,
		string(classification.Category), string(classification.Action),
	)
	draftText, err := w.cfg.Anthropic.Complete(ctx, AnthropicRequest{
		Model:       "claude-opus-4-7",
		MaxTokens:   512,
		Temperature: 0.3,
		System:      "You are a helpful, empathetic support agent for xolto.",
		UserMessage: draftPrompt,
	})
	if err != nil {
		w.logger.Warn(
			"classifier: draft generation failed (non-fatal)",
			"op", "classifier.draft.warn",
			"plain_thread_id", event.PlainThreadID,
			"error", err,
		)
		draftText = "[Draft generation failed — human reply required]"
	}

	noteBody := buildNoteBody(draftText, classification, linearIssueURL)
	if err := w.cfg.PlainMCP.AddNote(ctx, event.PlainThreadID, noteBody); err != nil {
		w.logger.Warn(
			"classifier: addNote failed (non-fatal)",
			"op", "classifier.add_note.warn",
			"plain_thread_id", event.PlainThreadID,
			"error", err,
		)
	} else {
		w.logger.Info(
			"support_event_drafted",
			"op", "classifier.draft.posted",
			"plain_thread_id", event.PlainThreadID,
		)
	}

	// -------------------------------------------------------------------------
	// Step 8: Persist classification.
	// -------------------------------------------------------------------------
	now := time.Now().UTC()
	if err := w.cfg.Store.AttachClassification(ctx, event.PlainThreadID, store.Classification{
		ClassifiedAt: now,
		Category:     string(classification.Category),
		Market:       string(classification.Market),
		ProductCat:   string(classification.ProductCat),
		Severity:     string(classification.Severity),
		ActionNeeded: string(classification.Action),
	}); err != nil {
		w.logger.Error(
			"classifier: AttachClassification failed",
			"op", "classifier.store.classification.error",
			"plain_thread_id", event.PlainThreadID,
			"error", err,
		)
		return fmt.Errorf("classifier: AttachClassification: %w", err)
	}

	w.logger.Info(
		"support_event_classified",
		"op", "classifier.complete",
		"plain_thread_id", event.PlainThreadID,
		"category", classification.Category,
		"market", classification.Market,
		"severity", classification.Severity,
		"action", classification.Action,
		"linear_project", classification.LinearProject,
		"incident_override", classification.IncidentOverride,
		"total_latency_ms", time.Since(start).Milliseconds(),
	)

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseLLMJSON extracts the JSON object from Claude's text output.
// Claude is instructed to emit only JSON but may occasionally include
// surrounding whitespace or code fences.
func parseLLMJSON(raw string, out *LLMClassification) error {
	// Strip optional code-fence wrappers.
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// Extract the first '{...}' block.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return fmt.Errorf("no JSON object found in LLM output: %q", raw)
	}
	s = s[start : end+1]

	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("JSON unmarshal: %w: raw=%q", err, raw)
	}
	return nil
}

// fallbackClassification returns safe default values when taxonomy validation
// fails (e.g., LLM returned an invalid enum). Human triage will handle it.
func fallbackClassification() Classification {
	return Classification{
		Category:      CategoryGeneral,
		Market:        MarketUnknown,
		ProductCat:    ProductCatOther,
		Severity:      SeverityMedium,
		Action:        ActionReplyOnly,
		LinearProject: "",
	}
}

// classificationToLabelTypeIDs converts a Classification into Plain label
// type IDs using the xolto label naming convention. Plain labels are
// referenced by type ID; the IDs here follow the convention defined in the
// xolto Plain workspace.
//
// In Phase 1, label type IDs are derived from the enum name using the
// prefix "xolto-" + dimension + ":" + value (e.g., "xolto-category:pricing").
// The actual IDs in Plain must match these strings exactly.
func classificationToLabelTypeIDs(c Classification) []string {
	return []string{
		fmt.Sprintf("xolto-category:%s", c.Category),
		fmt.Sprintf("xolto-market:%s", c.Market),
		fmt.Sprintf("xolto-severity:%s", c.Severity),
	}
}

// buildLinearIssueBody produces the markdown body for the Linear issue,
// including the thread URL, classification summary, and user context.
func buildLinearIssueBody(c Classification, threadURL, customerEmail, threadBody string) string {
	var sb strings.Builder
	sb.WriteString("## Support thread\n\n")
	sb.WriteString(fmt.Sprintf("**Plain thread:** %s\n\n", threadURL))
	if customerEmail != "" {
		sb.WriteString(fmt.Sprintf("**Customer:** %s\n\n", customerEmail))
	}
	sb.WriteString("## Classification\n\n")
	sb.WriteString(fmt.Sprintf("- **Category:** %s\n", c.Category))
	sb.WriteString(fmt.Sprintf("- **Market:** %s\n", c.Market))
	sb.WriteString(fmt.Sprintf("- **Severity:** %s\n", c.Severity))
	sb.WriteString(fmt.Sprintf("- **Action needed:** %s\n", c.Action))
	if c.IncidentOverride {
		sb.WriteString("- **Incident override:** true (keyword match)\n")
	}
	sb.WriteString("\n## Thread excerpt\n\n")
	excerpt := threadBody
	if len(excerpt) > 500 {
		excerpt = excerpt[:500] + "…"
	}
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n", excerpt))
	return sb.String()
}

// buildNoteBody formats the internal Plain note that will be shown to the
// support agent as a draft reply suggestion.
func buildNoteBody(draftText string, c Classification, linearIssueURL string) string {
	var sb strings.Builder
	sb.WriteString("**[xolto classifier — draft reply for human review]**\n\n")
	sb.WriteString(fmt.Sprintf("Category: %s | Market: %s | Severity: %s | Action: %s\n", c.Category, c.Market, c.Severity, c.Action))
	if linearIssueURL != "" {
		sb.WriteString(fmt.Sprintf("Linear: %s\n", linearIssueURL))
	}
	sb.WriteString("\n---\n\n")
	sb.WriteString(draftText)
	sb.WriteString("\n\n---\n*Human approval required before sending.*")
	return sb.String()
}

// detectThreadLang returns a language hint ("bg", "nl", "en") from the thread
// text, using the same stop-word logic as internal/draftnote.detectLang but
// without importing that package (to avoid import cycles).
func detectThreadLang(text string) string {
	lower := strings.ToLower(text)
	// Bulgarian Cyrillic stop words.
	bgWords := []string{"на", "от", "за", "се", "не", "да", "продавам", "работи"}
	for _, w := range bgWords {
		if strings.Contains(lower, w) {
			return "bg"
		}
	}
	// Dutch stop words.
	nlWords := []string{"de", "het", "een", "van", "niet", "zijn", "voor"}
	for _, w := range nlWords {
		if strings.Contains(lower, " "+w+" ") {
			return "nl"
		}
	}
	return "en"
}
