-- Reversible only for net-new tables; existing tables bootstrapped via the old
-- inline path are not dropped here to avoid data loss on partial reversal.
-- The additive ALTER TABLE / CREATE INDEX statements in the up migration are
-- not reversed because IF NOT EXISTS guards make them idempotent.
DROP TABLE IF EXISTS stripe_events;
DROP TABLE IF EXISTS action_log;
DROP TABLE IF EXISTS assistant_sessions;
DROP TABLE IF EXISTS conversation_artifacts;
DROP TABLE IF EXISTS shortlist_entries;
DROP TABLE IF EXISTS shopping_profiles;
DROP TABLE IF EXISTS price_history;
DROP TABLE IF EXISTS listings;
