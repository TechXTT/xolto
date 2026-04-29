-- XOL-53 SUP-2: support_events table for Plain webhook intake and dash contact reports.
-- plain_thread_id has a UNIQUE constraint to enable idempotent upsert.

CREATE TABLE IF NOT EXISTS support_events (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  plain_thread_id   TEXT NOT NULL,
  user_id           TEXT REFERENCES users(id),
  intake_source     TEXT NOT NULL,
  dash_context      JSONB,
  classified_at     TIMESTAMPTZ,
  category          TEXT,
  market            TEXT,
  product_cat       TEXT,
  severity          TEXT,
  action_needed     TEXT,
  linear_issue      TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (plain_thread_id)
);

CREATE INDEX IF NOT EXISTS idx_support_events_user ON support_events (user_id);
CREATE INDEX IF NOT EXISTS idx_support_events_classified_at ON support_events (classified_at);
CREATE INDEX IF NOT EXISTS idx_support_events_severity ON support_events (severity);
