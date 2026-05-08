// Package openai provides helpers for building OpenAI-compatible
// chat-completions request payloads with model-aware temperature handling.
//
// gpt-5 reasoning models (gpt-5, gpt-5-mini, gpt-5-nano) reject temperature
// != 1 and consume hidden reasoning tokens before visible output. For those
// models the helper omits temperature and sets max_completion_tokens. For
// gpt-4 family models the configured temperature and optional max_tokens are
// included in the standard positions.
//
// This is the canonical payload-build path for all AI call sites in the
// codebase. XOL-141: 4th recurrence of the multi-path coverage gap; helper
// enforces the contract going forward.
package openai

import "strings"

const (
	// defaultMaxCompletionTokens is used when RequestOpts.MaxCompletionTokens
	// is zero and the model is a gpt-5 family member.
	defaultMaxCompletionTokens = 2048
)

// RequestOpts holds the parameters for a single chat-completions request.
// Zero-value fields are omitted from the payload.
type RequestOpts struct {
	// Model is required.
	Model string
	// Messages is required.
	Messages []map[string]any
	// Temperature is included in the payload for non-gpt-5 models only.
	// gpt-5 family rejects temperature != 1 so the field is dropped.
	Temperature float64
	// MaxCompletionTokens is used when the model is gpt-5 family.
	// Defaults to defaultMaxCompletionTokens when zero.
	MaxCompletionTokens int
	// MaxTokens is used for non-gpt-5 family models.
	// Omitted when zero.
	MaxTokens int
	// ResponseFormat is optional. Passed through verbatim.
	ResponseFormat map[string]any
	// Tools is optional. Passed through verbatim.
	Tools []map[string]any
	// ToolChoice is optional. Passed through verbatim.
	ToolChoice any
}

// BuildRequestPayload returns the OpenAI /chat/completions request body as a
// map[string]any with model-aware temperature handling.
//
// For gpt-5 family models:
//   - temperature is omitted (the API rejects values != 1)
//   - max_completion_tokens is set (provides reasoning budget)
//
// For all other models:
//   - temperature is included at the configured value
//   - max_tokens is included when MaxTokens > 0
func BuildRequestPayload(opts RequestOpts) map[string]any {
	payload := map[string]any{
		"model":    opts.Model,
		"messages": opts.Messages,
	}

	if IsGPT5Family(opts.Model) {
		mct := opts.MaxCompletionTokens
		if mct <= 0 {
			mct = defaultMaxCompletionTokens
		}
		payload["max_completion_tokens"] = mct
	} else {
		payload["temperature"] = opts.Temperature
		if opts.MaxTokens > 0 {
			payload["max_tokens"] = opts.MaxTokens
		}
	}

	if opts.ResponseFormat != nil {
		payload["response_format"] = opts.ResponseFormat
	}
	if len(opts.Tools) > 0 {
		payload["tools"] = opts.Tools
	}
	if opts.ToolChoice != nil {
		payload["tool_choice"] = opts.ToolChoice
	}

	return payload
}

// IsGPT5Family returns true when the model name indicates a gpt-5 reasoning
// model. Covers gpt-5, gpt-5-mini, gpt-5-nano, and any future gpt-5-* variants.
// Mirrors the detection logic used in internal/generator/generator.go (XOL-66).
func IsGPT5Family(model string) bool {
	return strings.HasPrefix(model, "gpt-5")
}
