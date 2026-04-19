package replycopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// LLMClassifier implements Classifier using an OpenAI-compatible chat
// completions endpoint. Follows the same pattern as internal/generator
// (post-XOL-66): map[string]any payload, no temperature field,
// max_completion_tokens=2048 (XOL-73).
type LLMClassifier struct {
	APIURL string
	APIKey string
	Model  string
	client *http.Client
}

// NewLLMClassifier constructs an LLMClassifier. apiURL should be the base URL
// (e.g. "https://api.openai.com/v1"); the /chat/completions path is appended
// automatically.
func NewLLMClassifier(apiURL, apiKey, model string) *LLMClassifier {
	return &LLMClassifier{
		APIURL: apiURL,
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// Classify sends the prompt to the LLM and parses a ClassifyResult from the
// response. On parse failure it returns an error; the caller applies a fallback.
func (c *LLMClassifier) Classify(ctx context.Context, prompt string) (ClassifyResult, error) {
	reqPayload := map[string]any{
		"model":                 c.Model,
		"max_completion_tokens": 2048,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}

	body, err := json.Marshal(reqPayload)
	if err != nil {
		return ClassifyResult{}, fmt.Errorf("replycopilot: marshal request: %w", err)
	}

	url := strings.TrimRight(c.APIURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ClassifyResult{}, fmt.Errorf("replycopilot: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return ClassifyResult{}, fmt.Errorf("replycopilot: call provider: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ClassifyResult{}, fmt.Errorf("replycopilot: provider status %d", resp.StatusCode)
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return ClassifyResult{}, fmt.Errorf("replycopilot: decode response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return ClassifyResult{}, fmt.Errorf("replycopilot: no choices in response")
	}

	var result ClassifyResult
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &result); err != nil {
		return ClassifyResult{}, fmt.Errorf("replycopilot: parse classify json: %w", err)
	}
	return result, nil
}
