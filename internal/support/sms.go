// Package support — sms.go
//
// SMSEscalator sends founder SMS notifications for severity=incident events.
// In non-production environments (APP_ENV != "production") the SMS is logged
// but never sent (dry-run mode), satisfying AC-1.
//
// Retry schedule (AC-2): up to 3 retries with 100ms / 500ms / 2s backoff.
// Only ErrTwilioTransient is retried; ErrTwilioPermanent is returned immediately.
//
// Sentry reporting (AC-3): after 3 failed retries the final error and the
// support event ID are captured in Sentry at severity=error.
package support

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/TechXTT/xolto/internal/store"
)

// retrySchedule defines the wait durations between retries (AC-2).
// Index 0 = delay before retry 1, index 1 = before retry 2, etc.
var retrySchedule = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// maxRetries is the number of additional attempts after the first failure.
const maxRetries = 3

// SMSEscalator sends an SMS to the founder when a support event reaches
// severity=incident. It is injected into SUP-4's classifier as a callback.
type SMSEscalator struct {
	sender      TwilioSenderInterface
	fromNumber  string
	founderNum  string
	appEnv      string
	logger      *slog.Logger
}

// SMSEscalatorConfig holds the constructor parameters for SMSEscalator.
type SMSEscalatorConfig struct {
	Sender     TwilioSenderInterface
	FromNumber string
	FounderNum string
	AppEnv     string
	Logger     *slog.Logger
}

// NewSMSEscalator constructs an SMSEscalator. If Logger is nil, the default
// slog logger is used.
func NewSMSEscalator(cfg SMSEscalatorConfig) *SMSEscalator {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SMSEscalator{
		sender:     cfg.Sender,
		fromNumber: cfg.FromNumber,
		founderNum: cfg.FounderNum,
		appEnv:     cfg.AppEnv,
		logger:     logger,
	}
}

// NotifyIncident sends (or dry-run logs) an SMS to the founder when
// event.Severity == "incident".
//
// Non-incident severities are silently ignored (log + return nil).
// This satisfies AC-4: non-incident paths do NOT call into Twilio.
func (e *SMSEscalator) NotifyIncident(ctx context.Context, event store.SupportEvent) error {
	// Guard: only act on incident severity.
	if event.Severity == nil || *event.Severity != string(SeverityIncident) {
		e.logger.Info(
			"sms escalator: skipping non-incident event",
			"op", "sms.escalator.skip",
			"event_id", event.ID,
			"severity", severityString(event.Severity),
		)
		return nil
	}

	body := buildSMSBody(event)

	// Dry-run mode (AC-1): log payload but do not call Twilio.
	if !isProductionAppEnv(e.appEnv) {
		e.logger.Info(
			"sms_dry_run: incident SMS suppressed in non-prod env",
			"op", "sms.escalator.dry_run",
			"event_id", event.ID,
			"to", e.founderNum,
			"from", e.fromNumber,
			"body", body,
		)
		return nil
	}

	return e.sendWithRetry(ctx, event.ID, body)
}

// sendWithRetry executes the Twilio send with exponential backoff.
// Retries only on ErrTwilioTransient (AC-2); 4xx → fail immediately.
// After maxRetries failed attempts, captures the error in Sentry (AC-3).
func (e *SMSEscalator) sendWithRetry(ctx context.Context, eventID, body string) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := e.sender.SendSMS(ctx, e.fromNumber, e.founderNum, body)
		if err == nil {
			e.logger.Info(
				"sms escalator: incident SMS sent",
				"op", "sms.escalator.sent",
				"event_id", eventID,
				"attempt", attempt+1,
			)
			return nil
		}

		// Permanent errors are not retried (AC-2).
		if errors.Is(err, ErrTwilioPermanent) {
			e.logger.Error(
				"sms escalator: permanent Twilio error, not retrying",
				"op", "sms.escalator.permanent_error",
				"event_id", eventID,
				"error", err,
			)
			e.captureSentry(eventID, err)
			return err
		}

		lastErr = err
		e.logger.Warn(
			"sms escalator: transient Twilio error",
			"op", "sms.escalator.transient_error",
			"event_id", eventID,
			"attempt", attempt+1,
			"error", err,
		)

		// If we've used all retries, break before sleeping.
		if attempt == maxRetries {
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("sms escalator: context cancelled: %w", ctx.Err())
		case <-time.After(retrySchedule[attempt]):
		}
	}

	// All retries exhausted (AC-3).
	e.logger.Error(
		"sms escalator: all retries exhausted, reporting to Sentry",
		"op", "sms.escalator.retries_exhausted",
		"event_id", eventID,
		"error", lastErr,
	)
	e.captureSentry(eventID, lastErr)
	return lastErr
}

// captureSentry reports a failed SMS escalation to Sentry with the event ID
// so incidents can be reconciled (AC-3).
func (e *SMSEscalator) captureSentry(eventID string, err error) {
	hub := sentry.CurrentHub().Clone()
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("op", "sms.escalator.failure")
		scope.SetTag("event_id", eventID)
		scope.SetLevel(sentry.LevelError)
		scope.SetExtra("event_id", eventID)
		scope.SetExtra("error", err.Error())
	})
	hub.CaptureException(err)
}

// buildSMSBody formats the 160-char-target SMS payload from the XOL-56 template:
//
//	[xolto INCIDENT] {category}/{market}
//	{subject}
//	Plain: {thread_url}
//	Linear: {linear_issue_url or "none"}
func buildSMSBody(event store.SupportEvent) string {
	category := derefStr(event.Category, "unknown")
	market := derefStr(event.Market, "unknown")
	subject := derefStr((*string)(nil), "") // SupportEvent has no Subject field; use PlainThreadID as fallback
	if subject == "" {
		subject = event.PlainThreadID
	}
	threadURL := fmt.Sprintf("https://app.plain.com/threads/%s", event.PlainThreadID)
	linearURL := derefStr(event.LinearIssue, "none")

	return fmt.Sprintf("[xolto INCIDENT] %s/%s\n%s\nPlain: %s\nLinear: %s",
		category, market, subject, threadURL, linearURL)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func derefStr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func severityString(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// isProductionAppEnv returns true when the APP_ENV value is production (or
// unset). Only explicit non-production names opt out. Mirrors the logic in
// internal/config to keep the two packages decoupled.
func isProductionAppEnv(appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "dev", "development", "test", "testing", "staging", "local":
		return false
	default:
		return true
	}
}
