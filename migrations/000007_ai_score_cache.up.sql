CREATE TABLE IF NOT EXISTS ai_score_cache (
    key TEXT PRIMARY KEY,
    score DOUBLE PRECISION NOT NULL DEFAULT 0,
    reasoning TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT (EXTRACT(EPOCH FROM NOW()))::BIGINT,
    prompt_version INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_ai_score_cache_created
    ON ai_score_cache(created_at DESC);
