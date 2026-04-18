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

// callTool invokes a single MCP tool and returns the raw result bytes.
func (c *PlainMCPClient) callTool(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	reqBody := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      tool,
			"arguments": args,
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("plain/mcp: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(data))
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
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrPlainMCPUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rpcResp mcpResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("plain/mcp: unmarshal response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("plain/mcp: tool %q error %d: %s", tool, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
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
