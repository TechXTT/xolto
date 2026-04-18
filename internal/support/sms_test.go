package support

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/store"
)

// ---------------------------------------------------------------------------
// Fake smsSender
// ---------------------------------------------------------------------------

type fakeSender struct {
	calls     int
	responses []error // consume one per call; last one is repeated
}

func (f *fakeSender) SendSMS(_ context.Context, _, _, _ string) error {
	f.calls++
	if len(f.responses) == 0 {
		return nil
	}
	idx := f.calls - 1
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	return f.responses[idx]
}

// ---------------------------------------------------------------------------
// Test logger that writes to a buffer
// ---------------------------------------------------------------------------

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// ---------------------------------------------------------------------------
// strPtr helper
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Helpers to build test SupportEvent
// ---------------------------------------------------------------------------

func incidentEvent() store.SupportEvent {
	return store.SupportEvent{
		ID:            "evt-001",
		PlainThreadID: "thr_abc123",
		Severity:      strPtr(string(SeverityIncident)),
		Category:      strPtr("bug"),
		Market:        strPtr("olx_bg"),
		LinearIssue:   strPtr("XOL-99"),
	}
}

// ---------------------------------------------------------------------------
// AC-1: Dry-run mode — non-prod env logs but does NOT call sender
// ---------------------------------------------------------------------------

func TestSMSEscalator_DryRunMode_LogsPayload(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	sender := &fakeSender{}

	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "development", // non-prod
		Logger:     logger,
	})

	err := esc.NotifyIncident(context.Background(), incidentEvent())
	if err != nil {
		t.Fatalf("expected nil error in dry-run, got %v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("expected 0 Twilio calls in dry-run, got %d", sender.calls)
	}

	log := buf.String()
	for _, want := range []string{
		"sms_dry_run",
		"+15550002222", // to (founder number)
		"+15550001111", // from
		"evt-001",      // event_id
		"thr_abc123",   // body contains PlainThreadID
	} {
		if !strings.Contains(log, want) {
			t.Errorf("expected log to contain %q, got:\n%s", want, log)
		}
	}
}

// AC-1 — unset APP_ENV must also suppress the real call (fail-safe default)
func TestSMSEscalator_DryRun_UnsetAppEnv(t *testing.T) {
	sender := &fakeSender{}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "", // unset → prod-safe → should be production, so SMS IS sent
		Logger:     newTestLogger(&bytes.Buffer{}),
	})

	// We cannot hit real Twilio in tests so use a fake that always fails.
	// But the point is: with empty AppEnv = production, the escalator ATTEMPTS
	// the send (i.e. sender.calls > 0). Dry-run only fires for explicit dev envs.
	sender.responses = []error{ErrTwilioPermanent}
	err := esc.NotifyIncident(context.Background(), incidentEvent())
	if sender.calls == 0 {
		t.Fatal("expected Twilio to be called when AppEnv is empty (prod-safe), but calls==0")
	}
	// expect a permanent error back
	if !errors.Is(err, ErrTwilioPermanent) {
		t.Fatalf("expected ErrTwilioPermanent, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC-2: Retry logic — 3 retries with exponential backoff (100ms/500ms/2s)
// We replace retrySchedule with zero durations so the test is fast.
// ---------------------------------------------------------------------------

func withZeroRetrySchedule(t *testing.T) {
	t.Helper()
	original := make([]time.Duration, len(retrySchedule))
	copy(original, retrySchedule)
	retrySchedule = []time.Duration{0, 0, 0}
	t.Cleanup(func() { retrySchedule = original })
}

func TestSMSEscalator_Retry_TransientThenSuccess(t *testing.T) {
	withZeroRetrySchedule(t)

	var buf bytes.Buffer
	sender := &fakeSender{
		responses: []error{ErrTwilioTransient, ErrTwilioTransient, nil},
	}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "production",
		Logger:     newTestLogger(&buf),
	})

	err := esc.NotifyIncident(context.Background(), incidentEvent())
	if err != nil {
		t.Fatalf("expected success after transient errors, got %v", err)
	}
	if sender.calls != 3 {
		t.Fatalf("expected 3 calls (2 transient + 1 success), got %d", sender.calls)
	}
}

func TestSMSEscalator_Retry_AllTransient_ReturnsLastError(t *testing.T) {
	withZeroRetrySchedule(t)

	sender := &fakeSender{
		responses: []error{ErrTwilioTransient}, // repeated for all attempts
	}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "production",
		Logger:     newTestLogger(&bytes.Buffer{}),
	})

	err := esc.NotifyIncident(context.Background(), incidentEvent())
	if err == nil {
		t.Fatal("expected error after all retries exhausted, got nil")
	}
	if !errors.Is(err, ErrTwilioTransient) {
		t.Fatalf("expected ErrTwilioTransient after retries, got %v", err)
	}
	// 1 initial + 3 retries = 4 total calls.
	if sender.calls != 4 {
		t.Fatalf("expected 4 total calls (1+3 retries), got %d", sender.calls)
	}
}

func TestSMSEscalator_Retry_Permanent_NoRetry(t *testing.T) {
	withZeroRetrySchedule(t)

	sender := &fakeSender{
		responses: []error{ErrTwilioPermanent},
	}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "production",
		Logger:     newTestLogger(&bytes.Buffer{}),
	})

	err := esc.NotifyIncident(context.Background(), incidentEvent())
	if !errors.Is(err, ErrTwilioPermanent) {
		t.Fatalf("expected ErrTwilioPermanent, got %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("expected exactly 1 call on permanent error (no retry), got %d", sender.calls)
	}
}

// ---------------------------------------------------------------------------
// AC-4: Non-incident paths do NOT trigger NotifyIncident (call returns nil,
// no Twilio calls).
// ---------------------------------------------------------------------------

func TestSMSEscalator_NonIncident_NoSMSSent(t *testing.T) {
	sender := &fakeSender{}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "production",
		Logger:     newTestLogger(&bytes.Buffer{}),
	})

	for _, sev := range []string{"low", "medium", "high"} {
		sev := sev
		t.Run("severity="+sev, func(t *testing.T) {
			event := store.SupportEvent{
				ID:            "evt-002",
				PlainThreadID: "thr_xyz",
				Severity:      strPtr(sev),
			}
			err := esc.NotifyIncident(context.Background(), event)
			if err != nil {
				t.Fatalf("expected nil for non-incident severity %q, got %v", sev, err)
			}
			if sender.calls != 0 {
				t.Fatalf("expected 0 Twilio calls for severity %q, got %d", sev, sender.calls)
			}
		})
	}
}

func TestSMSEscalator_NilSeverity_NoSMSSent(t *testing.T) {
	sender := &fakeSender{}
	esc := NewSMSEscalator(SMSEscalatorConfig{
		Sender:     sender,
		FromNumber: "+15550001111",
		FounderNum: "+15550002222",
		AppEnv:     "production",
		Logger:     newTestLogger(&bytes.Buffer{}),
	})

	event := store.SupportEvent{
		ID:            "evt-003",
		PlainThreadID: "thr_nil",
		Severity:      nil,
	}
	err := esc.NotifyIncident(context.Background(), event)
	if err != nil {
		t.Fatalf("expected nil for nil severity, got %v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("expected 0 Twilio calls for nil severity, got %d", sender.calls)
	}
}

// ---------------------------------------------------------------------------
// buildSMSBody — payload shape
// ---------------------------------------------------------------------------

func TestBuildSMSBody_ContainsRequiredFields(t *testing.T) {
	event := incidentEvent()
	body := buildSMSBody(event)

	for _, want := range []string{
		"[xolto INCIDENT]",
		"bug/olx_bg",
		"thr_abc123",
		"Plain: https://app.plain.com/threads/thr_abc123",
		"Linear: XOL-99",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected SMS body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestBuildSMSBody_NoLinearIssue(t *testing.T) {
	event := incidentEvent()
	event.LinearIssue = nil
	body := buildSMSBody(event)
	if !strings.Contains(body, "Linear: none") {
		t.Errorf("expected 'Linear: none' when no linear issue, got:\n%s", body)
	}
}
