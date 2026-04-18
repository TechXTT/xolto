// Package support — twilio.go
//
// Thin Twilio SMS client using net/http only (no Twilio SDK).
// Posts to the Twilio Messages API with HTTP Basic Auth.
// Returns ErrTwilioTransient on 5xx / network errors so the caller
// can retry, and ErrTwilioPermanent on 4xx (auth / bad payload).
package support

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Sentinel errors allow callers to use errors.Is.
var (
	// ErrTwilioTransient indicates a 5xx or network error — safe to retry.
	ErrTwilioTransient = errors.New("twilio: transient error (5xx or network)")
	// ErrTwilioPermanent indicates a 4xx error — do not retry.
	ErrTwilioPermanent = errors.New("twilio: permanent error (4xx)")
)

// TwilioSenderInterface is the exported interface for the Twilio SMS sender.
// It allows main.go and tests to inject a concrete implementation or a fake.
type TwilioSenderInterface interface {
	SendSMS(ctx context.Context, fromNumber, toNumber, body string) error
}

// twilioMessagesURL returns the Twilio Messages API endpoint for the given
// account SID.
func twilioMessagesURL(accountSID string) string {
	return fmt.Sprintf(
		"https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json",
		accountSID,
	)
}

// TwilioClient is a thin HTTP wrapper around the Twilio Messages REST API.
// Use NewTwilioClient to construct one.
type TwilioClient struct {
	accountSID string
	authToken  string
	httpClient *http.Client
}

// NewTwilioClient returns a TwilioClient. The http.Client can be replaced in
// tests by passing a custom one; pass nil to use http.DefaultClient.
func NewTwilioClient(accountSID, authToken string, httpClient *http.Client) *TwilioClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &TwilioClient{
		accountSID: accountSID,
		authToken:  authToken,
		httpClient: httpClient,
	}
}

// SendSMS sends a single SMS from fromNumber to toNumber with the given body.
//
// Returns:
//   - nil on HTTP 201 Created.
//   - ErrTwilioTransient (wrapped) on 5xx or network errors.
//   - ErrTwilioPermanent (wrapped) on 4xx errors.
func (c *TwilioClient) SendSMS(ctx context.Context, fromNumber, toNumber, body string) error {
	form := url.Values{}
	form.Set("From", fromNumber)
	form.Set("To", toNumber)
	form.Set("Body", body)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		twilioMessagesURL(c.accountSID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrTwilioTransient, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.accountSID, c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTwilioTransient, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain body for connection reuse; limit to 4 KB.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d from Twilio", ErrTwilioTransient, resp.StatusCode)
	default:
		// 4xx or anything else unexpected.
		return fmt.Errorf("%w: HTTP %d from Twilio", ErrTwilioPermanent, resp.StatusCode)
	}
}
