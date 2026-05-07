-- XOL-181: additive columns for BGN currency storage and budget range support.
-- Existing rows are left with NULL currency (read back as legacy EUR by application code).
-- No backfill here; founder decides backfill scope separately for pre-BG-pivot rows.

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS budget_min INT NULL DEFAULT NULL;

ALTER TABLE shopping_profiles
    ADD COLUMN IF NOT EXISTS budget_currency VARCHAR(3) NULL DEFAULT NULL;
