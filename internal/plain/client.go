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
	// Body is the plain-text body of the thread, built by concatenating the
	// customer-authored timeline entries (ChatEntry, EmailEntry, SlackMessageEntry).
	// Internal entry types (NoteEntry, CustomEntry, system transitions) are skipped.
	// Capped at threadBodyMaxBytes with a trailing truncation marker when exceeded.
	// Empty when the thread has no customer content or the timeline fetch failed
	// (the latter still returns metadata — the classifier degrades gracefully).
	Body string
}

// threadBodyMaxBytes caps the extracted body text to keep classifier prompts
// bounded and LLM token cost predictable.
const threadBodyMaxBytes = 8 * 1024

// threadTimelineFirst is the page size for timeline entries. Support threads
// are overwhelmingly under this count; we do not paginate further.
const threadTimelineFirst = 25

// threadBodyTruncSuffix is appended when the concatenated body exceeds
// threadBodyMaxBytes.
const threadBodyTruncSuffix = "\n...[truncated]"

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

// GetThread fetches thread metadata and concatenates customer-authored
// timeline entries into ThreadInfo.Body. The body extraction is a single
// combined GraphQL query over thread metadata + timelineEntries (first N).
//
// Entry selection: ChatEntry.text, EmailEntry.textContent (or fullTextContent
// when hasMoreTextContent is true), SlackMessageEntry.text. All other entry
// types (NoteEntry, CustomEntry, system transitions, link events, surveys,
// etc.) are silently skipped.
//
// Body is capped at threadBodyMaxBytes with a truncation marker. If the
// timeline payload is present but yields no customer content, Body is empty.
func (c *Client) GetThread(ctx context.Context, threadID string) (ThreadInfo, error) {
	query := `
query GetThread($threadId: ID!, $first: Int!) {
  thread(threadId: $threadId) {
    id
    title
    customer {
      fullName
      email {
        email
      }
    }
    timelineEntries(first: $first) {
      edges {
        node {
          entry {
            __typename
            ... on ChatEntry { chatText: text }
            ... on EmailEntry { textContent fullTextContent hasMoreTextContent }
            ... on SlackMessageEntry { slackText: text }
          }
        }
      }
    }
  }
}`
	variables := map[string]any{
		"threadId": threadID,
		"first":    threadTimelineFirst,
	}

	var resp struct {
		Data struct {
			Thread *struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Customer struct {
					FullName string `json:"fullName"`
					Email    struct {
						Email string `json:"email"`
					} `json:"email"`
				} `json:"customer"`
				TimelineEntries struct {
					Edges []struct {
						Node struct {
							Entry timelineEntryPayload `json:"entry"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"timelineEntries"`
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
		CustomerEmail: t.Customer.Email.Email,
	}
	if info.ThreadID == "" {
		info.ThreadID = threadID
	}

	parts := make([]string, 0, len(t.TimelineEntries.Edges))
	for _, edge := range t.TimelineEntries.Edges {
		if text := edge.Node.Entry.extractText(); text != "" {
			parts = append(parts, text)
		}
	}
	info.Body = joinAndCapBody(parts)

	return info, nil
}

// timelineEntryPayload decodes the union-typed Entry field. Only the subset
// of customer-content entry types is modelled; everything else is skipped
// via an empty extractText result.
// ChatText and SlackText are aliased because ChatEntry.text is nullable
// (String) while SlackMessageEntry.text is non-null (String!); GraphQL
// rejects the same field name returning conflicting types without aliases.
type timelineEntryPayload struct {
	Typename           string `json:"__typename"`
	ChatText           string `json:"chatText"`
	SlackText          string `json:"slackText"`
	TextContent        string `json:"textContent"`
	FullTextContent    string `json:"fullTextContent"`
	HasMoreTextContent bool   `json:"hasMoreTextContent"`
}

// extractText returns the customer-authored text for this entry, or "" when
// the entry type carries no customer content we care about.
func (p timelineEntryPayload) extractText() string {
	switch p.Typename {
	case "ChatEntry":
		return strings.TrimSpace(p.ChatText)
	case "SlackMessageEntry":
		return strings.TrimSpace(p.SlackText)
	case "EmailEntry":
		if p.HasMoreTextContent && strings.TrimSpace(p.FullTextContent) != "" {
			return strings.TrimSpace(p.FullTextContent)
		}
		return strings.TrimSpace(p.TextContent)
	}
	return ""
}

// joinAndCapBody concatenates the non-empty parts with a blank-line separator
// and truncates the result to threadBodyMaxBytes, appending a marker if cut.
func joinAndCapBody(parts []string) string {
	nonEmpty := parts[:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	body := strings.Join(nonEmpty, "\n\n")
	if len(body) > threadBodyMaxBytes {
		cut := threadBodyMaxBytes - len(threadBodyTruncSuffix)
		if cut < 0 {
			cut = 0
		}
		body = body[:cut] + threadBodyTruncSuffix
	}
	return body
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
