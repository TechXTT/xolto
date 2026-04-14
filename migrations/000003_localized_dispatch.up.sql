ALTER TABLE users
    ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS preferred_radius_km INTEGER NOT NULL DEFAULT 0;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT '';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT '';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT '';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT '';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS marketplace_scope JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT '';

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT '';

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT '';

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS radius_km INTEGER NOT NULL DEFAULT 0;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS priority_class INTEGER NOT NULL DEFAULT 0;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS next_run_at TIMESTAMPTZ NULL;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS last_run_at TIMESTAMPTZ NULL;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS last_signal_at TIMESTAMPTZ NULL;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ NULL;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS last_result_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS consecutive_empty_runs INTEGER NOT NULL DEFAULT 0;

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_search_configs_due
    ON search_configs(enabled, next_run_at, user_id);

CREATE TABLE IF NOT EXISTS user_auth_identities (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    provider_subject TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider, provider_subject),
    UNIQUE(user_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_user_auth_identities_user
    ON user_auth_identities(user_id, provider);

CREATE TABLE IF NOT EXISTS search_run_log (
    id BIGSERIAL PRIMARY KEY,
    search_config_id BIGINT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    mission_id BIGINT NOT NULL DEFAULT 0,
    plan TEXT NOT NULL DEFAULT '',
    marketplace_id TEXT NOT NULL DEFAULT '',
    country_code TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    queue_wait_ms INTEGER NOT NULL DEFAULT 0,
    priority INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT '',
    results_found INTEGER NOT NULL DEFAULT 0,
    new_listings INTEGER NOT NULL DEFAULT 0,
    deal_hits INTEGER NOT NULL DEFAULT 0,
    throttled BOOLEAN NOT NULL DEFAULT FALSE,
    error_code TEXT NOT NULL DEFAULT '',
    searches_avoided INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_search_run_log_started
    ON search_run_log(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_search_run_log_marketplace
    ON search_run_log(marketplace_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_search_run_log_country
    ON search_run_log(country_code, started_at DESC);
