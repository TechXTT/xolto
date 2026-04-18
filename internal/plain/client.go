// Package plain provides a minimal GraphQL client for the Plain.com API.
//
// Only the five methods needed in Phase 1 (SUP-2) are implemented:
//   - UpsertCustomer — ensures a customer record exists for an email address.
//   - CreateThread   — opens a new support thread under a customer.
//   - AddLabel       — attaches a label to a thread.
//   - AddNote        — posts an internal note on a thread.
//   - SetPriority    — updates the priority of a thread.
//
// Authentication uses the PLAIN_API_KEY environment variable. The HTTP client
// is injected via Client.HTTPClient so tests can substitute an httptest.Server.
//
// All five methods call the Plain GraphQL endpoint at
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
	variables := map[string]any{
		"input": map[string]any{
			"identifier": map[string]any{
				"emailAddress": input.Email,
			},
			"onCreate": map[string]any{
				"fullName": map[string]any{
					"value": input.FullName,
				},
				"email": map[string]any{
					"email":             input.Email,
					"isVerified":        true,
					"verifiedAt":        nil,
				},
			},
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
