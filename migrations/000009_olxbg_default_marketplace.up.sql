-- XOL-50 M5-F: flip search_configs.marketplace_id default from 'marktplaats' to 'olxbg'
-- to align with the BG/OLX wedge (2026-04-17 replacement of NL/Marktplaats).
-- Additive: only affects NEW rows inserted without an explicit marketplace_id.
-- Existing rows are unchanged.

ALTER TABLE search_configs ALTER COLUMN marketplace_id SET DEFAULT 'olxbg';
