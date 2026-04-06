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
