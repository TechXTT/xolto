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
	"time"
)

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
}

// ---------------------------------------------------------------------------
// PostgresStore implementation
// ---------------------------------------------------------------------------

func (s *PostgresStore) RecordAIBudgetOverride(ctx context.Context, o AIBudgetOverride) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO ai_budget_overrides (set_at, new_cap_usd, reason, set_by_user_id)
		VALUES (NOW(), $1, $2, $3)
		RETURNING id`,
		o.NewCapUSD, o.Reason, o.SetByUserID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("inserting ai_budget_override: %w", err)
	}
	return id, nil
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
