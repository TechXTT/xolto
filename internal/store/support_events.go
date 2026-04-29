package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SupportEvent mirrors the support_events table columns.
// All time fields are UTC.
type SupportEvent struct {
	ID            string
	PlainThreadID string
	UserID        *string
	IntakeSource  string
	DashContext    map[string]any
	ClassifiedAt  *time.Time
	Category      *string
	Market        *string
	ProductCat    *string
	Severity      *string
	ActionNeeded  *string
	LinearIssue   *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Classification holds the fields written by the classifier (SUP-4).
type Classification struct {
	ClassifiedAt time.Time
	Category     string
	Market       string
	ProductCat   string
	Severity     string
	ActionNeeded string
}

// SupportEventStore is the interface for support_events persistence.
type SupportEventStore interface {
	UpsertEventFromWebhook(ctx context.Context, e SupportEvent) (SupportEvent, error)
	GetByPlainThreadID(ctx context.Context, id string) (*SupportEvent, error)
	AttachClassification(ctx context.Context, plainThreadID string, c Classification) error
	AttachLinearIssue(ctx context.Context, plainThreadID, linearIssue string) error
}

// ---------------------------------------------------------------------------
// Column list shared across Postgres queries.
// ---------------------------------------------------------------------------

const supportEventSelectColumns = `
	id, plain_thread_id, user_id, intake_source, dash_context,
	classified_at, category, market, product_cat, severity, action_needed,
	linear_issue, created_at, updated_at`

// ---------------------------------------------------------------------------
// Scan helper
// ---------------------------------------------------------------------------

func scanSupportEvent(row interface {
	Scan(dest ...any) error
}) (SupportEvent, error) {
	var e SupportEvent
	var userID sql.NullString
	var dashContextRaw []byte
	var classifiedAt sql.NullTime
	var category, market, productCat, severity, actionNeeded, linearIssue sql.NullString

	err := row.Scan(
		&e.ID,
		&e.PlainThreadID,
		&userID,
		&e.IntakeSource,
		&dashContextRaw,
		&classifiedAt,
		&category,
		&market,
		&productCat,
		&severity,
		&actionNeeded,
		&linearIssue,
		&e.CreatedAt,
		&e.UpdatedAt,
	)
	if err != nil {
		return SupportEvent{}, err
	}
	if userID.Valid {
		e.UserID = &userID.String
	}
	if len(dashContextRaw) > 0 {
		if err := json.Unmarshal(dashContextRaw, &e.DashContext); err != nil {
			e.DashContext = nil
		}
	}
	if classifiedAt.Valid {
		e.ClassifiedAt = &classifiedAt.Time
	}
	if category.Valid {
		e.Category = &category.String
	}
	if market.Valid {
		e.Market = &market.String
	}
	if productCat.Valid {
		e.ProductCat = &productCat.String
	}
	if severity.Valid {
		e.Severity = &severity.String
	}
	if actionNeeded.Valid {
		e.ActionNeeded = &actionNeeded.String
	}
	if linearIssue.Valid {
		e.LinearIssue = &linearIssue.String
	}
	return e, nil
}

// ---------------------------------------------------------------------------
// PostgresStore implementation
// ---------------------------------------------------------------------------

// UpsertEventFromWebhook inserts a support_events row identified by
// plain_thread_id. If a row already exists (idempotent Plain re-delivery),
// it updates only intake_source and updated_at, preserving all other fields.
func (s *PostgresStore) UpsertEventFromWebhook(ctx context.Context, e SupportEvent) (SupportEvent, error) {
	now := time.Now().UTC()

	var dashContextJSON []byte
	if e.DashContext != nil {
		var err error
		dashContextJSON, err = json.Marshal(e.DashContext)
		if err != nil {
			return SupportEvent{}, fmt.Errorf("marshalling dash_context: %w", err)
		}
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO support_events
			(plain_thread_id, user_id, intake_source, dash_context, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (plain_thread_id) DO UPDATE
			SET intake_source = EXCLUDED.intake_source,
			    updated_at    = EXCLUDED.updated_at
		RETURNING`+" "+supportEventSelectColumns,
		e.PlainThreadID,
		e.UserID,
		e.IntakeSource,
		nullableBytes(dashContextJSON),
		now,
	)
	return scanSupportEvent(row)
}

// GetByPlainThreadID returns the support event for the given Plain thread ID,
// or nil if no row exists.
func (s *PostgresStore) GetByPlainThreadID(ctx context.Context, id string) (*SupportEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT`+" "+supportEventSelectColumns+`
		FROM support_events
		WHERE plain_thread_id = $1`,
		id,
	)
	e, err := scanSupportEvent(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// AttachClassification writes classifier output fields onto an existing row
// identified by plain_thread_id.
func (s *PostgresStore) AttachClassification(ctx context.Context, plainThreadID string, c Classification) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE support_events
		SET classified_at = $1,
		    category      = $2,
		    market        = $3,
		    product_cat   = $4,
		    severity      = $5,
		    action_needed = $6,
		    updated_at    = $7
		WHERE plain_thread_id = $8`,
		c.ClassifiedAt,
		nullableString(c.Category),
		nullableString(c.Market),
		nullableString(c.ProductCat),
		nullableString(c.Severity),
		nullableString(c.ActionNeeded),
		now,
		plainThreadID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSupportEventNotFound
	}
	return nil
}

// AttachLinearIssue records the Linear issue identifier on an existing support
// event row.
func (s *PostgresStore) AttachLinearIssue(ctx context.Context, plainThreadID, linearIssue string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE support_events
		SET linear_issue = $1,
		    updated_at   = $2
		WHERE plain_thread_id = $3`,
		linearIssue,
		now,
		plainThreadID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSupportEventNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// SQLite migration — adds support_events to the local dev schema.
// ---------------------------------------------------------------------------

func migrateSupportEventsSQLite(db *sql.DB) {
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS support_events (
			id               TEXT PRIMARY KEY,
			plain_thread_id  TEXT NOT NULL UNIQUE,
			user_id          TEXT,
			intake_source    TEXT NOT NULL,
			dash_context     TEXT,
			classified_at    DATETIME,
			category         TEXT,
			market           TEXT,
			product_cat      TEXT,
			severity         TEXT,
			action_needed    TEXT,
			linear_issue     TEXT,
			created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_support_events_user ON support_events (user_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_support_events_classified_at ON support_events (classified_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_support_events_severity ON support_events (severity)`)
}

// ---------------------------------------------------------------------------
// SQLiteStore implementation
// ---------------------------------------------------------------------------

// UpsertEventFromWebhook — SQLite implementation.
func (s *SQLiteStore) UpsertEventFromWebhook(ctx context.Context, e SupportEvent) (SupportEvent, error) {
	now := time.Now().UTC()

	var dashContextJSON []byte
	if e.DashContext != nil {
		var err error
		dashContextJSON, err = json.Marshal(e.DashContext)
		if err != nil {
			return SupportEvent{}, fmt.Errorf("marshalling dash_context: %w", err)
		}
	}

	// Generate a random ID for new rows.
	id, err := randomID()
	if err != nil {
		return SupportEvent{}, fmt.Errorf("generating id: %w", err)
	}

	existing, err := s.GetByPlainThreadID(ctx, e.PlainThreadID)
	if err != nil {
		return SupportEvent{}, err
	}

	if existing != nil {
		// Idempotent update — only refresh intake_source and updated_at.
		_, err = s.db.ExecContext(ctx, `
			UPDATE support_events
			SET intake_source = ?,
			    updated_at    = ?
			WHERE plain_thread_id = ?`,
			e.IntakeSource, now, e.PlainThreadID,
		)
		if err != nil {
			return SupportEvent{}, err
		}
		return s.getByPlainThreadIDSQLite(ctx, e.PlainThreadID)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO support_events
			(id, plain_thread_id, user_id, intake_source, dash_context, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id,
		e.PlainThreadID,
		nullableStringPtr(e.UserID),
		e.IntakeSource,
		nullableBytes(dashContextJSON),
		now,
		now,
	)
	if err != nil {
		return SupportEvent{}, err
	}
	return s.getByPlainThreadIDSQLite(ctx, e.PlainThreadID)
}

// GetByPlainThreadID — SQLite implementation.
func (s *SQLiteStore) GetByPlainThreadID(ctx context.Context, id string) (*SupportEvent, error) {
	e, err := s.getByPlainThreadIDSQLite(ctx, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *SQLiteStore) getByPlainThreadIDSQLite(ctx context.Context, id string) (SupportEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, plain_thread_id, user_id, intake_source, dash_context,
		       classified_at, category, market, product_cat, severity, action_needed,
		       linear_issue, created_at, updated_at
		FROM support_events
		WHERE plain_thread_id = ?`,
		id,
	)
	return scanSupportEventSQLite(row)
}

// AttachClassification — SQLite implementation.
func (s *SQLiteStore) AttachClassification(ctx context.Context, plainThreadID string, c Classification) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE support_events
		SET classified_at = ?,
		    category      = ?,
		    market        = ?,
		    product_cat   = ?,
		    severity      = ?,
		    action_needed = ?,
		    updated_at    = ?
		WHERE plain_thread_id = ?`,
		c.ClassifiedAt,
		nullableString(c.Category),
		nullableString(c.Market),
		nullableString(c.ProductCat),
		nullableString(c.Severity),
		nullableString(c.ActionNeeded),
		now,
		plainThreadID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSupportEventNotFound
	}
	return nil
}

// AttachLinearIssue — SQLite implementation.
func (s *SQLiteStore) AttachLinearIssue(ctx context.Context, plainThreadID, linearIssue string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE support_events
		SET linear_issue = ?,
		    updated_at   = ?
		WHERE plain_thread_id = ?`,
		linearIssue,
		now,
		plainThreadID,
	)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSupportEventNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// SQLite scanning helper
// ---------------------------------------------------------------------------

func scanSupportEventSQLite(row interface {
	Scan(dest ...any) error
}) (SupportEvent, error) {
	// SQLite stores JSONB as TEXT; the scan shape is identical.
	return scanSupportEvent(row)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// nullableBytes returns nil when b is empty so sql.DB does not write an empty
// JSON byte slice — the DB column stays NULL.
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nullableString converts an empty string to nil so the DB column stays NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableStringPtr dereferences a *string and returns nil when the pointer is
// nil or points to an empty string.
func nullableStringPtr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrSupportEventNotFound is returned when a support event lookup finds no row.
var ErrSupportEventNotFound = fmt.Errorf("support event not found")
