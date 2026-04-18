package store

// Postgres integration tests for support_events.
//
// These tests execute real SQL against a live Postgres instance. They are
// gated on the TEST_POSTGRES_DSN environment variable and skip silently when
// that variable is unset, so they do not break the standard `go test ./...`
// run on machines without a Postgres instance.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable" \
//	  go test ./internal/store/ -run TestPostgresSupport -v

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AC-2: webhook idempotent upsert — same plain_thread_id twice → one row
// ---------------------------------------------------------------------------

func TestPostgresSupportEventWebhookIdempotentUpsert(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	event := SupportEvent{
		PlainThreadID: "th_idem0001",
		IntakeSource:  "email",
	}

	// First insert.
	first, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook(first) error = %v", err)
	}
	if first.PlainThreadID != "th_idem0001" {
		t.Fatalf("expected plain_thread_id=th_idem0001, got %q", first.PlainThreadID)
	}
	if first.ID == "" {
		t.Fatal("expected non-empty id after first insert")
	}

	// Second insert — same plain_thread_id, different intake_source to verify
	// the upsert preserves the first row's ID.
	event.IntakeSource = "dash_contact"
	second, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook(second) error = %v", err)
	}

	// Must be the same row (same UUID primary key).
	if first.ID != second.ID {
		t.Fatalf("expected same row ID on duplicate upsert; got first=%q second=%q", first.ID, second.ID)
	}

	// Verify only one row exists.
	rows, err := st.db.QueryContext(ctx,
		`SELECT COUNT(*) FROM support_events WHERE plain_thread_id = $1`,
		"th_idem0001",
	)
	if err != nil {
		t.Fatalf("count query error = %v", err)
	}
	defer rows.Close()
	var count int
	if rows.Next() {
		_ = rows.Scan(&count)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for plain_thread_id=th_idem0001, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// GetByPlainThreadID round-trip
// ---------------------------------------------------------------------------

func TestPostgresSupportEventGetByPlainThreadID(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	event := SupportEvent{
		PlainThreadID: "th_get0001",
		IntakeSource:  "email",
	}
	_, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook() error = %v", err)
	}

	got, err := st.GetByPlainThreadID(ctx, "th_get0001")
	if err != nil {
		t.Fatalf("GetByPlainThreadID() error = %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil event")
	}
	if got.PlainThreadID != "th_get0001" {
		t.Fatalf("expected plain_thread_id=th_get0001, got %q", got.PlainThreadID)
	}
	if got.IntakeSource != "email" {
		t.Fatalf("expected intake_source=email, got %q", got.IntakeSource)
	}

	// Non-existent thread returns nil.
	missing, err := st.GetByPlainThreadID(ctx, "th_nonexistent")
	if err != nil {
		t.Fatalf("GetByPlainThreadID(missing) error = %v", err)
	}
	if missing != nil {
		t.Fatal("expected nil for non-existent plain_thread_id")
	}
}

// ---------------------------------------------------------------------------
// AttachClassification
// ---------------------------------------------------------------------------

func TestPostgresSupportEventAttachClassification(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	event := SupportEvent{
		PlainThreadID: "th_class001",
		IntakeSource:  "email",
	}
	_, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook() error = %v", err)
	}

	c := Classification{
		ClassifiedAt: time.Now().UTC(),
		Category:     "pricing",
		Market:       "olx_bg",
		ProductCat:   "phone",
		Severity:     "medium",
		ActionNeeded: "backend_fix",
	}
	if err := st.AttachClassification(ctx, "th_class001", c); err != nil {
		t.Fatalf("AttachClassification() error = %v", err)
	}

	got, err := st.GetByPlainThreadID(ctx, "th_class001")
	if err != nil {
		t.Fatalf("GetByPlainThreadID() error = %v", err)
	}
	if got.Category == nil || *got.Category != "pricing" {
		t.Errorf("expected category=pricing, got %v", got.Category)
	}
	if got.Market == nil || *got.Market != "olx_bg" {
		t.Errorf("expected market=olx_bg, got %v", got.Market)
	}
	if got.Severity == nil || *got.Severity != "medium" {
		t.Errorf("expected severity=medium, got %v", got.Severity)
	}
	if got.ActionNeeded == nil || *got.ActionNeeded != "backend_fix" {
		t.Errorf("expected action_needed=backend_fix, got %v", got.ActionNeeded)
	}
	if got.ClassifiedAt == nil {
		t.Error("expected classified_at to be set")
	}
}

// ---------------------------------------------------------------------------
// AttachLinearIssue
// ---------------------------------------------------------------------------

func TestPostgresSupportEventAttachLinearIssue(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	event := SupportEvent{
		PlainThreadID: "th_lin001",
		IntakeSource:  "email",
	}
	_, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook() error = %v", err)
	}

	if err := st.AttachLinearIssue(ctx, "th_lin001", "XOL-99"); err != nil {
		t.Fatalf("AttachLinearIssue() error = %v", err)
	}

	got, err := st.GetByPlainThreadID(ctx, "th_lin001")
	if err != nil {
		t.Fatalf("GetByPlainThreadID() error = %v", err)
	}
	if got.LinearIssue == nil || *got.LinearIssue != "XOL-99" {
		t.Fatalf("expected linear_issue=XOL-99, got %v", got.LinearIssue)
	}
}

// ---------------------------------------------------------------------------
// AttachLinearIssue on non-existent thread returns ErrSupportEventNotFound
// ---------------------------------------------------------------------------

func TestPostgresSupportEventAttachLinearIssueNotFound(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	err := st.AttachLinearIssue(ctx, "th_nonexistent", "XOL-99")
	if err != ErrSupportEventNotFound {
		t.Fatalf("expected ErrSupportEventNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// dash_context JSON round-trip
// ---------------------------------------------------------------------------

func TestPostgresSupportEventDashContextRoundTrip(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()

	event := SupportEvent{
		PlainThreadID: "th_ctx001",
		IntakeSource:  "dash_contact",
		DashContext: map[string]any{
			"mission_id":   float64(42),
			"current_path": "/missions/42/matches",
		},
	}
	saved, err := st.UpsertEventFromWebhook(ctx, event)
	if err != nil {
		t.Fatalf("UpsertEventFromWebhook() error = %v", err)
	}

	got, err := st.GetByPlainThreadID(ctx, saved.PlainThreadID)
	if err != nil {
		t.Fatalf("GetByPlainThreadID() error = %v", err)
	}
	if got.DashContext == nil {
		t.Fatal("expected dash_context to be populated")
	}
	if v, ok := got.DashContext["mission_id"]; !ok || v != float64(42) {
		t.Errorf("expected dash_context.mission_id=42, got %v", got.DashContext)
	}
}
