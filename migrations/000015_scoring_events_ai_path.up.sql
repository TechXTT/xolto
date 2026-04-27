-- W19-23 Phase 1 (VAL-1 contamination guard).
--
-- Adds ai_path column to scoring_events so the calibration dashboard can
-- exclude rows produced via the W19-23 global AI-spend cap heuristic
-- fallback. Without this filter, a cap-fire incident would silently shift
-- VAL-0 verdict-correctness measurements (verdict-to-action >= 50%,
-- verdict-changed >= 30%) toward heuristic-only data.
--
-- Backfill: existing rows are tagged "ai" — the historical default for
-- pre-cap-fire data. New rows are written with either "ai" (real LLM call)
-- or "heuristic_fallback" (cap fired before LLM call) by the scorer.
--
-- Decision Log 2026-04-27. Founder-locked $3/24h cap. Do NOT raise the cap
-- via this column or any other path; the cap value is mutated only via
-- the owner-override audit-logged endpoint.
--
-- This migration is additive (column add + default backfill) and does not
-- touch indexes — calibration queries that filter by ai_path are linear-
-- scan acceptable at the current scoring_events size; if/when row count
-- demands it, follow up with idx_scoring_events_ai_path.

ALTER TABLE scoring_events
  ADD COLUMN IF NOT EXISTS ai_path TEXT NOT NULL DEFAULT 'ai';
