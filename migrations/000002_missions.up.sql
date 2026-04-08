ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS urgency TEXT NOT NULL DEFAULT 'flexible';

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS avoid_flags JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS travel_radius INTEGER NOT NULL DEFAULT 0;

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'other';

ALTER TABLE search_configs
    ADD COLUMN IF NOT EXISTS profile_id BIGINT NOT NULL DEFAULT 0;

ALTER TABLE listings
    ADD COLUMN IF NOT EXISTS profile_id BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_search_configs_profile
    ON search_configs(profile_id, enabled, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_listings_profile
    ON listings(profile_id, last_seen DESC);
