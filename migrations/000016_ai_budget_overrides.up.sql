-- W19-23 Phase 1: ai_budget_overrides audit log.
--
-- Each row is one owner-role override of the global AI-spend cap. The
-- in-memory aibudget.Tracker is the source of truth for the active cap
-- value; this table is the audit log of who changed it and why.
--
-- Override semantics:
--   * new_cap_usd > 0 and new_cap_usd <= 100 (sanity hard ceiling).
--   * The cap defaults to $3 USD/24h (founder-locked, Decision Log
--     2026-04-27). Process restart resets to default — overrides do NOT
--     persist across restarts in v1. This row IS the audit; the
--     in-memory cap value is consulted at request time.
--   * Lifting the cap above $3 should be temporary and rare. The 100x
--     hard ceiling above prevents accidental order-of-magnitude lifts.

CREATE TABLE IF NOT EXISTS ai_budget_overrides (
  id              BIGSERIAL PRIMARY KEY,
  set_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  new_cap_usd     DOUBLE PRECISION NOT NULL,
  reason          TEXT        NOT NULL DEFAULT '',
  set_by_user_id  TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ai_budget_overrides_set_at ON ai_budget_overrides (set_at DESC);
