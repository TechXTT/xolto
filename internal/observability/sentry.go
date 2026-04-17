// Package observability wires backend error tracking (Sentry).
// It exposes a single Init function that reads SENTRY_DSN from the environment.
// When SENTRY_DSN is empty the function is a no-op so local dev is unaffected.
package observability

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

// sentryEnabled is set to true after a successful Init call.
var sentryEnabled bool

// SentryEnabled reports whether the Sentry SDK was successfully initialised.
// The Sentry middleware uses this to skip hub operations when the SDK is off.
func SentryEnabled() bool { return sentryEnabled }

// SetEnabledForTest overrides the enabled flag for use in unit tests.
// It must not be called from non-test code.
func SetEnabledForTest(v bool) { sentryEnabled = v }

// sensitiveHeaders is the list of request headers that must be scrubbed before
// any event is sent to Sentry. Values are replaced with "[Filtered]".
var sensitiveHeaders = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
}

// sensitiveParams is the deny-list of URL query / form parameter names whose
// values must be scrubbed. Matching is case-insensitive.
var sensitiveParams = []string{
	"password",
	"token",
	"secret",
	"api_key",
	"apikey",
	"access_token",
	"refresh_token",
	"client_secret",
}

// Init initialises the Sentry SDK from environment variables.
//
// Required env var:
//
//	SENTRY_DSN  — when empty, Init is a no-op (local dev).
//
// Optional env vars:
//
//	SENTRY_ENVIRONMENT — defaults to "development"
//	SENTRY_RELEASE     — commit SHA; can also be injected at build time via
//	                     -ldflags "-X github.com/TechXTT/xolto/internal/observability.Release=<sha>"
func Init(release string) {
	dsn := strings.TrimSpace(os.Getenv("SENTRY_DSN"))
	if dsn == "" {
		slog.Default().Info("sentry disabled: no DSN", "op", "sentry.init")
		return
	}

	env := strings.TrimSpace(os.Getenv("SENTRY_ENVIRONMENT"))
	if env == "" {
		env = "development"
	}

	// Prefer build-time release; fall back to env var.
	rel := strings.TrimSpace(release)
	if rel == "" {
		rel = strings.TrimSpace(os.Getenv("SENTRY_RELEASE"))
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		Release:          rel,
		AttachStacktrace: true,
		BeforeSend:       scrubEvent,
	})
	if err != nil {
		slog.Default().Error("sentry init failed", "op", "sentry.init", "error", err)
		return
	}

	sentryEnabled = true
	slog.Default().Info("sentry initialised",
		"op", "sentry.init",
		"environment", env,
		"release", rel,
	)
}

// Flush flushes any queued Sentry events before process exit.
// Call this from a graceful-shutdown path with a short timeout.
func Flush(timeout time.Duration) {
	if !sentryEnabled {
		return
	}
	sentry.Flush(timeout)
}

// scrubEvent removes sensitive header values and query parameter values
// before the event is transmitted to Sentry. It is registered as BeforeSend.
func scrubEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}
	// Scrub sensitive request headers.
	for key := range event.Request.Headers {
		for _, h := range sensitiveHeaders {
			if strings.EqualFold(key, h) {
				event.Request.Headers[key] = "[Filtered]"
			}
		}
	}
	// Scrub sensitive query string parameters.
	if event.Request.QueryString != "" {
		event.Request.QueryString = scrubQueryString(event.Request.QueryString)
	}
	// Redact the raw request body to avoid leaking posted credentials.
	// The body (event.Request.Data) is a raw string; clear it entirely rather
	// than attempting to parse and selectively redact form/JSON fields.
	if event.Request.Data != "" {
		event.Request.Data = "[Filtered]"
	}
	return event
}

// scrubQueryString redacts sensitive parameter values in a raw query string.
func scrubQueryString(raw string) string {
	parts := strings.Split(raw, "&")
	for i, part := range parts {
		eqIdx := strings.IndexByte(part, '=')
		if eqIdx < 0 {
			continue
		}
		key := part[:eqIdx]
		for _, s := range sensitiveParams {
			if strings.EqualFold(key, s) {
				parts[i] = key + "=[Filtered]"
				break
			}
		}
	}
	return strings.Join(parts, "&")
}
