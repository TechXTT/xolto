// Package plain — support_adapter.go
//
// SupportAdapter is a thin adapter that wraps *Client to satisfy the
// plainSupportAPI interface expected by the support classifier worker.
//
// It bridges the classifier's string-based SetPriority and plural AddLabels
// contract to the Client's typed Priority enum and singular AddLabel method.
package plain

import (
	"context"
	"fmt"
)

// SupportAdapter wraps *Client to expose the four methods the classifier
// worker calls: GetThread, AddLabels, AddNote, SetPriority.
type SupportAdapter struct {
	client *Client
}

// NewSupportAdapter returns a SupportAdapter backed by the given Client.
func NewSupportAdapter(c *Client) *SupportAdapter {
	return &SupportAdapter{client: c}
}

// GetThread fetches thread metadata. Delegates to Client.GetThread.
func (a *SupportAdapter) GetThread(ctx context.Context, threadID string) (ThreadInfo, error) {
	return a.client.GetThread(ctx, threadID)
}

// AddLabels attaches each labelTypeID to the thread in order.
// It aborts and returns the first error encountered.
func (a *SupportAdapter) AddLabels(ctx context.Context, threadID string, labelTypeIDs []string) error {
	for _, id := range labelTypeIDs {
		if err := a.client.AddLabel(ctx, threadID, id); err != nil {
			return err
		}
	}
	return nil
}

// AddNote posts an internal note on the given thread.
func (a *SupportAdapter) AddNote(ctx context.Context, threadID, body string) error {
	return a.client.AddNote(ctx, threadID, body)
}

// SetPriority maps the string priority to the typed Priority enum and calls
// Client.SetPriority. Valid values: "urgent", "high", "normal", "low".
// Any other value returns a descriptive error.
func (a *SupportAdapter) SetPriority(ctx context.Context, threadID, priority string) error {
	var p Priority
	switch priority {
	case "urgent":
		p = PriorityUrgent
	case "high":
		p = PriorityHigh
	case "normal":
		p = PriorityNormal
	case "low":
		p = PriorityLow
	default:
		return fmt.Errorf("plain: unknown priority %q", priority)
	}
	return a.client.SetPriority(ctx, threadID, p)
}
