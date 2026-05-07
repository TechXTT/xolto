-- XOL-181: revert additive columns added in up migration.

ALTER TABLE shopping_profiles
    DROP COLUMN IF EXISTS budget_min;

ALTER TABLE shopping_profiles
    DROP COLUMN IF EXISTS budget_currency;
