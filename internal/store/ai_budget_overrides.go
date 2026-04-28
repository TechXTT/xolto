// Package store — ai_budget_overrides audit log (W19-23 Phase 1).
//
// Each row records one owner-role override of the global AI-spend cap.
// The in-memory aibudget.Tracker is the source of truth for the active
// cap value; this table is the audit log of who changed it and why.
//
// Persistence semantics: overrides do NOT survive process restart in v1
// (the cap value is in-memory). On restart the cap reverts to the
// founder-locked $3/24h default. Recording the override here lets the
// admin tile show "recent_overrides" so the operator can see drift even
// after a restart, and the founder can audit changes.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// createAIBudgetOverridesTablePostgres is the canonical Postgres DDL for the
// audit-log table. Kept in sync with migratePostgresCalibration so a self-heal
// path can recreate the table when an INSERT discovers it is missing — the
// 2026-04-27 incident showed migratePostgresCalibration's silent error pattern
// can leave the table absent without any deploy-time signal.
const createAIBudgetOverridesTablePostgres = `
CREATE TABLE IF NOT EXISTS ai_budget_overrides (
	id              BIGSERIAL PRIMARY KEY,
	set_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	new_cap_usd     DOUBLE PRECISION NOT NULL,
	reason          TEXT        NOT NULL DEFAULT '',
	set_by_user_id  TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ai_budget_overrides_set_at ON ai_budget_overrides (set_at DESC);
`

// AIBudgetOverride is one audit row.
type AIBudgetOverride struct {
	ID           int64
	SetAt        time.Time
	NewCapUSD    float64
	Reason       string
	SetByUserID  string
}

// AIBudgetOverrideStore is the interface for ai_budget_overrides persistence.
type AIBudgetOverrideStore interface {
	RecordAIBudgetOverride(ctx context.Context, o AIBudgetOverride) (int64, error)
	ListRecentAIBudgetOverrides(ctx context.Context, limit int) ([]AIBudgetOverride, error)
	// AIBudgetTableReady probes the underlying ai_budget_overrides table without
	// self-heal — returns nil when the table exists and is queryable, an error
	// otherwise. Used by /healthz and by the startup assertion in main.go to
	// fail-loud when migrations did not apply (the 2026-04-27 silent-migration
	// incident class). Distinct from the read methods which self-heal so the
	// admin tile keeps rendering even mid-incident.
	AIBudgetTableReady(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// PostgresStore implementation
// ---------------------------------------------------------------------------

// AIBudgetTableReady runs a SELECT against ai_budget_overrides to confirm the
// table is queryable. Returns nil when ready, the underlying driver error
// otherwise. No self-heal — this is the canonical "did the migration apply"
// probe consumed by /healthz and the startup assertion.
func (s *PostgresStore) AIBudgetTableReady(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `SELECT 1 FROM ai_budget_overrides LIMIT 0`)
	return err
}

func (s *PostgresStore) RecordAIBudgetOverride(ctx context.Context, o AIBudgetOverride) (int64, error) {
	var id int64
	insertSQL := `
		INSERT INTO ai_budget_overrides (set_at, new_cap_usd, reason, set_by_user_id)
		VALUES (NOW(), $1, $2, $3)
		RETURNING id`
	err := s.db.QueryRowContext(ctx, insertSQL, o.NewCapUSD, o.Reason, o.SetByUserID).Scan(&id)
	if err != nil && isRelationDoesNotExistErr(err) {
		// Self-heal: migratePostgresCalibration silently swallows DDL errors
		// (the 2026-04-27 incident proved this). If the table is missing at
		// INSERT time, run the canonical CREATE inline once with explicit
		// error-logging, then retry the INSERT. This unblocks the audit-log
		// without requiring a redeploy and surfaces the original DDL error
		// (if any) into the slog stream.
		slog.Warn("ai_budget_overrides table missing at INSERT time; attempting self-heal CREATE",
			"original_error", err.Error())
		if _, cerr := s.db.ExecContext(ctx, createAIBudgetOverridesTablePostgres); cerr != nil {
			slog.Error("ai_budget_overrides self-heal CREATE failed",
				"create_error", cerr.Error(), "original_error", err.Error())
			return 0, fmt.Errorf("self-heal create ai_budget_overrides: %w", cerr)
		}
		slog.Info("ai_budget_overrides self-heal CREATE succeeded; retrying INSERT")
		err = s.db.QueryRowContext(ctx, insertSQL, o.NewCapUSD, o.Reason, o.SetByUserID).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("inserting ai_budget_override: %w", err)
	}
	return id, nil
}

// isRelationDoesNotExistErr returns true when err is the Postgres
// "relation does not exist" error (SQLSTATE 42P01). The driver wraps it as a
// pgconn.PgError but the wrapped error string contains the SQLSTATE; matching
// on the string is robust without taking a hard pgconn dependency at this
// boundary.
func isRelationDoesNotExistErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "42P01") || strings.Contains(s, "does not exist")
}

func (s *PostgresStore) ListRecentAIBudgetOverrides(ctx context.Context, limit int) ([]AIBudgetOverride, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, set_at, new_cap_usd, reason, set_by_user_id
		FROM ai_budget_overrides
		ORDER BY set_at DESC
		LIMIT $1`, limit,
	)
	if err != nil {
		// If the table is missing (silent migration miss), surface an empty
		// list rather than an error — the admin tile renders "no recent
		// overrides" cleanly. The next override INSERT will self-heal.
		if isRelationDoesNotExistErr(err) {
			slog.Warn("ai_budget_overrides table missing at SELECT time; returning empty list",
				"error", err.Error())
			return nil, nil
		}
		return nil, fmt.Errorf("querying ai_budget_overrides: %w", err)
	}
	defer rows.Close()

	var out []AIBudgetOverride
	for rows.Next() {
		var o AIBudgetOverride
		if err := rows.Scan(&o.ID, &o.SetAt, &o.NewCapUSD, &o.Reason, &o.SetByUserID); err != nil {
			return nil, fmt.Errorf("scanning ai_budget_override: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// SQLiteStore implementation (dev / test path)
// ---------------------------------------------------------------------------

// AIBudgetTableReady runs a SELECT against ai_budget_overrides on the SQLite
// store. Mirrors the Postgres semantics for parity. Returns nil when ready,
// the driver error otherwise.
func (s *SQLiteStore) AIBudgetTableReady(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `SELECT 1 FROM ai_budget_overrides LIMIT 0`)
	return err
}

func (s *SQLiteStore) RecordAIBudgetOverride(ctx context.Context, o AIBudgetOverride) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_budget_overrides (set_at, new_cap_usd, reason, set_by_user_id)
		VALUES (datetime('now'), ?, ?, ?)`,
		o.NewCapUSD, o.Reason, o.SetByUserID,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting ai_budget_override: %w", err)
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) ListRecentAIBudgetOverrides(ctx context.Context, limit int) ([]AIBudgetOverride, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, set_at, new_cap_usd, reason, set_by_user_id
		FROM ai_budget_overrides
		ORDER BY set_at DESC
		LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying ai_budget_overrides: %w", err)
	}
	defer rows.Close()

	var out []AIBudgetOverride
	for rows.Next() {
		var (
			o      AIBudgetOverride
			setAt  string
		)
		if err := rows.Scan(&o.ID, &setAt, &o.NewCapUSD, &o.Reason, &o.SetByUserID); err != nil {
			return nil, fmt.Errorf("scanning ai_budget_override: %w", err)
		}
		// SQLite stores datetimes as text; parse to time.Time.
		if t, perr := time.Parse("2006-01-02 15:04:05", setAt); perr == nil {
			o.SetAt = t
		} else if t, perr := time.Parse(time.RFC3339, setAt); perr == nil {
			o.SetAt = t
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
