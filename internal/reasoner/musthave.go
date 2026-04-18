// Package reasoner — musthave.go
//
// MustHaveEvaluatorLLM is the production implementation of
// scorer.MustHaveEvaluator. It calls the OpenAI-compatible Chat Completions
// endpoint once per listing with the "unknown" subset of must-haves and returns
// semantic verdicts ("met" | "missed" | "unknown") for each.
//
// Design principles (XOL-22):
//   - Cost-gated: only invoked when the tokenizer left at least one "unknown".
//   - Conservative: prefers "unknown" over "missed"; only emits "missed" on
//     clear affirmative evidence of absence or contradiction.
//   - BG/OLX-aware: system prompt explicitly instructs the model to handle
//     Bulgarian Cyrillic and English listing text equivalently.
//   - Fail-safe: any error returns an error; the caller (ScoreMustHavesSemantic)
//     falls back to the tokenizer result and never emits "missed" on failure.
//   - Caching: per-(listingID, missionID, promptVersion) to bound costs across
//     repeated /matches fetches. Uses the existing AI score cache store table
//     with a "musthave:" key prefix so no new schema is needed.
//   - Rate-limit: per-mission hourly ceiling via an internal sliding-window counter.
//
// Interface note: MustHaveEvaluatorLLM satisfies scorer.MustHaveEvaluator
// structurally (Go duck-typing). The interface lives in the scorer package;
// this package does NOT import scorer to avoid the scorer→reasoner→scorer
// import cycle. The flat Evaluate signature uses only stdlib types.
package reasoner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// mustHavePromptVersion is bumped when the system/user prompt changes.
// Cache keys include this version so a prompt change invalidates all entries.
const mustHavePromptVersion = 1

// mustHaveEvalCacheTTL is the time-to-live for cached must-have verdicts.
// Unused at runtime (the store uses promptVersion for invalidation), kept as
// documentation of the intended eviction policy.
const mustHaveEvalCacheTTL = 14 * 24 * time.Hour //nolint:unused

// ErrMustHaveRateLimited is returned when the per-mission hourly ceiling is
// exceeded. The caller (ScoreMustHavesSemantic) treats this as a fallback
// trigger and never emits "missed" as a result.
var ErrMustHaveRateLimited = errors.New("musthave evaluator: rate limited")

// MustHaveStore is the subset of the store used by MustHaveEvaluatorLLM for
// caching. It maps to the existing SetAIScoreCache / GetAIScoreCache pair.
type MustHaveStore interface {
	SetAIScoreCache(cacheKey string, score float64, reasoning string, promptVersion int) error
	GetAIScoreCache(cacheKey string, promptVersion int) (score float64, reasoning string, found bool, err error)
}

// MustHaveEvaluatorConfig holds all configuration for MustHaveEvaluatorLLM.
type MustHaveEvaluatorConfig struct {
	// APIKey is the OpenAI-compatible API key. When empty the evaluator is
	// disabled and NewMustHaveEvaluatorLLM returns nil.
	APIKey string
	// BaseURL is the OpenAI-compatible base URL (no trailing slash).
	BaseURL string
	// Model is the resolved model string (AI_MODEL_MUSTHAVE → AI_MODEL → default).
	Model string
	// MaxCallsPerMissionPerHour is the per-mission ceiling.
	MaxCallsPerMissionPerHour int
	// HTTPClient is optional; production uses a default 20s-timeout client.
	HTTPClient *http.Client
	// Store is used for caching verdicts. Nil disables caching.
	Store MustHaveStore
	// UsageCallback is called after each LLM call for usage tracking.
	UsageCallback UsageCallback
}

// MustHaveEvaluatorLLM implements scorer.MustHaveEvaluator against the
// OpenAI-compatible Chat Completions endpoint.
//
// It satisfies the scorer.MustHaveEvaluator interface structurally — the
// Evaluate method signature uses only stdlib types so this package does not
// need to import scorer (which would create a circular import).
type MustHaveEvaluatorLLM struct {
	cfg        MustHaveEvaluatorConfig
	httpClient *http.Client
	mu         sync.Mutex
	// per-mission call counts for the hourly ceiling (sliding window).
	missionCalls map[int64][]time.Time
	// nowFn is overridden in tests.
	nowFn func() time.Time
}

// NewMustHaveEvaluatorLLM constructs a MustHaveEvaluatorLLM from cfg.
// Returns nil when cfg.APIKey is empty (disabled degrade path).
func NewMustHaveEvaluatorLLM(cfg MustHaveEvaluatorConfig) *MustHaveEvaluatorLLM {
	if cfg.APIKey == "" {
		return nil
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-5-nano"
	}
	if cfg.MaxCallsPerMissionPerHour <= 0 {
		cfg.MaxCallsPerMissionPerHour = 200
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &MustHaveEvaluatorLLM{
		cfg:          cfg,
		httpClient:   hc,
		missionCalls: make(map[int64][]time.Time),
		nowFn:        time.Now,
	}
}

// Evaluate implements scorer.MustHaveEvaluator.
//
// It first checks the cache (if a store is configured). On a cache miss it
// calls the LLM, caches the result, and returns the verdicts.
// On any error, returns an error — the caller falls back to the tokenizer result
// and never emits "missed".
//
// Signature: Evaluate(ctx, listingID, missionID, userID, title, description, unknownMustHaves)
// This flat signature (no scorer-package types) avoids the scorer→reasoner→scorer
// import cycle.
func (e *MustHaveEvaluatorLLM) Evaluate(
	ctx context.Context,
	listingID string,
	missionID int64,
	userID string,
	title string,
	description string,
	unknownMustHaves []string,
) (map[string]string, error) {
	if len(unknownMustHaves) == 0 {
		return map[string]string{}, nil
	}

	// Check per-mission rate limit.
	if !e.allowMission(missionID) {
		return nil, ErrMustHaveRateLimited
	}

	// Check cache.
	if e.cfg.Store != nil {
		cacheKey := mustHaveCacheKey(listingID, missionID, mustHavePromptVersion)
		if cacheKey != "" {
			_, rawJSON, found, cacheErr := e.cfg.Store.GetAIScoreCache(cacheKey, mustHavePromptVersion)
			if cacheErr == nil && found {
				verdicts, ok := decodeMustHaveCache(rawJSON)
				if ok {
					return verdicts, nil
				}
			}
		}
	}

	// Call the LLM.
	verdicts, err := e.callLLM(ctx, listingID, missionID, userID, title, description, unknownMustHaves)
	if err != nil {
		return nil, err
	}

	// Persist cache.
	if e.cfg.Store != nil {
		cacheKey := mustHaveCacheKey(listingID, missionID, mustHavePromptVersion)
		if cacheKey != "" {
			encoded, encErr := encodeMustHaveCache(verdicts)
			if encErr == nil {
				// Best-effort; cache write failure must not block the result.
				_ = e.cfg.Store.SetAIScoreCache(cacheKey, 0, encoded, mustHavePromptVersion)
			}
		}
	}

	return verdicts, nil
}

// allowMission checks and records a call for the per-mission hourly ceiling.
// Thread-safe.
func (e *MustHaveEvaluatorLLM) allowMission(missionID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFn()
	cutoff := now.Add(-time.Hour)

	calls := pruneCalls(e.missionCalls[missionID], cutoff)
	if len(calls) >= e.cfg.MaxCallsPerMissionPerHour {
		return false
	}
	e.missionCalls[missionID] = append(calls, now)
	return true
}

// callLLM posts to the OpenAI-compatible endpoint and parses the response.
func (e *MustHaveEvaluatorLLM) callLLM(
	ctx context.Context,
	listingID string,
	missionID int64,
	userID string,
	title string,
	description string,
	unknownMustHaves []string,
) (map[string]string, error) {
	systemPrompt := mustHaveSystemPrompt()
	userMsg := buildMustHaveUserPrompt(title, description, unknownMustHaves)

	// Strict json_schema response_format (mirrors the SUP-9 / scorer pattern).
	responseFormat := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "musthave_verdicts",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdicts": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text":   map[string]any{"type": "string"},
								"status": map[string]any{"type": "string", "enum": []string{"met", "missed", "unknown"}},
							},
							"required":             []string{"text", "status"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"verdicts"},
				"additionalProperties": false,
			},
		},
	}

	payload := map[string]any{
		"model":       e.cfg.Model,
		"temperature": 0,
		"max_tokens":  512,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
		"response_format": responseFormat,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("musthave evaluator: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(e.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("musthave evaluator: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)

	start := time.Now()
	resp, err := e.httpClient.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		e.reportUsage(userID, missionID, latencyMs, false, err.Error())
		return nil, fmt.Errorf("musthave evaluator: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("musthave evaluator: HTTP %d from LLM", resp.StatusCode)
		e.reportUsage(userID, missionID, latencyMs, false, msg)
		return nil, fmt.Errorf("%s", msg)
	}

	var completion chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		e.reportUsage(userID, missionID, latencyMs, false, err.Error())
		return nil, fmt.Errorf("musthave evaluator: decode response: %w", err)
	}
	e.reportUsage(userID, missionID, latencyMs, true, "")

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("musthave evaluator: no choices in response")
	}

	verdicts, err := parseMustHaveResponse(completion.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("musthave evaluator: parse verdicts: %w", err)
	}

	slog.InfoContext(ctx, "musthave verdicts evaluated",
		"op", "musthave.evaluator.complete",
		"listing_id", listingID,
		"mission_id", missionID,
		"model", e.cfg.Model,
		"musthave_count", len(unknownMustHaves),
		"latency_ms", latencyMs,
	)

	return verdicts, nil
}

func (e *MustHaveEvaluatorLLM) reportUsage(userID string, missionID int64, latencyMs int, success bool, errMsg string) {
	if e.cfg.UsageCallback != nil {
		e.cfg.UsageCallback(userID, missionID, "musthave_evaluator", e.cfg.Model, 0, 0, latencyMs, success, errMsg)
	}
}

// ---------------------------------------------------------------------------
// Prompt builders
// ---------------------------------------------------------------------------

func mustHaveSystemPrompt() string {
	return "You are evaluating used-electronics marketplace listings against buyer must-have requirements. " +
		"For each must-have, emit exactly one status:\n" +
		"  \"met\"     — the listing text positively confirms the must-have.\n" +
		"  \"missed\"  — the listing text explicitly contradicts the must-have OR explicitly states the must-have is absent.\n" +
		"  \"unknown\" — no signal either way.\n\n" +
		"Be conservative: prefer \"unknown\" over \"missed\". " +
		"Only emit \"missed\" when the listing clearly and explicitly contradicts or denies the requirement. " +
		"Listing text may be in Bulgarian (Cyrillic), English, or Dutch — evaluate all languages equivalently. " +
		"Bulgarian Cyrillic text is common on OLX.bg listings and must be evaluated with the same accuracy as English. " +
		"Reply with strict JSON only."
}

func buildMustHaveUserPrompt(title, description string, mustHaves []string) string {
	type promptListing struct {
		Title string `json:"title"`
		Desc  string `json:"desc,omitempty"`
	}
	type promptMustHave struct {
		N    int    `json:"n"`
		Text string `json:"text"`
	}

	listing := promptListing{
		Title: title,
		Desc:  trimMustHaveText(description, 400),
	}
	mhs := make([]promptMustHave, len(mustHaves))
	for i, mh := range mustHaves {
		mhs[i] = promptMustHave{N: i + 1, Text: mh}
	}

	input := struct {
		Listing   promptListing    `json:"listing"`
		MustHaves []promptMustHave `json:"must_haves"`
	}{
		Listing:   listing,
		MustHaves: mhs,
	}
	raw, _ := json.Marshal(input)
	return string(raw)
}

func trimMustHaveText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxChars <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars]))
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

type mustHaveVerdict struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

type mustHaveResponse struct {
	Verdicts []mustHaveVerdict `json:"verdicts"`
}

func parseMustHaveResponse(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	// Strip optional markdown code fences.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var resp mustHaveResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("JSON unmarshal: %w (raw=%q)", err, truncateMH(raw, 200))
	}

	out := make(map[string]string, len(resp.Verdicts))
	for _, v := range resp.Verdicts {
		switch v.Status {
		case "met", "missed", "unknown":
			out[v.Text] = v.Status
		default:
			// Unknown status value from LLM — map to "unknown" (safe default).
			out[v.Text] = "unknown"
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Caching helpers
// ---------------------------------------------------------------------------

// mustHaveCacheKey returns the store cache key for a (listingID, missionID,
// promptVersion) tuple. Uses the "musthave:" prefix to avoid collision with
// the scorer's AI score cache keys.
func mustHaveCacheKey(listingID string, missionID int64, promptVersion int) string {
	listingID = strings.TrimSpace(listingID)
	if listingID == "" {
		return ""
	}
	return fmt.Sprintf("musthave:%s:%d:%d", listingID, missionID, promptVersion)
}

// encodeMustHaveCache serialises verdicts to JSON for the cache store.
func encodeMustHaveCache(verdicts map[string]string) (string, error) {
	raw, err := json.Marshal(verdicts)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// decodeMustHaveCache deserialises verdicts from the cache store.
func decodeMustHaveCache(raw string) (map[string]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var verdicts map[string]string
	if err := json.Unmarshal([]byte(raw), &verdicts); err != nil {
		return nil, false
	}
	return verdicts, true
}

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

func truncateMH(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
