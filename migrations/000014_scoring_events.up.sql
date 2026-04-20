-- VAL-1a: scoring_events table for calibration dashboard persistence.
-- One row per listing scored at worker time. Best-effort write — scoring
-- is on the critical path; calibration persistence is not.
--
-- contributions is JSONB (the same map emitted by VAL-2 score_attribution log).
-- scorer_version is a stable identifier for the scoring formula; hardcoded for
-- now but forward-compatible for future scorer changes.

CREATE TABLE scoring_events (
  id               BIGSERIAL PRIMARY KEY,
  listing_id       TEXT        NOT NULL,
  marketplace      TEXT        NOT NULL DEFAULT '',
  mission_id       BIGINT,
  score            DOUBLE PRECISION NOT NULL DEFAULT 0,
  verdict          TEXT        NOT NULL DEFAULT '',
  confidence       DOUBLE PRECISION NOT NULL DEFAULT 0,
  contributions    JSONB       NOT NULL DEFAULT '{}'::jsonb,
  scorer_version   TEXT        NOT NULL DEFAULT 'v1',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scoring_events_created_at   ON scoring_events (created_at DESC);
CREATE INDEX idx_scoring_events_marketplace  ON scoring_events (marketplace, created_at DESC);
CREATE INDEX idx_scoring_events_listing_id   ON scoring_events (listing_id);
