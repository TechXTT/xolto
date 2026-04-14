ALTER TABLE ai_usage_log
    ADD COLUMN IF NOT EXISTS mission_id BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_ai_usage_user_mission
    ON ai_usage_log(user_id, mission_id, created_at DESC);
