// Package support — classifier.go
//
// ClassifierWorker consumes the SUP-2 webhook channel, calls an
// OpenAI-compatible LLM to classify the support thread, applies the SUP-3
// taxonomy, attaches Plain labels + Linear issue via the Plain GraphQL API,
// posts a draft note, and persists results (XOL-55 SUP-4, migrated to
// OpenAI-compat path by XOL-59 SUP-8, MCP retired by XOL-? SUP-10).
//
// Per-event flow (mirroring the spec):
//  1. GetThread from Plain GraphQL API (metadata + subject).
//  2. Build prompt + call LLM (model from AI_MODEL_CLASSIFIER, temperature 0).
//  3. Run taxonomy.Classify (incident-keyword override + enum validation).
//  4. Apply Plain labels via GraphQL addLabels.
//  5. If severity == incident: bump priority to urgent + fire SMS callback.
//  6. If action_needed maps to a Linear project: create Linear issue.
//  7. Generate draft reply via LLM; post as Plain note (not sent message).
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
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/linear"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/store"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrClassifierLLMTimeout is returned when the LLM API call exceeds the
// deadline. Callers use errors.Is to detect and log separately.
var ErrClassifierLLMTimeout = errors.New("support/classifier: LLM call timed out")

// ErrClassifierLLMBadJSON is returned when the LLM response cannot be parsed
// as a valid LLMClassification JSON object.
var ErrClassifierLLMBadJSON = errors.New("support/classifier: LLM returned invalid JSON")

// ---------------------------------------------------------------------------
// LLMClient interface
// ---------------------------------------------------------------------------

// LLMClient is the interface used to call the OpenAI-compatible LLM.
// Tests inject a mock; production uses the minimal HTTP implementation below.
type LLMClient interface {
	// Complete sends a prompt to the LLM and returns the text response.
	Complete(ctx context.Context, req LLMRequest) (string, error)
}

// LLMRequest holds the parameters for a single LLM chat-completions call.
type LLMRequest struct {
	// Model is the model to use (e.g., "gpt-5-nano").
	Model string
	// MaxTokens caps the response length.
	MaxTokens int
	// Temperature controls randomness (0.0–1.0). Classifier uses 0.
	Temperature float64
	// System is the system prompt (mapped to a "system" role message).
	System string
	// UserMessage is the user turn.
	UserMessage string
	// JSONMode instructs the model to emit a JSON object via
	// response_format: {"type":"json_object"}. Used for the classifier call.
	JSONMode bool
}

// ---------------------------------------------------------------------------
// Minimal OpenAI-compatible HTTP client
// ---------------------------------------------------------------------------

// openAICompatClient is the production implementation of LLMClient.
// It posts to <baseURL>/chat/completions using Bearer token auth — the same
// path used by the reasoner and generator (XOL-59 SUP-8).
type openAICompatClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAICompatClient returns the production LLMClient backed by the
// OpenAI-compatible Chat Completions API at baseURL.
// baseURL must not end with a trailing slash (e.g. "https://api.openai.com/v1").
func NewOpenAICompatClient(apiKey, baseURL string) LLMClient {
	return &openAICompatClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewOpenAICompatClientWithHTTP is used in tests to inject a custom http.Client.
func NewOpenAICompatClientWithHTTP(apiKey, baseURL string, httpClient *http.Client) LLMClient {
	return &openAICompatClient{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (c *openAICompatClient) Complete(ctx context.Context, req LLMRequest) (string, error) {
	// gpt-5 reasoning models (nano/mini/full) only support temperature=1 (default),
	// so we omit the field entirely and rely on response_format + json_schema for
	// determinism. Older chat-completions models use 1 by default too.
	body := map[string]any{
		"model": req.Model,
		"messages": []map[string]any{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.UserMessage},
		},
	}
	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}
	if req.JSONMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("openai-compat: marshal request: %w", err)
	}

	// W19-23 global AI-spend cap. Pre-spend gate; on cap-fire the support
	// classifier worker logs and skips this event (the SUP processEvent
	// loop already handles classifier errors gracefully).
	budget := aibudget.Global()
	if budget != nil {
		if allowed, _ := budget.Allow(ctx, "support.classifier", aibudget.EstimatedCostPerCallUSD); !allowed {
			return "", fmt.Errorf("openai-compat: global ai budget exhausted")
		}
	}

	endpoint := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return "", fmt.Errorf("openai-compat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: %w", ErrClassifierLLMTimeout, ctx.Err())
		}
		return "", fmt.Errorf("openai-compat: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return "", fmt.Errorf("openai-compat: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return "", fmt.Errorf("openai-compat: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return "", fmt.Errorf("openai-compat: unmarshal response: %w", err)
	}
	if budget != nil {
		actualCostUSD := float64(parsed.Usage.PromptTokens)*0.25/1_000_000 +
			float64(parsed.Usage.CompletionTokens)*2.00/1_000_000
		budget.Reconcile(actualCostUSD)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai-compat: no choices in response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ---------------------------------------------------------------------------
// plainSupportAPI — minimal Plain interface for the classifier
// ---------------------------------------------------------------------------

// plainSupportAPI is the interface the classifier depends on for Plain calls.
// Tests inject a mock; production uses plain.SupportAdapter.
// Method signatures are kept identical to the former plain.MCPClient so the
// classifier business logic (processEvent) needs no changes beyond field rename.
type plainSupportAPI interface {
	// GetThread fetches thread metadata (subject, customer) by thread ID.
	GetThread(ctx context.Context, threadID string) (plain.ThreadInfo, error)
	// AddLabels attaches label type IDs to the thread.
	AddLabels(ctx context.Context, threadID string, labelTypeIDs []string) error
	// AddNote posts an internal note (draft reply) on the thread.
	AddNote(ctx context.Context, threadID, body string) error
	// SetPriority sets the thread priority. Pass "urgent" for incidents.
	SetPriority(ctx context.Context, threadID, priority string) error
}

// ---------------------------------------------------------------------------
// ClassifierConfig
// ---------------------------------------------------------------------------

// ClassifierConfig holds all dependencies for the ClassifierWorker.
type ClassifierConfig struct {
	// Store is used to persist classification and linear issue.
	Store store.SupportEventStore
	// PlainAPI is the Plain GraphQL API client (addLabels, addNote, setPriority, getThread).
	// Production uses plain.NewSupportAdapter(plain.New(apiKey)).
	PlainAPI plainSupportAPI
	// LinearMCP is the Linear MCP client (createIssue).
	LinearMCP linear.MCPClient
	// LLM is the OpenAI-compatible LLM client (XOL-59 SUP-8).
	// The caller is responsible for injecting the resolved classifier model
	// via LLMModel; all call sites read through config, not hardcoded strings.
	LLM LLMClient
	// LLMModel is the model string passed to the LLM (e.g. "gpt-5-nano").
	// Resolved from AI_MODEL_CLASSIFIER → AI_MODEL → default by the caller.
	LLMModel string
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
	wg     sync.WaitGroup
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
		w.wg.Add(1)
		go w.runWorker(ctx, eventCh)
	}
	w.logger.Info(
		"classifier worker pool started",
		"op", "classifier.pool.start",
		"num_workers", numWorkers,
	)
}

// Wait blocks until every goroutine launched by Start has returned. Safe to
// call multiple times; tests use it to synchronise mock-state reads with the
// worker's completion.
func (w *ClassifierWorker) Wait() {
	w.wg.Wait()
}

// runWorker is the goroutine body — drains eventCh until closed or cancelled.
func (w *ClassifierWorker) runWorker(ctx context.Context, eventCh <-chan store.SupportEvent) {
	defer w.wg.Done()
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
	// Step 1: Load full thread from Plain GraphQL API.
	// -------------------------------------------------------------------------
	threadInfo, err := w.cfg.PlainAPI.GetThread(ctx, event.PlainThreadID)
	if err != nil {
		w.logger.Warn("classifier: getThread failed, proceeding with stored data",
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
	} else {
		// body_len lets operators tell from Railway logs whether SUP-11 body
		// extraction landed or whether the classifier is running on metadata
		// only. The body itself is never logged.
		w.logger.Info("classifier: getThread ok",
			"op", "classifier.get_thread",
			"plain_thread_id", event.PlainThreadID,
			"body_len", len(threadInfo.Body),
		)
	}

	body := threadInfo.Body
	subject := threadInfo.Subject
	customerEmail := threadInfo.CustomerEmail

	// -------------------------------------------------------------------------
	// Step 2: Build prompt + call LLM (OpenAI-compatible path, XOL-59 SUP-8).
	//
	// Incident-keyword hard-floor precedence note:
	// The keyword override in taxonomy.Classify() (Step 3 below) runs BEFORE
	// this LLM output is trusted for severity. Even if the LLM returns
	// severity="low", the keyword matcher will override it to "incident" when
	// the body/subject contains an incident keyword. The LLM call is therefore
	// on the non-safety path; latency/quality degrading to "normal priority" is
	// acceptable because the keyword floor remains active regardless.
	// -------------------------------------------------------------------------
	userMsg := BuildClassifierUserPrompt(body, subject, customerEmail)
	llmStart := time.Now()
	rawText, err := w.cfg.LLM.Complete(ctx, LLMRequest{
		Model: w.cfg.LLMModel,
		// gpt-5 reasoning models consume hidden reasoning tokens before
		// emitting the final output; a tight cap yields an empty response.
		// 2048 covers typical reasoning + the ~80-token JSON schema payload.
		MaxTokens:   2048,
		Temperature: 0,
		System:      ClassifierSystemPrompt(),
		UserMessage: userMsg,
		JSONMode:    true,
	})
	llmLatency := time.Since(llmStart)
	if err != nil {
		return fmt.Errorf("classifier: LLM call failed: %w", err)
	}

	w.logger.Info(
		"support_event_classified",
		"op", "support.classifier.openai",
		"plain_thread_id", event.PlainThreadID,
		"model", w.cfg.LLMModel,
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
		if err := w.cfg.PlainAPI.AddLabels(ctx, event.PlainThreadID, labelTypeIDs); err != nil {
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
		if err := w.cfg.PlainAPI.SetPriority(ctx, event.PlainThreadID, "urgent"); err != nil {
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
	draftText, err := w.cfg.LLM.Complete(ctx, LLMRequest{
		Model:       w.cfg.LLMModel,
		MaxTokens:   2048,
		Temperature: 0.3,
		System:      "You are a helpful, empathetic support agent for xolto.",
		UserMessage: draftPrompt,
	})
	if err != nil {
		w.logger.Warn(
			"classifier: draft generation failed (non-fatal)",
			"op", "classifier.draft.warn",
			"plain_thread_id", event.PlainThreadID,
			"model", w.cfg.LLMModel,
			"error", err,
		)
		draftText = "[Draft generation failed — human reply required]"
	}

	noteBody := buildNoteBody(draftText, classification, linearIssueURL)
	if err := w.cfg.PlainAPI.AddNote(ctx, event.PlainThreadID, noteBody); err != nil {
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
