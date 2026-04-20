DROP INDEX IF EXISTS idx_price_history_model_key;
ALTER TABLE price_history DROP COLUMN IF EXISTS model_key;
