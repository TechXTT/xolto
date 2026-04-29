CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    tier TEXT NOT NULL DEFAULT 'free',
    stripe_customer_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS search_configs (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    query TEXT NOT NULL DEFAULT '',
    marketplace_id TEXT NOT NULL DEFAULT 'marktplaats',
    category_id INTEGER NOT NULL DEFAULT 0,
    max_price INTEGER NOT NULL DEFAULT 0,
    min_price INTEGER NOT NULL DEFAULT 0,
    condition_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    offer_percentage INTEGER NOT NULL DEFAULT 70,
    auto_message BOOLEAN NOT NULL DEFAULT FALSE,
    message_template TEXT NOT NULL DEFAULT '',
    attributes_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    check_interval_seconds INTEGER NOT NULL DEFAULT 300,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- W19-30: tables that historically came from inline CREATE TABLE in postgres.go
-- (removed in W19-27). Migrations 000002+ ALTER these tables, so they MUST exist
-- before 000002 runs. Production already has these (applied via the inline path
-- before W19-27), so this block is a no-op there; CI fresh-Postgres needs them.
-- Migration 000017 still creates them with full ALTER guards for prod-state
-- bootstrapped databases — that path stays as defence-in-depth.

CREATE TABLE IF NOT EXISTS listings (
    item_id    TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    price      INTEGER NOT NULL,
    price_type TEXT NOT NULL DEFAULT '',
    score      DOUBLE PRECISION NOT NULL DEFAULT 0,
    reasoning_source TEXT NOT NULL DEFAULT '',
    offered    BOOLEAN NOT NULL DEFAULT FALSE,
    query      TEXT NOT NULL DEFAULT '',
    first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS price_history (
    id BIGSERIAL PRIMARY KEY,
    query TEXT NOT NULL,
    category_id INTEGER NOT NULL DEFAULT 0,
    price INTEGER NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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
