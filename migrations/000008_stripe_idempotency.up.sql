CREATE TABLE IF NOT EXISTS stripe_processed_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stripe_processed_events_processed_at
    ON stripe_processed_events(processed_at DESC);
