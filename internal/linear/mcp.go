// Package linear provides a thin client for creating Linear issues via the
// Linear GraphQL API (LINEAR_API_KEY).
//
// The interface mirrors the MCP tool-name "save_issue" so callers can treat it
// as a MCP tool call. A hosted Linear MCP endpoint is not yet publicly
// available, so this Phase-1 implementation calls the Linear REST/GraphQL API
// directly while preserving the same interface contract (XOL-55 SUP-4).
//
// Sentinel errors allow callers to use errors.Is for precise error handling.
package linear

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

// ErrLinearMCPRateLimited is returned when the Linear API responds with 429.
// Callers use errors.Is to detect rate-limit conditions.
var ErrLinearMCPRateLimited = errors.New("linear/mcp: rate limited (429)")

// ErrLinearAPIError is returned on Linear GraphQL or HTTP errors that are not
// rate-limit related.
var ErrLinearAPIError = errors.New("linear/mcp: api error")

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// CreateIssueInput holds the parameters for creating a Linear issue.
type CreateIssueInput struct {
	// Title is the issue title.
	Title string
	// Description is the markdown body (includes thread URL + classification).
	Description string
	// TeamID is the Linear team to create the issue in. If empty the client
	// will attempt to look up the team from the team name field.
	TeamID string
	// TeamName is used for lookup when TeamID is not set.
	TeamName string
	// ProjectName is the Linear project the issue should belong to.
	ProjectName string
}

// CreateIssueResult is returned by CreateIssue.
type CreateIssueResult struct {
	// IssueID is the Linear internal UUID of the created issue.
	IssueID string
	// Identifier is the human-readable ID like "XOL-123".
	Identifier string
	// URL is the direct link to the issue in Linear.
	URL string
}

// ---------------------------------------------------------------------------
// MCPClient interface
// ---------------------------------------------------------------------------

// MCPClient is the interface the classifier depends on for Linear operations.
// Tests inject a mock; production uses LinearMCPClient.
type MCPClient interface {
	// CreateIssue creates a Linear issue and returns its identifier and URL.
	// This corresponds to the MCP tool name "save_issue".
	CreateIssue(ctx context.Context, input CreateIssueInput) (CreateIssueResult, error)
}

// ---------------------------------------------------------------------------
// LinearMCPClient — wire implementation
// ---------------------------------------------------------------------------

const linearGraphQLEndpoint = "https://api.linear.app/graphql"

// LinearMCPClient calls the Linear GraphQL API using the LINEAR_API_KEY.
type LinearMCPClient struct {
	// APIKey is the Linear API key (LINEAR_API_KEY env var).
	APIKey string
	// Endpoint overrides the default GraphQL URL (used in tests).
	Endpoint string
	// HTTPClient is injected in tests; nil uses a default with 30s timeout.
	HTTPClient *http.Client
}

// NewLinearMCPClient returns a production LinearMCPClient.
func NewLinearMCPClient(apiKey string) *LinearMCPClient {
	return &LinearMCPClient{
		APIKey:   apiKey,
		Endpoint: linearGraphQLEndpoint,
	}
}

func (c *LinearMCPClient) endpoint() string {
	if strings.TrimSpace(c.Endpoint) != "" {
		return c.Endpoint
	}
	return linearGraphQLEndpoint
}

func (c *LinearMCPClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ---------------------------------------------------------------------------
// GraphQL wire types
// ---------------------------------------------------------------------------

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

// doGraphQL executes a GraphQL request and unmarshals the response into out.
func (c *LinearMCPClient) doGraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	reqBody := gqlRequest{Query: query, Variables: variables}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("linear/mcp: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("linear/mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLinearAPIError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("linear/mcp: read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s", ErrLinearMCPRateLimited, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: HTTP %d: %s", ErrLinearAPIError, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Unmarshal into a generic structure so we can surface top-level errors.
	var wrapper struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return fmt.Errorf("linear/mcp: unmarshal wrapper: %w", err)
	}
	if len(wrapper.Errors) > 0 {
		msgs := make([]string, 0, len(wrapper.Errors))
		for _, e := range wrapper.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("%w: %s", ErrLinearAPIError, strings.Join(msgs, "; "))
	}

	if out != nil {
		if err := json.Unmarshal(wrapper.Data, out); err != nil {
			return fmt.Errorf("linear/mcp: unmarshal data: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// CreateIssue
// ---------------------------------------------------------------------------

// CreateIssue creates a Linear issue and returns its identifier and URL.
// This is the Phase-1 implementation of the MCP tool "save_issue".
//
// If TeamID is not provided the client performs a teams query to resolve the
// TeamID from TeamName. If no team is found, ErrLinearAPIError is returned.
func (c *LinearMCPClient) CreateIssue(ctx context.Context, input CreateIssueInput) (CreateIssueResult, error) {
	teamID := input.TeamID
	if teamID == "" {
		var err error
		teamID, err = c.resolveTeamID(ctx, input.TeamName)
		if err != nil {
			return CreateIssueResult{}, err
		}
	}

	query := `
mutation CreateIssue($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue {
      id
      identifier
      url
    }
  }
}`
	variables := map[string]any{
		"input": map[string]any{
			"title":       input.Title,
			"description": input.Description,
			"teamId":      teamID,
		},
	}

	var resp struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				URL        string `json:"url"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}

	if err := c.doGraphQL(ctx, query, variables, &resp); err != nil {
		return CreateIssueResult{}, err
	}
	if !resp.IssueCreate.Success {
		return CreateIssueResult{}, fmt.Errorf("%w: issueCreate returned success=false", ErrLinearAPIError)
	}

	return CreateIssueResult{
		IssueID:    resp.IssueCreate.Issue.ID,
		Identifier: resp.IssueCreate.Issue.Identifier,
		URL:        resp.IssueCreate.Issue.URL,
	}, nil
}

// resolveTeamID returns the Linear team ID for the given team name.
// If teamName is empty or no match is found, it returns the first available
// team ID, or ErrLinearAPIError if no teams exist.
func (c *LinearMCPClient) resolveTeamID(ctx context.Context, teamName string) (string, error) {
	query := `
query Teams {
  teams {
    nodes {
      id
      name
    }
  }
}`
	var resp struct {
		Teams struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := c.doGraphQL(ctx, query, nil, &resp); err != nil {
		return "", err
	}
	if len(resp.Teams.Nodes) == 0 {
		return "", fmt.Errorf("%w: no teams found in Linear workspace", ErrLinearAPIError)
	}
	// Exact match first.
	for _, team := range resp.Teams.Nodes {
		if strings.EqualFold(team.Name, teamName) {
			return team.ID, nil
		}
	}
	// Fall back to first team.
	return resp.Teams.Nodes[0].ID, nil
}
