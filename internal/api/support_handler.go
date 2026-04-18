package api

// support_handler.go — XOL-53 SUP-2
//
// Implements two endpoints:
//
//   POST /v1/support/webhook  — Plain → markt webhook (no bearer auth,
//                               HMAC-SHA256 signature verification).
//
//   POST /v1/support/report   — dash → markt contact form (auth required,
//                               creates a Plain thread via GraphQL).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/store"
)

func (s *Server) registerSupportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/support/webhook", s.handleSupportWebhook)
	mux.HandleFunc("/v1/support/report", s.requireAuth(s.handleSupportReport))
}

// Verify the Server satisfies the expected handler signature at compile time
// via the requireAuth wrapper — no explicit assertion needed.

// ---------------------------------------------------------------------------
// POST /v1/support/webhook
// ---------------------------------------------------------------------------

// plainWebhookPayload is the envelope sent by Plain for thread events.
type plainWebhookPayload struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// plainWebhookThread is the thread sub-object inside relevant event payloads.
type plainWebhookThread struct {
	ID string `json:"id"`
}

// plainWebhookEventPayload is the inner payload for thread.created /
// thread.message.added events.
type plainWebhookEventPayload struct {
	Thread plainWebhookThread `json:"thread"`
}

// handleSupportWebhook receives Plain webhook events, verifies the HMAC-SHA256
// signature, and upserts a support_events row.
func (s *Server) handleSupportWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	// Read body before signature check so we can verify the full payload.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		slog.Default().Error("support webhook: failed to read body",
			"op", "support.webhook.read_body", "error", err)
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	// Signature verification is mandatory regardless of environment.
	if !s.verifyPlainSignature(r.Header.Get("Plain-Signature"), body) {
		slog.Default().Warn("support webhook: invalid signature",
			"op", "support.webhook.signature.invalid",
			"request_id", requestIDFromRequest(r),
		)
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	var envelope plainWebhookPayload
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	// Only handle the two event types needed in Phase 1.
	switch envelope.Type {
	case "thread.created", "thread.message.added":
		// Parse the inner payload to extract the thread ID.
	default:
		// Unknown event types are acknowledged with 200 — Plain will not retry.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	var inner plainWebhookEventPayload
	if err := json.Unmarshal(envelope.Payload, &inner); err != nil || inner.Thread.ID == "" {
		writeError(w, http.StatusBadRequest, "could not parse thread ID from payload")
		return
	}

	event := store.SupportEvent{
		PlainThreadID: inner.Thread.ID,
		IntakeSource:  "email",
	}

	ctx := r.Context()
	saved, err := s.db.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		slog.Default().Error("support webhook: upsert failed",
			"op", "support.webhook.upsert",
			"plain_thread_id", inner.Thread.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "failed to persist support event")
		return
	}

	slog.Default().Info("support webhook: event persisted",
		"op", "support.webhook.persisted",
		"plain_thread_id", saved.PlainThreadID,
		"event_type", envelope.Type,
	)

	// Push onto in-process channel for SUP-4 classifier (non-blocking).
	select {
	case s.supportEvents <- saved:
	default:
		slog.Default().Warn("support webhook: event channel full, dropping event",
			"op", "support.webhook.channel.drop",
			"plain_thread_id", saved.PlainThreadID,
		)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// verifyPlainSignature returns true when the Plain-Signature header matches the
// HMAC-SHA256 of body using PLAIN_WEBHOOK_SECRET. Returns false if the secret
// is unset or the header is missing/wrong.
func (s *Server) verifyPlainSignature(header string, body []byte) bool {
	secret := strings.TrimSpace(s.cfg.PlainWebhookSecret)
	if secret == "" {
		return false
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}

// ---------------------------------------------------------------------------
// POST /v1/support/report
// ---------------------------------------------------------------------------

// supportReportRequest is the JSON body for the dash-facing contact endpoint.
type supportReportRequest struct {
	Subject     string                 `json:"subject"`
	Message     string                 `json:"message"`
	DashContext map[string]any         `json:"dash_context"`
}

// handleSupportReport creates a Plain thread on behalf of the authenticated
// user and persists a support_events row.
func (s *Server) handleSupportReport(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req supportReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Message = strings.TrimSpace(req.Message)
	if req.Subject == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, "subject and message are required")
		return
	}

	ctx := r.Context()

	threadID, err := s.createPlainThread(ctx, user.Email, user.Name, req.Subject, req.Message)
	if err != nil {
		slog.Default().Error("support report: plain thread creation failed",
			"op", "support.report.plain.create",
			"user_id", user.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "failed to create support thread")
		return
	}

	userID := user.ID
	event := store.SupportEvent{
		PlainThreadID: threadID,
		UserID:        &userID,
		IntakeSource:  "dash_contact",
		DashContext:   req.DashContext,
	}
	saved, err := s.db.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		slog.Default().Error("support report: db persist failed",
			"op", "support.report.db",
			"plain_thread_id", threadID,
			"error", err,
		)
		// Thread was already created in Plain; return success to the caller
		// so they don't retry (which would open a duplicate Plain thread).
		slog.Default().Warn("support report: returning plain_thread_id despite db error",
			"op", "support.report.db.partial",
			"plain_thread_id", threadID,
		)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plain_thread_id": threadID})
		return
	}

	slog.Default().Info("support report: event created",
		"op", "support.report.created",
		"plain_thread_id", saved.PlainThreadID,
		"user_id", user.ID,
	)

	// Push onto in-process channel (non-blocking).
	select {
	case s.supportEvents <- saved:
	default:
		slog.Default().Warn("support report: event channel full, dropping",
			"op", "support.report.channel.drop",
			"plain_thread_id", saved.PlainThreadID,
		)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "plain_thread_id": saved.PlainThreadID})
}

// createPlainThread upserts the customer and creates the thread in Plain.
func (s *Server) createPlainThread(ctx context.Context, email, name, subject, body string) (string, error) {
	customerResult, err := s.plainClient.UpsertCustomer(ctx, plain.UpsertCustomerInput{
		Email:    email,
		FullName: name,
	})
	if err != nil {
		return "", err
	}

	threadResult, err := s.plainClient.CreateThread(ctx, plain.CreateThreadInput{
		CustomerID: customerResult.CustomerID,
		Subject:    subject,
		Body:       body,
	})
	if err != nil {
		return "", err
	}
	return threadResult.ThreadID, nil
}
