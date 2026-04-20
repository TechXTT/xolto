ALTER TABLE price_history ADD COLUMN IF NOT EXISTS model_key TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_price_history_model_key ON price_history(model_key, marketplace_id, timestamp DESC);
