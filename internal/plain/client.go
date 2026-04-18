// Package plain provides a minimal GraphQL client for the Plain.com API.
//
// Methods implemented:
//   - UpsertCustomer — ensures a customer record exists for an email address.
//   - CreateThread   — opens a new support thread under a customer.
//   - AddLabel       — attaches a label to a thread.
//   - AddNote        — posts an internal note on a thread.
//   - SetPriority    — updates the priority of a thread.
//   - GetThread      — fetches thread metadata (id, title, customer) by thread ID.
//   - Preflight      — probes the GraphQL endpoint to verify API key health at boot.
//
// Authentication uses the PLAIN_API_KEY environment variable. The HTTP client
// is injected via Client.HTTPClient so tests can substitute an httptest.Server.
//
// All methods call the Plain GraphQL endpoint at
// https://core-api.uk.plain.com/graphql/v1.
package plain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEndpoint = "https://core-api.uk.plain.com/graphql/v1"

// Client is a minimal Plain GraphQL client.
type Client struct {
	// APIKey is the Plain API key (PLAIN_API_KEY env var).
	APIKey string
	// Endpoint is the GraphQL URL. Defaults to the Plain production endpoint.
	Endpoint string
	// HTTPClient is the HTTP client used for requests. Defaults to a client
	// with a 15-second timeout when nil.
	HTTPClient *http.Client
}

// New creates a Client using the given API key.
func New(apiKey string) *Client {
	return &Client{
		APIKey:   apiKey,
		Endpoint: defaultEndpoint,
	}
}

// ---------------------------------------------------------------------------
// ThreadInfo — normalised view of a Plain thread
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
	// Body is the plain-text body of the thread.
	// Note: Plain's GraphQL API requires a separate paginated query
	// (threadTimelineEntries) to retrieve message content. To keep this
	// implementation within the ~60 LOC budget, Body is left empty here.
	// The classifier degrades gracefully when Body is empty.
	Body string
}

// ---------------------------------------------------------------------------
// PreflightResult — diagnostic snapshot from Preflight
// ---------------------------------------------------------------------------

// PreflightResult holds the outcome of a single Preflight probe call.
type PreflightResult struct {
	// Configured is true when the API key is non-empty after strings.TrimSpace.
	Configured bool
	// Endpoint is the URL that was called (empty when Configured is false).
	Endpoint string
	// KeyLen is len(strings.TrimSpace(apiKey)) — length of the trimmed credential.
	KeyLen int
	// RawKeyLen is len(apiKey) — includes any surrounding whitespace/newlines.
	// When RawKeyLen != KeyLen the value contains whitespace padding.
	RawKeyLen int
	// StatusCode is the HTTP status returned by the preflight probe.
	// Zero when no HTTP call was made (e.g. empty key).
	StatusCode int
	// BodySnippet is the first 256 characters of the response body, newlines
	// replaced with spaces, for single-line log safety.
	BodySnippet string
	// Err is any wire-level or application-level error from the probe.
	Err error
}

// bodySnippet returns the first 256 characters of b with newlines replaced
// by spaces, safe for single-line structured log fields.
func bodySnippet(b []byte) string {
	s := strings.ReplaceAll(string(b), "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 256 {
		s = s[:256]
	}
	return strings.TrimSpace(s)
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// Preflight probes the GraphQL endpoint with a cheap auth-gated query to
// verify the configured API key is accepted. It uses { __typename } which
// requires valid authentication and returns in a single round-trip.
//
// Preflight never panics and never crashes the caller — any error is captured
// in PreflightResult.Err. The caller is responsible for logging.
//
// The API key value is never included in PreflightResult or any error message.
func (c *Client) Preflight(ctx context.Context) PreflightResult {
	trimmed := strings.TrimSpace(c.APIKey)
	result := PreflightResult{
		Configured: trimmed != "",
		KeyLen:     len(trimmed),
		RawKeyLen:  len(c.APIKey),
		Endpoint:   c.endpoint(),
	}

	if !result.Configured {
		// No key — skip HTTP call entirely.
		return result
	}

	// Issue a minimal auth-gated query. { __typename } is the cheapest possible
	// introspection query; Plain's GraphQL endpoint returns 200 with
	// {"data":{"__typename":"Query"}} on success, or 401 on auth failure.
	query := `{ __typename }`
	reqBody, _ := json.Marshal(graphQLRequest{Query: query})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(reqBody))
	if err != nil {
		result.Err = fmt.Errorf("plain preflight: build request: %w", err)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		result.Err = fmt.Errorf("plain preflight: %w", err)
		return result
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(resp.Body)
	result.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.BodySnippet = bodySnippet(rawBody)
		result.Err = fmt.Errorf("plain preflight: HTTP %d", resp.StatusCode)
	}
	return result
}

// ---------------------------------------------------------------------------
// GetThread
// ---------------------------------------------------------------------------

// GetThread fetches thread metadata (id, title, customer) from Plain's
// GraphQL API. The Body field is left empty — retrieving message content
// requires a separate paginated threadTimelineEntries query which is
// outside the scope of this method (see ThreadInfo.Body doc comment).
func (c *Client) GetThread(ctx context.Context, threadID string) (ThreadInfo, error) {
	query := `
query GetThread($threadId: ID!) {
  thread(threadId: $threadId) {
    id
    title
    customer {
      fullName
      primaryEmailAddress {
        email
      }
    }
  }
}`
	variables := map[string]any{
		"threadId": threadID,
	}

	var resp struct {
		Data struct {
			Thread *struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Customer struct {
					FullName            string `json:"fullName"`
					PrimaryEmailAddress struct {
						Email string `json:"email"`
					} `json:"primaryEmailAddress"`
				} `json:"customer"`
			} `json:"thread"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return ThreadInfo{}, err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return ThreadInfo{}, err
	}
	if resp.Data.Thread == nil {
		return ThreadInfo{}, fmt.Errorf("plain getThread: thread %q not found", threadID)
	}

	t := resp.Data.Thread
	info := ThreadInfo{
		ThreadID:      t.ID,
		Subject:       t.Title,
		CustomerName:  t.Customer.FullName,
		CustomerEmail: t.Customer.PrimaryEmailAddress.Email,
	}
	if info.ThreadID == "" {
		info.ThreadID = threadID
	}
	return info, nil
}

// ---------------------------------------------------------------------------
// UpsertCustomer
// ---------------------------------------------------------------------------

// UpsertCustomerInput is the input for creating or finding a customer by email.
type UpsertCustomerInput struct {
	// Identifier holds the email address used to look up or create the customer.
	Email string
	// FullName is the optional display name.
	FullName string
}

// UpsertCustomerResult is returned by UpsertCustomer.
type UpsertCustomerResult struct {
	CustomerID string
}

// UpsertCustomer creates or finds the Plain customer for the given email.
func (c *Client) UpsertCustomer(ctx context.Context, input UpsertCustomerInput) (UpsertCustomerResult, error) {
	query := `
mutation UpsertCustomer($input: UpsertCustomerInput!) {
  upsertCustomer(input: $input) {
    result
    customer {
      id
    }
    error { message }
  }
}`
	onCreate := map[string]any{
		"email": map[string]any{
			"email":      input.Email,
			"isVerified": true,
		},
	}
	if strings.TrimSpace(input.FullName) != "" {
		onCreate["fullName"] = input.FullName
	}
	variables := map[string]any{
		"input": map[string]any{
			"identifier": map[string]any{
				"emailAddress": input.Email,
			},
			"onCreate": onCreate,
			"onUpdate": map[string]any{},
		},
	}

	var resp struct {
		Data struct {
			UpsertCustomer struct {
				Customer struct {
					ID string `json:"id"`
				} `json:"customer"`
				Error *graphQLMutationError `json:"error"`
			} `json:"upsertCustomer"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return UpsertCustomerResult{}, err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return UpsertCustomerResult{}, err
	}
	if resp.Data.UpsertCustomer.Error != nil {
		return UpsertCustomerResult{}, fmt.Errorf("plain upsertCustomer: %s", resp.Data.UpsertCustomer.Error.Message)
	}
	return UpsertCustomerResult{CustomerID: resp.Data.UpsertCustomer.Customer.ID}, nil
}

// ---------------------------------------------------------------------------
// CreateThread
// ---------------------------------------------------------------------------

// CreateThreadInput is the input for opening a new support thread.
type CreateThreadInput struct {
	// CustomerID is the Plain customer ID returned by UpsertCustomer.
	CustomerID string
	// Subject is the thread title.
	Subject string
	// Body is the initial message markdown body.
	Body string
}

// CreateThreadResult is returned by CreateThread.
type CreateThreadResult struct {
	ThreadID string
}

// CreateThread opens a new support thread in Plain under the given customer.
func (c *Client) CreateThread(ctx context.Context, input CreateThreadInput) (CreateThreadResult, error) {
	query := `
mutation CreateThread($input: CreateThreadInput!) {
  createThread(input: $input) {
    thread {
      id
    }
    error { message }
  }
}`
	variables := map[string]any{
		"input": map[string]any{
			"customerIdentifier": map[string]any{
				"customerId": input.CustomerID,
			},
			"title": input.Subject,
			"components": []map[string]any{
				{
					"componentText": map[string]any{
						"text": input.Body,
					},
				},
			},
		},
	}

	var resp struct {
		Data struct {
			CreateThread struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
				Error *graphQLMutationError `json:"error"`
			} `json:"createThread"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return CreateThreadResult{}, err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return CreateThreadResult{}, err
	}
	if resp.Data.CreateThread.Error != nil {
		return CreateThreadResult{}, fmt.Errorf("plain createThread: %s", resp.Data.CreateThread.Error.Message)
	}
	return CreateThreadResult{ThreadID: resp.Data.CreateThread.Thread.ID}, nil
}

// ---------------------------------------------------------------------------
// AddLabel
// ---------------------------------------------------------------------------

// AddLabel attaches a label (by label type ID) to the given thread.
func (c *Client) AddLabel(ctx context.Context, threadID, labelTypeID string) error {
	query := `
mutation AddLabels($input: AddLabelsInput!) {
  addLabels(input: $input) {
    labels {
      id
    }
    error { message }
  }
}`
	variables := map[string]any{
		"input": map[string]any{
			"threadId": threadID,
			"labelTypeIds": []string{labelTypeID},
		},
	}

	var resp struct {
		Data struct {
			AddLabels struct {
				Error *graphQLMutationError `json:"error"`
			} `json:"addLabels"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return err
	}
	if resp.Data.AddLabels.Error != nil {
		return fmt.Errorf("plain addLabels: %s", resp.Data.AddLabels.Error.Message)
	}
	return nil
}

// ---------------------------------------------------------------------------
// AddNote
// ---------------------------------------------------------------------------

// AddNote posts an internal note on the given thread.
func (c *Client) AddNote(ctx context.Context, threadID, body string) error {
	query := `
mutation AddNote($input: CreateNoteInput!) {
  createNote(input: $input) {
    note {
      id
    }
    error { message }
  }
}`
	variables := map[string]any{
		"input": map[string]any{
			"threadId": threadID,
			"components": []map[string]any{
				{
					"componentText": map[string]any{
						"text": body,
					},
				},
			},
		},
	}

	var resp struct {
		Data struct {
			CreateNote struct {
				Error *graphQLMutationError `json:"error"`
			} `json:"createNote"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return err
	}
	if resp.Data.CreateNote.Error != nil {
		return fmt.Errorf("plain createNote: %s", resp.Data.CreateNote.Error.Message)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SetPriority
// ---------------------------------------------------------------------------

// Priority maps to Plain's thread priority values.
type Priority int

const (
	PriorityUrgent  Priority = 0
	PriorityHigh    Priority = 1
	PriorityNormal  Priority = 2
	PriorityLow     Priority = 3
)

// SetPriority updates the priority of the given thread.
func (c *Client) SetPriority(ctx context.Context, threadID string, priority Priority) error {
	query := `
mutation UpdateThreadPriority($input: UpdateThreadInput!) {
  updateThread(input: $input) {
    thread {
      id
    }
    error { message }
  }
}`
	variables := map[string]any{
		"input": map[string]any{
			"threadId": threadID,
			"priority": int(priority),
		},
	}

	var resp struct {
		Data struct {
			UpdateThread struct {
				Error *graphQLMutationError `json:"error"`
			} `json:"updateThread"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}

	if err := c.do(ctx, query, variables, &resp); err != nil {
		return err
	}
	if err := firstGraphQLError(resp.Errors); err != nil {
		return err
	}
	if resp.Data.UpdateThread.Error != nil {
		return fmt.Errorf("plain updateThread: %s", resp.Data.UpdateThread.Error.Message)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal HTTP helpers
// ---------------------------------------------------------------------------

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLMutationError struct {
	Message string `json:"message"`
}

func firstGraphQLError(errs []graphQLError) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Message)
	}
	return fmt.Errorf("plain graphql: %s", strings.Join(msgs, "; "))
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) endpoint() string {
	if strings.TrimSpace(c.Endpoint) != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}

func (c *Client) do(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("marshalling plain request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating plain request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("plain request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading plain response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plain api returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("unmarshalling plain response: %w", err)
	}
	return nil
}
