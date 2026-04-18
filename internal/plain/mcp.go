// Package plain — mcp.go
//
// PlainMCPClient implements MCPClient against the Plain MCP endpoint
// (https://mcp.plain.com/mcp) using JSON-RPC 2.0 over HTTP POST.
//
// The interface is isolated here so classifier_test.go can inject a mock
// without touching the wire format (AC-2 / XOL-55).
//
// Tools used:
//   - getThread      — fetch thread + customer metadata
//   - addLabels      — attach classification labels
//   - addNote        — post draft reply as internal note
//   - setPriority    — bump priority to urgent on incident
package plain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrPlainMCPUnavailable is returned when the Plain MCP endpoint cannot be
// reached or returns a non-200 status. Callers use errors.Is to branch.
var ErrPlainMCPUnavailable = errors.New("plain/mcp: endpoint unavailable")

// ---------------------------------------------------------------------------
// MCPCallError — structured error exposing HTTP status + body snippet
// ---------------------------------------------------------------------------

// MCPCallError is returned by callTool (and therefore all callers of callTool)
// when the server responds with a non-2xx HTTP status. It exposes the status
// code and a sanitised body snippet so that callers (e.g. the classifier warn
// log) can surface diagnostics without logging the raw credential.
type MCPCallError struct {
	// StatusCode is the HTTP status code returned by the server.
	StatusCode int
	// BodySnippet is the first 256 characters of the response body, with
	// newlines replaced by spaces, for single-line log safety.
	BodySnippet string
	// Endpoint is the URL that was called.
	Endpoint string
}

func (e *MCPCallError) Error() string {
	return fmt.Sprintf("plain/mcp: HTTP %d: %s", e.StatusCode, e.BodySnippet)
}

// Unwrap returns ErrPlainMCPUnavailable so errors.Is(err, ErrPlainMCPUnavailable)
// still works for callers that do not inspect the status code.
func (e *MCPCallError) Unwrap() error { return ErrPlainMCPUnavailable }

// ---------------------------------------------------------------------------
// PreflightResult — diagnostic snapshot from Preflight
// ---------------------------------------------------------------------------

// PreflightResult holds the outcome of a single Preflight probe call.
type PreflightResult struct {
	// Configured is true when the token is non-empty after strings.TrimSpace.
	Configured bool
	// Endpoint is the URL that was called (empty when Configured is false).
	Endpoint string
	// TokenLen is len(strings.TrimSpace(token)) — length of the trimmed credential.
	TokenLen int
	// RawTokenLen is len(token) — includes any surrounding whitespace/newlines.
	// When RawTokenLen != TokenLen the value contains whitespace padding.
	RawTokenLen int
	// StatusCode is the HTTP status returned by the preflight probe.
	// Zero when no HTTP call was made (e.g. empty token).
	StatusCode int
	// BodySnippet is the first 256 characters of the response body, newlines
	// replaced with spaces, for single-line log safety.
	BodySnippet string
	// Err is any wire-level or application-level error from the probe.
	Err error
}

// ---------------------------------------------------------------------------
// Domain types returned by getThread
// ---------------------------------------------------------------------------

// ThreadInfo is the normalised view of a Plain thread returned by GetThread.
type ThreadInfo struct {
	// ThreadID is the Plain thread identifier (e.g. "th_01ABC").
	ThreadID string
	// CustomerEmail is the email of the customer who opened the thread.
	CustomerEmail string
	// CustomerName is the display name of the customer (may be empty).
	CustomerName string
	// Subject is the thread title / subject line.
	Subject string
	// Body is the plain-text concatenation of the thread's message components.
	Body string
}

// ---------------------------------------------------------------------------
// MCPClient interface
// ---------------------------------------------------------------------------

// MCPClient is the interface the classifier depends on for Plain MCP calls.
// Tests inject a mock; production uses PlainMCPClient.
type MCPClient interface {
	// GetThread fetches the thread body, subject, and customer metadata.
	GetThread(ctx context.Context, threadID string) (ThreadInfo, error)
	// AddLabels attaches label type IDs to the thread.
	AddLabels(ctx context.Context, threadID string, labelTypeIDs []string) error
	// AddNote posts an internal note (draft reply) on the thread.
	AddNote(ctx context.Context, threadID, body string) error
	// SetPriority sets the thread priority. Pass "urgent" for incidents.
	SetPriority(ctx context.Context, threadID, priority string) error
}

// ---------------------------------------------------------------------------
// PlainMCPClient — wire implementation
// ---------------------------------------------------------------------------

const defaultMCPEndpoint = "https://mcp.plain.com/mcp"

// PlainMCPClient calls the Plain MCP endpoint using JSON-RPC 2.0 over HTTP.
type PlainMCPClient struct {
	// Token is the MCP bearer token (PLAIN_MCP_TOKEN env var).
	Token string
	// Endpoint overrides the default MCP URL (used in tests).
	Endpoint string
	// HTTPClient is injected in tests; nil uses a default with 30s timeout.
	HTTPClient *http.Client
}

// NewPlainMCPClient returns a production PlainMCPClient.
func NewPlainMCPClient(token string) *PlainMCPClient {
	return &PlainMCPClient{
		Token:    token,
		Endpoint: defaultMCPEndpoint,
	}
}

func (c *PlainMCPClient) endpoint() string {
	if strings.TrimSpace(c.Endpoint) != "" {
		return c.Endpoint
	}
	return defaultMCPEndpoint
}

func (c *PlainMCPClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 wire types
// ---------------------------------------------------------------------------

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// bodySnippet returns the first 256 characters of body with newlines replaced
// by spaces, safe for single-line structured log fields.
func bodySnippet(b []byte) string {
	s := strings.ReplaceAll(string(b), "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 256 {
		s = s[:256]
	}
	return strings.TrimSpace(s)
}

// callMethod sends a raw JSON-RPC 2.0 request with the given method and params,
// and returns the raw result bytes. It is the shared transport used by both
// callTool and Preflight. On non-2xx HTTP status it returns *MCPCallError so
// callers can inspect status code and body snippet via errors.As.
func (c *PlainMCPClient) callMethod(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	reqBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("plain/mcp: marshal request: %w", err)
	}

	ep := c.endpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("plain/mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPlainMCPUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("plain/mcp: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &MCPCallError{
			StatusCode:  resp.StatusCode,
			BodySnippet: bodySnippet(body),
			Endpoint:    ep,
		}
	}

	var rpcResp mcpResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("plain/mcp: unmarshal response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("plain/mcp: RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// callTool invokes a single MCP tool and returns the raw result bytes.
// On non-2xx HTTP status, the error is *MCPCallError and callers can use
// errors.As to retrieve status code and body snippet.
func (c *PlainMCPClient) callTool(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	return c.callMethod(ctx, "tools/call", map[string]any{
		"name":      tool,
		"arguments": args,
	})
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// Preflight issues a low-cost probe against the MCP endpoint to verify that
// the configured token is accepted. It calls tools/list (an auth-only MCP
// standard method that requires no tool arguments).
//
// Preflight never panics and never crashes the caller — any error is captured
// in PreflightResult.Err. The caller is responsible for logging.
//
// The token value is never included in PreflightResult or any error message.
func (c *PlainMCPClient) Preflight(ctx context.Context) PreflightResult {
	trimmed := strings.TrimSpace(c.Token)
	result := PreflightResult{
		Configured:  trimmed != "",
		TokenLen:    len(trimmed),
		RawTokenLen: len(c.Token),
		Endpoint:    c.endpoint(),
	}

	if !result.Configured {
		// No token — skip HTTP call entirely.
		return result
	}

	_, err := c.callMethod(ctx, "tools/list", map[string]any{})
	if err != nil {
		result.Err = err
		var mcpErr *MCPCallError
		if errors.As(err, &mcpErr) {
			result.StatusCode = mcpErr.StatusCode
			result.BodySnippet = mcpErr.BodySnippet
		}
		return result
	}

	// Successful probe — 2xx (any 2xx we reach here due to callMethod logic).
	result.StatusCode = http.StatusOK
	return result
}

// ---------------------------------------------------------------------------
// GetThread
// ---------------------------------------------------------------------------

// GetThread fetches thread + customer metadata via the Plain MCP getThread tool.
func (c *PlainMCPClient) GetThread(ctx context.Context, threadID string) (ThreadInfo, error) {
	result, err := c.callTool(ctx, "getThread", map[string]any{"threadId": threadID})
	if err != nil {
		return ThreadInfo{}, err
	}

	// Plain MCP returns content as a slice of typed items.
	// We extract what we need from the flexible JSON structure.
	var raw struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Thread struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Customer struct {
				Email    string `json:"email"`
				FullName string `json:"fullName"`
			} `json:"customer"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		// Fallback: try to parse a simpler flat structure.
		return ThreadInfo{ThreadID: threadID}, nil
	}

	info := ThreadInfo{
		ThreadID:      raw.Thread.ID,
		CustomerEmail: raw.Thread.Customer.Email,
		CustomerName:  raw.Thread.Customer.FullName,
		Subject:       raw.Thread.Title,
	}
	if info.ThreadID == "" {
		info.ThreadID = threadID
	}

	// Collect text content from the content array.
	var bodyParts []string
	for _, item := range raw.Content {
		if item.Type == "text" && item.Text != "" {
			bodyParts = append(bodyParts, item.Text)
		}
	}
	info.Body = strings.Join(bodyParts, "\n")

	return info, nil
}

// ---------------------------------------------------------------------------
// AddLabels
// ---------------------------------------------------------------------------

// AddLabels attaches label type IDs to a Plain thread.
func (c *PlainMCPClient) AddLabels(ctx context.Context, threadID string, labelTypeIDs []string) error {
	_, err := c.callTool(ctx, "addLabels", map[string]any{
		"threadId":     threadID,
		"labelTypeIds": labelTypeIDs,
	})
	return err
}

// ---------------------------------------------------------------------------
// AddNote
// ---------------------------------------------------------------------------

// AddNote posts an internal note on a Plain thread.
func (c *PlainMCPClient) AddNote(ctx context.Context, threadID, body string) error {
	_, err := c.callTool(ctx, "addNote", map[string]any{
		"threadId": threadID,
		"text":     body,
	})
	return err
}

// ---------------------------------------------------------------------------
// SetPriority
// ---------------------------------------------------------------------------

// SetPriority sets the priority of a Plain thread.
// priority should be "urgent", "high", "normal", or "low".
func (c *PlainMCPClient) SetPriority(ctx context.Context, threadID, priority string) error {
	_, err := c.callTool(ctx, "setPriority", map[string]any{
		"threadId": threadID,
		"priority": priority,
	})
	return err
}
