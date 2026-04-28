-- W19-27: initial tables that were always created inline in postgres.go but were
-- never written into a migration file. This migration makes migration files the
-- canonical source of truth for the full schema (Decision Log 2026-04-28).
--
-- All statements use CREATE TABLE IF NOT EXISTS / ALTER ... ADD COLUMN IF NOT EXISTS
-- so the migration is safe to apply against a production database that was bootstrapped
-- by the old inline path.
--
-- Tables covered (cross-referenced against inline migratePostgres block):
--   listings, price_history, shopping_profiles, shortlist_entries,
--   conversation_artifacts, assistant_sessions, action_log, stripe_events
--
-- Also captures inline column additions that post-date the initial CREATE but
-- predate their respective numbered migration files.

CREATE TABLE IF NOT EXISTS listings (
    item_id    TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    price      INTEGER NOT NULL,
    price_type TEXT NOT NULL DEFAULT '',
    score      DOUBLE PRECISION NOT NULL DEFAULT 0,
    reasoning_source TEXT NOT NULL DEFAULT '',
    offered    BOOLEAN NOT NULL DEFAULT FALSE,
    query      TEXT NOT NULL DEFAULT '',
    profile_id BIGINT NOT NULL DEFAULT 0,
    image_urls TEXT NOT NULL DEFAULT '[]',
    url        TEXT NOT NULL DEFAULT '',
    condition  TEXT NOT NULL DEFAULT '',
    marketplace_id TEXT NOT NULL DEFAULT 'olxbg',
    fair_price INTEGER NOT NULL DEFAULT 0,
    offer_price INTEGER NOT NULL DEFAULT 0,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    reasoning  TEXT NOT NULL DEFAULT '',
    risk_flags TEXT NOT NULL DEFAULT '[]',
    recommended_action TEXT NOT NULL DEFAULT 'ask_seller',
    comparables_count INTEGER NOT NULL DEFAULT 0,
    comparables_median_age_days INTEGER NOT NULL DEFAULT 0,
    feedback   TEXT NOT NULL DEFAULT '',
    feedback_at TIMESTAMPTZ NULL,
    currency_status TEXT NOT NULL DEFAULT '',
    outreach_status TEXT NOT NULL DEFAULT 'none',
    first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Additive column guards for databases bootstrapped with an older inline schema.
ALTER TABLE listings ADD COLUMN IF NOT EXISTS url TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS condition TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS marketplace_id TEXT NOT NULL DEFAULT 'olxbg';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS fair_price INTEGER NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS offer_price INTEGER NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS confidence DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS reasoning TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS risk_flags TEXT NOT NULL DEFAULT '[]';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS recommended_action TEXT NOT NULL DEFAULT 'ask_seller';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS comparables_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS comparables_median_age_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback_at TIMESTAMPTZ NULL;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS currency_status TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS outreach_status TEXT NOT NULL DEFAULT 'none';

CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_listings_feedback ON listings(profile_id, feedback);

CREATE TABLE IF NOT EXISTS price_history (
    id BIGSERIAL PRIMARY KEY,
    query TEXT NOT NULL,
    category_id INTEGER NOT NULL DEFAULT 0,
    price INTEGER NOT NULL,
    marketplace_id TEXT NOT NULL DEFAULT '',
    model_key TEXT NOT NULL DEFAULT '',
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE price_history ADD COLUMN IF NOT EXISTS marketplace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE price_history ADD COLUMN IF NOT EXISTS model_key TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_price_history_query ON price_history(query, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_price_history_model_key ON price_history(model_key, marketplace_id, timestamp DESC);

CREATE TABLE IF NOT EXISTS shopping_profiles (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    target_query TEXT NOT NULL DEFAULT '',
    category_id INTEGER NOT NULL DEFAULT 0,
    budget_max INTEGER NOT NULL DEFAULT 0,
    budget_stretch INTEGER NOT NULL DEFAULT 0,
    preferred_condition JSONB NOT NULL DEFAULT '[]'::jsonb,
    required_features JSONB NOT NULL DEFAULT '[]'::jsonb,
    nice_to_have JSONB NOT NULL DEFAULT '[]'::jsonb,
    risk_tolerance TEXT NOT NULL DEFAULT 'balanced',
    zip_code TEXT NOT NULL DEFAULT '',
    distance INTEGER NOT NULL DEFAULT 0,
    search_queries JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'active',
    urgency TEXT NOT NULL DEFAULT 'flexible',
    avoid_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
    travel_radius INTEGER NOT NULL DEFAULT 0,
    country_code TEXT NOT NULL DEFAULT '',
    region TEXT NOT NULL DEFAULT '',
    city TEXT NOT NULL DEFAULT '',
    postal_code TEXT NOT NULL DEFAULT '',
    cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    marketplace_scope JSONB NOT NULL DEFAULT '[]'::jsonb,
    category TEXT NOT NULL DEFAULT 'other',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    last_manual_recheck_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS urgency TEXT NOT NULL DEFAULT 'flexible';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS avoid_flags JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS travel_radius INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT '';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT '';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT '';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT '';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS marketplace_scope JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'other';
ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS last_manual_recheck_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_shopping_profiles_user ON shopping_profiles(user_id, active, updated_at DESC);

CREATE TABLE IF NOT EXISTS shortlist_entries (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    profile_id BIGINT NOT NULL DEFAULT 0,
    item_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    recommendation_label TEXT NOT NULL DEFAULT '',
    recommendation_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    ask_price INTEGER NOT NULL DEFAULT 0,
    fair_price INTEGER NOT NULL DEFAULT 0,
    verdict TEXT NOT NULL DEFAULT '',
    concerns JSONB NOT NULL DEFAULT '[]'::jsonb,
    suggested_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'watching',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, item_id)
);

CREATE INDEX IF NOT EXISTS idx_shortlist_entries_user ON shortlist_entries(user_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS conversation_artifacts (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    intent TEXT NOT NULL DEFAULT '',
    input_text TEXT NOT NULL DEFAULT '',
    output_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS assistant_sessions (
    user_id TEXT PRIMARY KEY,
    pending_intent TEXT NOT NULL DEFAULT '',
    pending_question TEXT NOT NULL DEFAULT '',
    draft_profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_assistant_msg TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS action_log (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    item_id TEXT NOT NULL DEFAULT '',
    action_type TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS stripe_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
