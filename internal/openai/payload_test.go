package openai_test

import (
	"testing"

	"github.com/TechXTT/xolto/internal/openai"
)

// TestBuildRequestPayload_GPT5Family verifies that gpt-5 family models yield
// max_completion_tokens and NO temperature field.
func TestBuildRequestPayload_GPT5Family(t *testing.T) {
	cases := []struct {
		model string
		mct   int // MaxCompletionTokens override (0 = use default)
		want  int
	}{
		{"gpt-5", 0, 2048},
		{"gpt-5-mini", 0, 2048},
		{"gpt-5-nano", 0, 2048},
		{"gpt-5-mini", 4096, 4096},
		{"gpt-5", 16000, 16000},
	}

	for _, tc := range cases {
		msgs := []map[string]any{{"role": "user", "content": "hi"}}
		payload := openai.BuildRequestPayload(openai.RequestOpts{
			Model:               tc.model,
			Messages:            msgs,
			Temperature:         0.5, // must be dropped for gpt-5 family
			MaxCompletionTokens: tc.mct,
		})

		if _, hasTemp := payload["temperature"]; hasTemp {
			t.Errorf("[%s] temperature must be absent for gpt-5 family, got %v", tc.model, payload["temperature"])
		}
		got, ok := payload["max_completion_tokens"].(int)
		if !ok {
			t.Errorf("[%s] max_completion_tokens missing or wrong type: %T %v", tc.model, payload["max_completion_tokens"], payload["max_completion_tokens"])
			continue
		}
		if got != tc.want {
			t.Errorf("[%s] max_completion_tokens = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// TestBuildRequestPayload_GPT4Family verifies that gpt-4 family models yield
// temperature and optional max_tokens.
func TestBuildRequestPayload_GPT4Family(t *testing.T) {
	cases := []struct {
		model     string
		temp      float64
		maxTokens int
	}{
		{"gpt-4o-mini", 0.2, 0},
		{"gpt-4o", 0.5, 1024},
		{"gpt-4", 0.7, 0},
	}

	for _, tc := range cases {
		msgs := []map[string]any{{"role": "user", "content": "hi"}}
		payload := openai.BuildRequestPayload(openai.RequestOpts{
			Model:       tc.model,
			Messages:    msgs,
			Temperature: tc.temp,
			MaxTokens:   tc.maxTokens,
		})

		got, hasTemp := payload["temperature"].(float64)
		if !hasTemp {
			t.Errorf("[%s] temperature must be present for gpt-4 family", tc.model)
		} else if got != tc.temp {
			t.Errorf("[%s] temperature = %v, want %v", tc.model, got, tc.temp)
		}

		if _, hasMCT := payload["max_completion_tokens"]; hasMCT {
			t.Errorf("[%s] max_completion_tokens must be absent for gpt-4 family", tc.model)
		}

		if tc.maxTokens > 0 {
			gotMT, hasMT := payload["max_tokens"].(int)
			if !hasMT {
				t.Errorf("[%s] max_tokens must be present when MaxTokens=%d", tc.model, tc.maxTokens)
			} else if gotMT != tc.maxTokens {
				t.Errorf("[%s] max_tokens = %d, want %d", tc.model, gotMT, tc.maxTokens)
			}
		} else {
			if _, hasMT := payload["max_tokens"]; hasMT {
				t.Errorf("[%s] max_tokens must be absent when MaxTokens=0", tc.model)
			}
		}
	}
}

// TestBuildRequestPayload_PassThrough verifies that ResponseFormat, Tools, and
// ToolChoice are passed through when set, and absent when nil/zero.
func TestBuildRequestPayload_PassThrough(t *testing.T) {
	msgs := []map[string]any{{"role": "user", "content": "hi"}}
	rf := map[string]any{"type": "json_object"}
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "foo"}}}

	payload := openai.BuildRequestPayload(openai.RequestOpts{
		Model:          "gpt-4o-mini",
		Messages:       msgs,
		Temperature:    0.5,
		ResponseFormat: rf,
		Tools:          tools,
		ToolChoice:     "auto",
	})

	if _, ok := payload["response_format"]; !ok {
		t.Error("response_format must be present when ResponseFormat is set")
	}
	if _, ok := payload["tools"]; !ok {
		t.Error("tools must be present when Tools is set")
	}
	if _, ok := payload["tool_choice"]; !ok {
		t.Error("tool_choice must be present when ToolChoice is set")
	}

	// Nil/zero values omitted.
	payloadEmpty := openai.BuildRequestPayload(openai.RequestOpts{
		Model:    "gpt-4o-mini",
		Messages: msgs,
	})
	if _, ok := payloadEmpty["response_format"]; ok {
		t.Error("response_format must be absent when ResponseFormat is nil")
	}
	if _, ok := payloadEmpty["tools"]; ok {
		t.Error("tools must be absent when Tools is nil")
	}
	if _, ok := payloadEmpty["tool_choice"]; ok {
		t.Error("tool_choice must be absent when ToolChoice is nil")
	}
}

// TestIsGPT5Family verifies the prefix-based detection.
func TestIsGPT5Family(t *testing.T) {
	trueCase := []string{"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-something-new"}
	for _, m := range trueCase {
		if !openai.IsGPT5Family(m) {
			t.Errorf("IsGPT5Family(%q) = false, want true", m)
		}
	}
	falseCase := []string{"gpt-4o-mini", "gpt-4o", "gpt-4", "gpt-3.5-turbo", "claude-3", ""}
	for _, m := range falseCase {
		if openai.IsGPT5Family(m) {
			t.Errorf("IsGPT5Family(%q) = true, want false", m)
		}
	}
}
