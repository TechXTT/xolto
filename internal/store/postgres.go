package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

var _ Store = (*PostgresStore)(nil)

func NewPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	return NewPostgresWithPool(ctx, databaseURL, DefaultDBPoolConfig())
}

func NewPostgresWithPool(ctx context.Context, databaseURL string, poolCfg DBPoolConfig) (*PostgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("opening postgres database: %w", err)
	}
	applyDBPoolConfig(db, poolCfg)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging postgres database: %w", err)
	}
	if err := migratePostgres(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running postgres migrations: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func migratePostgres(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
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

		CREATE INDEX IF NOT EXISTS idx_price_history_query ON price_history(query, timestamp DESC);

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
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

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

		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			tier TEXT NOT NULL DEFAULT 'free',
			role TEXT NOT NULL DEFAULT '',
			stripe_customer_id TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			postal_code TEXT NOT NULL DEFAULT '',
			preferred_radius_km INTEGER NOT NULL DEFAULT 0,
			cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS search_configs (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			profile_id BIGINT NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			query TEXT NOT NULL DEFAULT '',
			marketplace_id TEXT NOT NULL DEFAULT 'marktplaats',
			country_code TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			postal_code TEXT NOT NULL DEFAULT '',
			radius_km INTEGER NOT NULL DEFAULT 0,
			category_id INTEGER NOT NULL DEFAULT 0,
			max_price INTEGER NOT NULL DEFAULT 0,
			min_price INTEGER NOT NULL DEFAULT 0,
			condition_json JSONB NOT NULL DEFAULT '[]'::jsonb,
			offer_percentage INTEGER NOT NULL DEFAULT 70,
			auto_message BOOLEAN NOT NULL DEFAULT FALSE,
			message_template TEXT NOT NULL DEFAULT '',
			attributes_json JSONB NOT NULL DEFAULT '{}'::jsonb,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			check_interval_seconds BIGINT NOT NULL DEFAULT 300,
			priority_class INTEGER NOT NULL DEFAULT 0,
			next_run_at TIMESTAMPTZ NULL,
			last_run_at TIMESTAMPTZ NULL,
			last_signal_at TIMESTAMPTZ NULL,
			last_error_at TIMESTAMPTZ NULL,
			last_result_count INTEGER NOT NULL DEFAULT 0,
			consecutive_empty_runs INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_search_configs_user ON search_configs(user_id, enabled, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_search_configs_profile ON search_configs(profile_id, enabled, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen DESC);

		CREATE TABLE IF NOT EXISTS stripe_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS stripe_processed_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_processed_events_processed_at ON stripe_processed_events(processed_at DESC);

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

		CREATE INDEX IF NOT EXISTS idx_user_auth_identities_user ON user_auth_identities(user_id, provider);

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

		CREATE INDEX IF NOT EXISTS idx_search_run_log_started ON search_run_log(started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_search_run_log_marketplace ON search_run_log(marketplace_id, started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_search_run_log_country ON search_run_log(country_code, started_at DESC);

		CREATE TABLE IF NOT EXISTS admin_audit_log (
			id BIGSERIAL PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			before_json TEXT NOT NULL DEFAULT '',
			after_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created ON admin_audit_log(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor ON admin_audit_log(actor_user_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_webhook_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			event_type TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			api_account TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'received',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 1,
			payload_json TEXT NOT NULL DEFAULT '',
			received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_received ON stripe_webhook_events(received_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_status ON stripe_webhook_events(status, received_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_subscription_snapshots (
			subscription_id TEXT PRIMARY KEY,
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			plan_interval TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount BIGINT NOT NULL DEFAULT 0,
			quantity BIGINT NOT NULL DEFAULT 0,
			current_period_start TIMESTAMPTZ NULL,
			current_period_end TIMESTAMPTZ NULL,
			cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
			canceled_at TIMESTAMPTZ NULL,
			paused BOOLEAN NOT NULL DEFAULT FALSE,
			latest_invoice_id TEXT NOT NULL DEFAULT '',
			default_payment_method TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_customer ON stripe_subscription_snapshots(customer_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_status ON stripe_subscription_snapshots(status, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_user ON stripe_subscription_snapshots(user_id, updated_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_subscription_history (
			id BIGSERIAL PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			event_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount BIGINT NOT NULL DEFAULT 0,
			quantity BIGINT NOT NULL DEFAULT 0,
			period_start TIMESTAMPTZ NULL,
			period_end TIMESTAMPTZ NULL,
			cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_history_sub ON stripe_subscription_history(subscription_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_invoice_summaries (
			invoice_id TEXT PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			amount_due BIGINT NOT NULL DEFAULT 0,
			amount_paid BIGINT NOT NULL DEFAULT 0,
			amount_remaining BIGINT NOT NULL DEFAULT 0,
			attempt_count BIGINT NOT NULL DEFAULT 0,
			paid BOOLEAN NOT NULL DEFAULT FALSE,
			hosted_invoice_url TEXT NOT NULL DEFAULT '',
			invoice_pdf TEXT NOT NULL DEFAULT '',
			period_start TIMESTAMPTZ NULL,
			period_end TIMESTAMPTZ NULL,
			due_date TIMESTAMPTZ NULL,
			finalized_at TIMESTAMPTZ NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_customer ON stripe_invoice_summaries(customer_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_status ON stripe_invoice_summaries(status, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_subscription ON stripe_invoice_summaries(subscription_id, updated_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_mutation_log (
			idempotency_key TEXT PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_mutation_log_actor ON stripe_mutation_log(actor_user_id, created_at DESC);

			CREATE TABLE IF NOT EXISTS billing_reconcile_runs (
				id BIGSERIAL PRIMARY KEY,
				triggered_by TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				summary_json TEXT NOT NULL DEFAULT '',
				error_json TEXT NOT NULL DEFAULT '',
				started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				finished_at TIMESTAMPTZ NULL
			);
			CREATE INDEX IF NOT EXISTS idx_billing_reconcile_runs_started ON billing_reconcile_runs(started_at DESC);

			CREATE TABLE IF NOT EXISTS ai_score_cache (
				key TEXT PRIMARY KEY,
				score DOUBLE PRECISION NOT NULL DEFAULT 0,
				reasoning TEXT NOT NULL DEFAULT '',
				created_at BIGINT NOT NULL DEFAULT (EXTRACT(EPOCH FROM NOW()))::BIGINT,
				prompt_version INTEGER NOT NULL DEFAULT 1
			);
			CREATE INDEX IF NOT EXISTS idx_ai_score_cache_created ON ai_score_cache(created_at DESC);
		`)
	if err != nil {
		return err
	}
	// Add image_urls column to existing databases that pre-date this field.
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS image_urls TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS url TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS condition TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS marketplace_id TEXT NOT NULL DEFAULT 'marktplaats'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS fair_price INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS offer_price INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS confidence DOUBLE PRECISION NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS reasoning TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS reasoning_source TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS risk_flags TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS recommended_action TEXT NOT NULL DEFAULT 'ask_seller'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS comparables_count INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS comparables_median_age_days INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS profile_id BIGINT NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_listings_feedback ON listings(profile_id, feedback)`)
	// XOL-33 (M2-D): currency_status tracks how the listing price was normalised
	// from the marketplace native currency into EUR cents. Empty string for rows
	// ingested before this migration (non-OLX or pre-fix OLX listings).
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS currency_status TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS urgency TEXT NOT NULL DEFAULT 'flexible'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS avoid_flags JSONB NOT NULL DEFAULT '[]'::jsonb`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS travel_radius INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS marketplace_scope JSONB NOT NULL DEFAULT '[]'::jsonb`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'other'`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_radius_km INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS cross_border_enabled BOOLEAN NOT NULL DEFAULT FALSE`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS profile_id BIGINT NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS radius_km INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS priority_class INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS next_run_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS last_run_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS last_signal_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS last_result_count INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS consecutive_empty_runs INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE search_configs ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_search_configs_profile ON search_configs(profile_id, enabled, updated_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_search_configs_due ON search_configs(enabled, next_run_at, user_id)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen DESC)`)

	// Admin & AI usage tracking.
	_, _ = db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE`)
	_, _ = db.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE is_admin = TRUE AND COALESCE(role, '') = ''`)
	_, _ = db.ExecContext(ctx, `UPDATE users SET role = 'user' WHERE is_admin = FALSE AND COALESCE(role, '') = ''`)
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_usage_log (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT '',
			mission_id BIGINT NOT NULL DEFAULT 0,
			call_type TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			success BOOLEAN NOT NULL DEFAULT TRUE,
			error_msg TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
		`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE ai_usage_log ADD COLUMN IF NOT EXISTS mission_id BIGINT NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_usage_user ON ai_usage_log(user_id, created_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_usage_user_mission ON ai_usage_log(user_id, mission_id, created_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_usage_created ON ai_usage_log(created_at DESC)`)
	_, _ = db.ExecContext(ctx, `
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
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_user_auth_identities_user ON user_auth_identities(user_id, provider)`)
	_, _ = db.ExecContext(ctx, `
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
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_search_run_log_started ON search_run_log(started_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_search_run_log_marketplace ON search_run_log(marketplace_id, started_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_search_run_log_country ON search_run_log(country_code, started_at DESC)`)
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS admin_audit_log (
			id BIGSERIAL PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			before_json TEXT NOT NULL DEFAULT '',
			after_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created ON admin_audit_log(created_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor ON admin_audit_log(actor_user_id, created_at DESC)`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE admin_audit_log ADD COLUMN IF NOT EXISTS actor_role TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE admin_audit_log ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT ''`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_webhook_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			event_type TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			api_account TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'received',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 1,
			payload_json TEXT NOT NULL DEFAULT '',
			received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ NULL
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_received ON stripe_webhook_events(received_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_status ON stripe_webhook_events(status, received_at DESC)`)
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_processed_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_processed_events_processed_at ON stripe_processed_events(processed_at DESC)`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_subscription_snapshots (
			subscription_id TEXT PRIMARY KEY,
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			plan_interval TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount BIGINT NOT NULL DEFAULT 0,
			quantity BIGINT NOT NULL DEFAULT 0,
			current_period_start TIMESTAMPTZ NULL,
			current_period_end TIMESTAMPTZ NULL,
			cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
			canceled_at TIMESTAMPTZ NULL,
			paused BOOLEAN NOT NULL DEFAULT FALSE,
			latest_invoice_id TEXT NOT NULL DEFAULT '',
			default_payment_method TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_customer ON stripe_subscription_snapshots(customer_id, updated_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_status ON stripe_subscription_snapshots(status, updated_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_user ON stripe_subscription_snapshots(user_id, updated_at DESC)`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_subscription_history (
			id BIGSERIAL PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			event_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount BIGINT NOT NULL DEFAULT 0,
			quantity BIGINT NOT NULL DEFAULT 0,
			period_start TIMESTAMPTZ NULL,
			period_end TIMESTAMPTZ NULL,
			cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_subscription_history_sub ON stripe_subscription_history(subscription_id, created_at DESC)`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_invoice_summaries (
			invoice_id TEXT PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			amount_due BIGINT NOT NULL DEFAULT 0,
			amount_paid BIGINT NOT NULL DEFAULT 0,
			amount_remaining BIGINT NOT NULL DEFAULT 0,
			attempt_count BIGINT NOT NULL DEFAULT 0,
			paid BOOLEAN NOT NULL DEFAULT FALSE,
			hosted_invoice_url TEXT NOT NULL DEFAULT '',
			invoice_pdf TEXT NOT NULL DEFAULT '',
			period_start TIMESTAMPTZ NULL,
			period_end TIMESTAMPTZ NULL,
			due_date TIMESTAMPTZ NULL,
			finalized_at TIMESTAMPTZ NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_customer ON stripe_invoice_summaries(customer_id, updated_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_status ON stripe_invoice_summaries(status, updated_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_subscription ON stripe_invoice_summaries(subscription_id, updated_at DESC)`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stripe_mutation_log (
			idempotency_key TEXT PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_stripe_mutation_log_actor ON stripe_mutation_log(actor_user_id, created_at DESC)`)

	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS billing_reconcile_runs (
			id BIGSERIAL PRIMARY KEY,
			triggered_by TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			summary_json TEXT NOT NULL DEFAULT '',
			error_json TEXT NOT NULL DEFAULT '',
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			finished_at TIMESTAMPTZ NULL
		)
	`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_billing_reconcile_runs_started ON billing_reconcile_runs(started_at DESC)`)

	// XOL-24: outreach thread reply-time tracking.
	migrateOutreachThreadsPostgres(ctx, db)

	// XOL-53 SUP-2: support events intake table.
	migratePostgresSupportEvents(ctx, db)

	// XOL-71: isolate price_history by marketplace_id.
	_, _ = db.ExecContext(ctx, `ALTER TABLE price_history ADD COLUMN IF NOT EXISTS marketplace_id TEXT NOT NULL DEFAULT ''`)

	// XOL-79 (C-6): outreach lifecycle status per saved listing.
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS outreach_status TEXT NOT NULL DEFAULT 'none'`)

	// XOL-101: manual recheck rate-limit timestamp on missions.
	_, _ = db.ExecContext(ctx, `ALTER TABLE shopping_profiles ADD COLUMN IF NOT EXISTS last_manual_recheck_at TIMESTAMPTZ`)

	// XOL-105: per-model comparables pool key on price_history.
	_, _ = db.ExecContext(ctx, `ALTER TABLE price_history ADD COLUMN IF NOT EXISTS model_key TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_price_history_model_key ON price_history(model_key, marketplace_id, timestamp DESC)`)

	return nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) UpsertMission(mission models.Mission) (int64, error) {
	preferredJSON, _ := json.Marshal(mission.PreferredCondition)
	requiredJSON, _ := json.Marshal(mission.RequiredFeatures)
	niceJSON, _ := json.Marshal(mission.NiceToHave)
	queriesJSON, _ := json.Marshal(mission.SearchQueries)
	avoidFlagsJSON, _ := json.Marshal(mission.AvoidFlags)
	marketplaceScopeJSON, _ := json.Marshal(normalizeMarketplaceScope(mission.MarketplaceScope))

	if strings.TrimSpace(mission.Status) == "" {
		mission.Status = "active"
	}
	if strings.TrimSpace(mission.Urgency) == "" {
		mission.Urgency = "flexible"
	}
	if strings.TrimSpace(mission.Category) == "" {
		mission.Category = "other"
	}
	if mission.TravelRadius == 0 && mission.Distance > 0 {
		mission.TravelRadius = mission.Distance / 1000
	}

	if mission.ID > 0 {
		_, err := s.db.Exec(`
			UPDATE shopping_profiles
			SET name = $1, target_query = $2, category_id = $3, budget_max = $4, budget_stretch = $5,
			    preferred_condition = $6::jsonb, required_features = $7::jsonb, nice_to_have = $8::jsonb,
			    risk_tolerance = $9, zip_code = $10, distance = $11, search_queries = $12::jsonb,
			    status = $13, urgency = $14, avoid_flags = $15::jsonb, travel_radius = $16, country_code = $17,
			    region = $18, city = $19, postal_code = $20, cross_border_enabled = $21, marketplace_scope = $22::jsonb,
			    category = $23, active = $24, updated_at = NOW()
			WHERE id = $25
		`,
			mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
			string(preferredJSON), string(requiredJSON), string(niceJSON), mission.RiskTolerance,
			mission.ZipCode, mission.Distance, string(queriesJSON),
			mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius,
			strings.ToUpper(strings.TrimSpace(mission.CountryCode)), strings.TrimSpace(mission.Region), strings.TrimSpace(mission.City),
			strings.TrimSpace(mission.PostalCode), mission.CrossBorderEnabled, string(marketplaceScopeJSON),
			mission.Category, mission.Active, mission.ID,
		)
		return mission.ID, err
	}

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO shopping_profiles (
			user_id, name, target_query, category_id, budget_max, budget_stretch,
			preferred_condition, required_features, nice_to_have, risk_tolerance,
			zip_code, distance, search_queries,
			status, urgency, avoid_flags, travel_radius, country_code, region, city, postal_code,
			cross_border_enabled, marketplace_scope, category,
			active
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13::jsonb, $14, $15, $16::jsonb, $17, $18, $19, $20, $21, $22, $23::jsonb, $24, $25)
		RETURNING id
	`,
		mission.UserID, mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
		string(preferredJSON), string(requiredJSON), string(niceJSON), mission.RiskTolerance,
		mission.ZipCode, mission.Distance, string(queriesJSON),
		mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius,
		strings.ToUpper(strings.TrimSpace(mission.CountryCode)), strings.TrimSpace(mission.Region), strings.TrimSpace(mission.City),
		strings.TrimSpace(mission.PostalCode), mission.CrossBorderEnabled, string(marketplaceScopeJSON), mission.Category,
		mission.Active,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetActiveMission(userID string) (*models.Mission, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, name, target_query, category_id, budget_max, budget_stretch,
		       preferred_condition::text, required_features::text, nice_to_have::text, risk_tolerance,
		       zip_code, distance, search_queries::text,
		       status, urgency, avoid_flags::text, travel_radius, country_code, region, city, postal_code,
		       cross_border_enabled, marketplace_scope::text, category,
		       active, created_at, updated_at
		FROM shopping_profiles
		WHERE user_id = $1 AND active = TRUE AND status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1
	`, userID)
	mission, err := scanPGMission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mission, nil
}

func (s *PostgresStore) GetMission(id int64) (*models.Mission, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, name, target_query, category_id, budget_max, budget_stretch,
		       preferred_condition::text, required_features::text, nice_to_have::text, risk_tolerance,
		       zip_code, distance, search_queries::text,
		       status, urgency, avoid_flags::text, travel_radius, country_code, region, city, postal_code,
		       cross_border_enabled, marketplace_scope::text, category,
		       active, created_at, updated_at
		FROM shopping_profiles
		WHERE id = $1
	`, id)
	mission, err := scanPGMission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mission, nil
}

func (s *PostgresStore) ListMissions(userID string) ([]models.Mission, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.user_id, m.name, m.target_query, m.category_id, m.budget_max, m.budget_stretch,
		       m.preferred_condition::text, m.required_features::text, m.nice_to_have::text, m.risk_tolerance,
		       m.zip_code, m.distance, m.search_queries::text,
		       m.status, m.urgency, m.avoid_flags::text, m.travel_radius, m.country_code, m.region, m.city, m.postal_code,
		       m.cross_border_enabled, m.marketplace_scope::text, m.category,
		       m.active, m.created_at, m.updated_at,
		       COUNT(l.item_id) AS match_count,
		       COALESCE(MAX(l.last_seen), TO_TIMESTAMP(0)) AS last_match_at
		FROM shopping_profiles m
		LEFT JOIN listings l
			ON l.profile_id = m.id AND l.item_id LIKE $1
		WHERE m.user_id = $2
		GROUP BY m.id
		ORDER BY m.updated_at DESC, m.id DESC
	`, scopedItemPrefix(userID), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.Mission, 0)
	for rows.Next() {
		mission, err := scanPGMissionWithStats(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mission)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DeleteMission(id int64, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM shopping_profiles WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	if _, err := tx.Exec(`DELETE FROM search_configs WHERE profile_id = $1 AND user_id = $2`, id, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM listings WHERE profile_id = $1 AND item_id LIKE $2`, id, scopedItemPrefix(userID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shortlist_entries WHERE profile_id = $1 AND user_id = $2`, id, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) UpdateMissionStatus(id int64, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "active", "paused", "completed":
	default:
		return fmt.Errorf("unsupported mission status %q", status)
	}
	active := status == "active"
	_, err := s.db.Exec(`
		UPDATE shopping_profiles
		SET status = $1, active = $2, updated_at = NOW()
		WHERE id = $3
	`, status, active, id)
	return err
}

func (s *PostgresStore) GetMissionLastRecheck(ctx context.Context, missionID int64, userID string) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT last_manual_recheck_at FROM shopping_profiles WHERE id = $1 AND user_id = $2
	`, missionID, userID).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, sql.ErrNoRows
	}
	return t, err
}

func (s *PostgresStore) SetMissionRecheck(ctx context.Context, missionID int64, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE shopping_profiles SET last_manual_recheck_at = NOW() WHERE id = $1 AND user_id = $2
	`, missionID, userID)
	return err
}

func (s *PostgresStore) ResetMissionSearchSpecsNextRun(ctx context.Context, missionID int64, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE search_configs SET next_run_at = NOW() WHERE profile_id = $1 AND user_id = $2
	`, missionID, userID)
	return err
}

func (s *PostgresStore) SaveShortlistEntry(entry models.ShortlistEntry) error {
	concernsJSON, _ := json.Marshal(entry.Concerns)
	questionsJSON, _ := json.Marshal(entry.SuggestedQuestions)
	_, err := s.db.Exec(`
		INSERT INTO shortlist_entries (
			user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
			ask_price, fair_price, verdict, concerns, suggested_questions, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, $13)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			profile_id = EXCLUDED.profile_id,
			title = EXCLUDED.title,
			url = EXCLUDED.url,
			recommendation_label = EXCLUDED.recommendation_label,
			recommendation_score = EXCLUDED.recommendation_score,
			ask_price = EXCLUDED.ask_price,
			fair_price = EXCLUDED.fair_price,
			verdict = EXCLUDED.verdict,
			concerns = EXCLUDED.concerns,
			suggested_questions = EXCLUDED.suggested_questions,
			status = EXCLUDED.status,
			updated_at = NOW()
	`,
		entry.UserID, entry.MissionID, entry.ItemID, entry.Title, entry.URL,
		string(entry.RecommendationLabel), entry.RecommendationScore, entry.AskPrice, entry.FairPrice,
		entry.Verdict, string(concernsJSON), string(questionsJSON), entry.Status,
	)
	return err
}

func (s *PostgresStore) GetShortlist(userID string) ([]models.ShortlistEntry, error) {
	rows, err := s.db.Query(`
		SELECT se.id, se.user_id, se.profile_id, se.item_id, se.title, se.url,
		       se.recommendation_label, se.recommendation_score,
		       se.ask_price, se.fair_price, se.verdict,
		       se.concerns::text, se.suggested_questions::text, se.status, se.created_at, se.updated_at,
		       COALESCE(l.condition, '') AS condition,
		       COALESCE(l.marketplace_id, '') AS marketplace_id,
		       COALESCE(l.outreach_status, 'none') AS outreach_status
		FROM shortlist_entries se
		LEFT JOIN listings l ON l.item_id = (se.user_id || '::' || se.item_id)
		WHERE se.user_id = $1
		ORDER BY se.updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.ShortlistEntry
	for rows.Next() {
		entry, err := scanShortlistEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *PostgresStore) GetShortlistEntry(userID, itemID string) (*models.ShortlistEntry, error) {
	row := s.db.QueryRow(`
		SELECT se.id, se.user_id, se.profile_id, se.item_id, se.title, se.url,
		       se.recommendation_label, se.recommendation_score,
		       se.ask_price, se.fair_price, se.verdict,
		       se.concerns::text, se.suggested_questions::text, se.status, se.created_at, se.updated_at,
		       COALESCE(l.condition, '') AS condition,
		       COALESCE(l.marketplace_id, '') AS marketplace_id,
		       COALESCE(l.outreach_status, 'none') AS outreach_status
		FROM shortlist_entries se
		LEFT JOIN listings l ON l.item_id = (se.user_id || '::' || se.item_id)
		WHERE se.user_id = $1 AND se.item_id = $2
	`, userID, itemID)
	entry, err := scanShortlistEntry(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *PostgresStore) SaveConversationArtifact(userID string, intent models.ConversationIntent, input, output string) error {
	_, err := s.db.Exec(`
		INSERT INTO conversation_artifacts (user_id, intent, input_text, output_text)
		VALUES ($1, $2, $3, $4)
	`, userID, string(intent), input, output)
	return err
}

func (s *PostgresStore) SaveAssistantSession(session models.AssistantSession) error {
	draftJSON := "{}"
	if session.DraftMission != nil {
		raw, err := json.Marshal(session.DraftMission)
		if err != nil {
			return err
		}
		draftJSON = string(raw)
	}
	_, err := s.db.Exec(`
		INSERT INTO assistant_sessions (user_id, pending_intent, pending_question, draft_profile, last_assistant_msg, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, NOW())
		ON CONFLICT(user_id) DO UPDATE SET
			pending_intent = EXCLUDED.pending_intent,
			pending_question = EXCLUDED.pending_question,
			draft_profile = EXCLUDED.draft_profile,
			last_assistant_msg = EXCLUDED.last_assistant_msg,
			updated_at = NOW()
	`, session.UserID, string(session.PendingIntent), session.PendingQuestion, draftJSON, session.LastAssistantMsg)
	return err
}

func (s *PostgresStore) GetAssistantSession(userID string) (*models.AssistantSession, error) {
	row := s.db.QueryRow(`
		SELECT user_id, pending_intent, pending_question, draft_profile::text, last_assistant_msg, updated_at
		FROM assistant_sessions
		WHERE user_id = $1
	`, userID)

	var session models.AssistantSession
	var draftJSON string
	err := row.Scan(&session.UserID, &session.PendingIntent, &session.PendingQuestion, &draftJSON, &session.LastAssistantMsg, &session.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	draftJSON = strings.TrimSpace(draftJSON)
	if draftJSON != "" && draftJSON != "{}" && draftJSON != "null" {
		var draft models.Mission
		if err := json.Unmarshal([]byte(draftJSON), &draft); err == nil {
			session.DraftMission = &draft
		}
	}
	return &session, nil
}

func (s *PostgresStore) ClearAssistantSession(userID string) error {
	_, err := s.db.Exec(`DELETE FROM assistant_sessions WHERE user_id = $1`, userID)
	return err
}

func (s *PostgresStore) SaveActionDraft(draft models.ActionDraft) error {
	_, err := s.db.Exec(`
		INSERT INTO action_log (user_id, item_id, action_type, content, status)
		VALUES ($1, $2, $3, $4, $5)
	`, draft.UserID, draft.ItemID, draft.ActionType, draft.Content, draft.Status)
	return err
}

func (s *PostgresStore) CreateUser(email, hash, name string) (string, error) {
	id, err := randomPostgresID()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`
		INSERT INTO users (id, email, password_hash, name, tier, role)
		VALUES ($1, $2, $3, $4, 'free', '')
	`, id, strings.ToLower(strings.TrimSpace(email)), hash, strings.TrimSpace(name))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *PostgresStore) UpdateUserProfile(user models.User) error {
	_, err := s.db.Exec(`
		UPDATE users
		SET name = $1, country_code = $2, region = $3, city = $4, postal_code = $5, preferred_radius_km = $6,
		    cross_border_enabled = $7, updated_at = NOW()
		WHERE id = $8
	`, strings.TrimSpace(user.Name), strings.ToUpper(strings.TrimSpace(user.CountryCode)), strings.TrimSpace(user.Region),
		strings.TrimSpace(user.City), strings.TrimSpace(user.PostalCode), user.PreferredRadiusKm, user.CrossBorderEnabled, user.ID)
	return err
}

func (s *PostgresStore) GetUserByEmail(email string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email)))
	return scanPGUser(row)
}

func (s *PostgresStore) GetUserByID(id string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE id = $1
	`, id)
	return scanPGUser(row)
}

func (s *PostgresStore) GetUserByAuthIdentity(provider, subject string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT u.id, u.email, u.password_hash, u.name, u.tier, u.role, u.is_admin, u.stripe_customer_id, u.country_code, u.region, u.city,
		       u.postal_code, u.preferred_radius_km, u.cross_border_enabled, u.created_at, u.updated_at
		FROM users u
		JOIN user_auth_identities i ON i.user_id = u.id
		WHERE i.provider = $1 AND i.provider_subject = $2
	`, strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(subject))
	return scanPGUser(row)
}

func (s *PostgresStore) UpsertUserAuthIdentity(identity models.AuthIdentity) error {
	_, err := s.db.Exec(`
		INSERT INTO user_auth_identities (user_id, provider, provider_subject, email, email_verified)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT(provider, provider_subject) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			email = EXCLUDED.email,
			email_verified = EXCLUDED.email_verified,
			updated_at = NOW()
	`, identity.UserID, strings.ToLower(strings.TrimSpace(identity.Provider)), strings.TrimSpace(identity.ProviderSubject),
		strings.ToLower(strings.TrimSpace(identity.Email)), identity.EmailVerified)
	return err
}

func (s *PostgresStore) ListUserAuthMethods(userID string) ([]string, error) {
	user, err := s.GetUserByID(userID)
	if err != nil || user == nil {
		return nil, err
	}
	methods := []string{}
	if strings.HasPrefix(strings.TrimSpace(user.PasswordHash), "$2") {
		methods = append(methods, "email_password")
	}
	rows, err := s.db.Query(`SELECT provider FROM user_auth_identities WHERE user_id = $1 ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for _, value := range methods {
		seen[value] = true
	}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, err
		}
		if !seen[provider] {
			seen[provider] = true
			methods = append(methods, provider)
		}
	}
	return methods, rows.Err()
}

func (s *PostgresStore) UpdateUserTier(userID, tier string) error {
	_, err := s.db.Exec(`UPDATE users SET tier = $1, updated_at = NOW() WHERE id = $2`, tier, userID)
	return err
}

func (s *PostgresStore) UpdateUserRole(userID, role string) error {
	_, err := s.db.Exec(`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`, strings.TrimSpace(role), userID)
	return err
}

func (s *PostgresStore) UpdateStripeCustomer(userID, customerID string) error {
	_, err := s.db.Exec(`UPDATE users SET stripe_customer_id = $1, updated_at = NOW() WHERE id = $2`, customerID, userID)
	return err
}

func (s *PostgresStore) UpdateUserTierByStripeCustomer(customerID, tier string) error {
	_, err := s.db.Exec(`UPDATE users SET tier = $1, updated_at = NOW() WHERE stripe_customer_id = $2`, tier, customerID)
	return err
}

func (s *PostgresStore) RecordStripeEvent(eventID string) error {
	_, err := s.db.Exec(`INSERT INTO stripe_events (event_id) VALUES ($1) ON CONFLICT(event_id) DO NOTHING`, eventID)
	return err
}

func (s *PostgresStore) RecordStripeProcessedEvent(eventID string) (bool, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false, nil
	}
	result, err := s.db.Exec(`INSERT INTO stripe_processed_events (event_id) VALUES ($1) ON CONFLICT(event_id) DO NOTHING`, eventID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *PostgresStore) SetUserAdmin(userID string, isAdmin bool) error {
	_, err := s.db.Exec(`UPDATE users SET is_admin = $1, updated_at = NOW() WHERE id = $2`, isAdmin, userID)
	return err
}

func (s *PostgresStore) RecordAIUsage(entry models.AIUsageEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO ai_usage_log (user_id, mission_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, entry.UserID, entry.MissionID, entry.CallType, entry.Model, entry.PromptTokens, entry.CompletionTokens, entry.TotalTokens,
		entry.LatencyMs, entry.Success, entry.ErrorMsg)
	return err
}

func (s *PostgresStore) ListAllUsers() ([]models.AdminUserSummary, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.email, u.password_hash, u.name, u.tier, u.role, u.is_admin, u.stripe_customer_id, u.created_at, u.updated_at,
		       COALESCE(m.cnt, 0) AS mission_count,
		       COALESCE(sc.cnt, 0) AS search_count,
		       COALESCE(ai.cnt, 0) AS ai_call_count,
		       COALESCE(ai.tokens, 0) AS ai_tokens
		FROM users u
		LEFT JOIN (SELECT user_id, COUNT(*) AS cnt FROM shopping_profiles GROUP BY user_id) m ON m.user_id = u.id
		LEFT JOIN (SELECT user_id, COUNT(*) AS cnt FROM search_configs GROUP BY user_id) sc ON sc.user_id = u.id
		LEFT JOIN (SELECT user_id, COUNT(*) AS cnt, COALESCE(SUM(total_tokens), 0) AS tokens FROM ai_usage_log GROUP BY user_id) ai ON ai.user_id = u.id
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AdminUserSummary
	for rows.Next() {
		var s models.AdminUserSummary
		if err := rows.Scan(&s.ID, &s.Email, &s.PasswordHash, &s.Name, &s.Tier, &s.Role, &s.IsAdmin, &s.StripeCustomer,
			&s.CreatedAt, &s.UpdatedAt, &s.MissionCount, &s.SearchCount, &s.AICallCount, &s.AITokens); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAIUsageStats(days int) (models.AIUsageStats, error) {
	var stats models.AIUsageStats
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(CASE WHEN NOT success THEN 1 ELSE 0 END), 0)
		FROM ai_usage_log
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
	`, days).Scan(&stats.TotalCalls, &stats.TotalTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.FailedCalls)
	return stats, err
}

func (s *PostgresStore) GetAIUsageTimeline(days int) ([]models.AIUsageEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, mission_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg, created_at
		FROM ai_usage_log
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
		ORDER BY created_at DESC
		LIMIT 500
	`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AIUsageEntry
	for rows.Next() {
		var e models.AIUsageEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.MissionID, &e.CallType, &e.Model, &e.PromptTokens, &e.CompletionTokens,
			&e.TotalTokens, &e.LatencyMs, &e.Success, &e.ErrorMsg, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetUserAIUsageStats(userID string, days int) (models.AIUsageStats, error) {
	var stats models.AIUsageStats
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(CASE WHEN NOT success THEN 1 ELSE 0 END), 0)
		FROM ai_usage_log
		WHERE user_id = $1 AND created_at >= NOW() - MAKE_INTERVAL(days => $2)
	`, userID, days).Scan(&stats.TotalCalls, &stats.TotalTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.FailedCalls)
	return stats, err
}

func (s *PostgresStore) GetSearchConfigs(userID string) ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json::text, offer_percentage, auto_message, message_template, attributes_json::text,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE user_id = $1
		ORDER BY updated_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []models.SearchSpec
	for rows.Next() {
		spec, err := scanPGSearchSpec(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (s *PostgresStore) GetSearchConfigByID(id int64) (*models.SearchSpec, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json::text, offer_percentage, auto_message, message_template, attributes_json::text,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE id = $1
	`, id)
	spec, err := scanPGSearchSpec(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *PostgresStore) GetAllEnabledSearchConfigs() ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json::text, offer_percentage, auto_message, message_template, attributes_json::text,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE enabled = TRUE
		ORDER BY user_id, updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []models.SearchSpec
	for rows.Next() {
		spec, err := scanPGSearchSpec(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (s *PostgresStore) CountSearchConfigs(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM search_configs WHERE user_id = $1 AND enabled = TRUE`, userID).Scan(&count)
	return count, err
}

func (s *PostgresStore) CountActiveMissions(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM shopping_profiles WHERE user_id = $1 AND status = 'active'`, userID).Scan(&count)
	return count, err
}

func (s *PostgresStore) CreateSearchConfig(spec models.SearchSpec) (int64, error) {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO search_configs (
			user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
			condition_json, offer_percentage, auto_message, message_template, attributes_json,
			enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
			last_result_count, consecutive_empty_runs, consecutive_failures
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14, $15, $16, $17::jsonb, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)
		RETURNING id
	`, spec.UserID, spec.ProfileID, spec.Name, spec.Query, marketplace.NormalizeMarketplaceID(spec.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(spec.CountryCode)), strings.TrimSpace(spec.City), strings.TrimSpace(spec.PostalCode), spec.RadiusKm,
		spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, spec.AutoMessage, spec.MessageTemplate, string(attributesJSON),
		spec.Enabled, int64(spec.CheckInterval/time.Second), spec.PriorityClass,
		nullTime(spec.NextRunAt), nullTime(spec.LastRunAt), nullTime(spec.LastSignalAt), nullTime(spec.LastErrorAt),
		spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateSearchConfig(spec models.SearchSpec) error {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET profile_id = $1, name = $2, query = $3, marketplace_id = $4, country_code = $5, city = $6, postal_code = $7,
		    radius_km = $8, category_id = $9, max_price = $10, min_price = $11,
		    condition_json = $12::jsonb, offer_percentage = $13, auto_message = $14, message_template = $15,
		    attributes_json = $16::jsonb, enabled = $17, check_interval_seconds = $18, priority_class = $19, next_run_at = $20,
		    last_run_at = $21, last_signal_at = $22, last_error_at = $23, last_result_count = $24, consecutive_empty_runs = $25,
		    consecutive_failures = $26, updated_at = NOW()
		WHERE id = $27 AND user_id = $28
	`, spec.ProfileID, spec.Name, spec.Query, marketplace.NormalizeMarketplaceID(spec.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(spec.CountryCode)), strings.TrimSpace(spec.City), strings.TrimSpace(spec.PostalCode), spec.RadiusKm,
		spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, spec.AutoMessage, spec.MessageTemplate,
		string(attributesJSON), spec.Enabled, int64(spec.CheckInterval/time.Second), spec.PriorityClass,
		nullTime(spec.NextRunAt), nullTime(spec.LastRunAt), nullTime(spec.LastSignalAt), nullTime(spec.LastErrorAt),
		spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures, spec.ID, spec.UserID,
	)
	return err
}

func (s *PostgresStore) UpdateSearchRuntime(spec models.SearchSpec) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET priority_class = $1, next_run_at = $2, last_run_at = $3, last_signal_at = $4, last_error_at = $5,
		    last_result_count = $6, consecutive_empty_runs = $7, consecutive_failures = $8, updated_at = NOW()
		WHERE id = $9
	`, spec.PriorityClass, nullTime(spec.NextRunAt), nullTime(spec.LastRunAt), nullTime(spec.LastSignalAt),
		nullTime(spec.LastErrorAt), spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures, spec.ID)
	return err
}

func (s *PostgresStore) SetSearchEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET enabled = $1, updated_at = NOW()
		WHERE id = $2
	`, enabled, id)
	return err
}

func (s *PostgresStore) SetSearchNextRunAt(id int64, nextRunAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET next_run_at = $1, updated_at = NOW()
		WHERE id = $2
	`, nullTime(nextRunAt), id)
	return err
}

func (s *PostgresStore) DeleteSearchConfig(id int64, userID string) error {
	_, err := s.db.Exec(`DELETE FROM search_configs WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func (s *PostgresStore) RecordSearchRun(entry models.SearchRunLog) error {
	_, err := s.db.Exec(`
		INSERT INTO search_run_log (
			search_config_id, user_id, mission_id, plan, marketplace_id, country_code,
			started_at, finished_at, queue_wait_ms, priority, status, results_found, new_listings, deal_hits,
			throttled, error_code, searches_avoided
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, entry.SearchConfigID, entry.UserID, entry.MissionID, entry.Plan, marketplace.NormalizeMarketplaceID(entry.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(entry.CountryCode)), entry.StartedAt, entry.FinishedAt, entry.QueueWaitMs,
		entry.Priority, entry.Status, entry.ResultsFound, entry.NewListings, entry.DealHits, entry.Throttled,
		entry.ErrorCode, entry.SearchesAvoided)
	return err
}

func (s *PostgresStore) RecordAdminAuditLog(entry models.AdminAuditLogEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO admin_audit_log (
			actor_user_id, actor_role, request_id, action, target_type, target_id, before_json, after_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, strings.TrimSpace(entry.ActorUserID), strings.TrimSpace(entry.ActorRole), strings.TrimSpace(entry.RequestID),
		strings.TrimSpace(entry.Action), strings.TrimSpace(entry.TargetType), strings.TrimSpace(entry.TargetID),
		strings.TrimSpace(entry.BeforeJSON), strings.TrimSpace(entry.AfterJSON))
	return err
}

func (s *PostgresStore) GetSearchOpsStats(days int) (models.SearchOpsStats, error) {
	stats := models.SearchOpsStats{
		ByStatus:      map[string]int{},
		ByPlan:        map[string]int{},
		ByCountry:     map[string]int{},
		ByMarketplace: map[string]int{},
	}
	var queueAvg sql.NullFloat64
	var freshness sql.NullFloat64
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN COALESCE(error_code, '') <> '' AND error_code <> 'out_of_scope' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(results_found), 0), COALESCE(SUM(new_listings), 0), COALESCE(SUM(deal_hits), 0),
		       COALESCE(SUM(CASE WHEN throttled THEN 1 ELSE 0 END), 0), COALESCE(SUM(searches_avoided), 0),
		       AVG(queue_wait_ms::double precision),
		       AVG(EXTRACT(EPOCH FROM (NOW() - sc.last_signal_at)) / 60.0)
		FROM search_run_log l
		LEFT JOIN search_configs sc ON sc.id = l.search_config_id
		WHERE l.started_at >= NOW() - MAKE_INTERVAL(days => $1)
	`, days).Scan(&stats.TotalRuns, &stats.SuccessfulRuns, &stats.FailedRuns,
		&stats.TotalResultsFound, &stats.TotalNewListings, &stats.TotalDealHits,
		&stats.TotalThrottled, &stats.SearchesAvoidedByScoping, &queueAvg, &freshness)
	if err != nil {
		return stats, err
	}
	if stats.TotalRuns > 0 {
		stats.FailureRatePct = float64(stats.FailedRuns) * 100 / float64(stats.TotalRuns)
	}
	if queueAvg.Valid {
		stats.AverageQueueWaitMs = int(queueAvg.Float64)
	}
	if freshness.Valid {
		stats.AverageMissionFreshnessMins = int(freshness.Float64)
	}
	if err := s.fillPGSearchOpsBreakdown(days, "status", &stats.ByStatus); err != nil {
		return stats, err
	}
	if err := s.fillPGSearchOpsBreakdown(days, "plan", &stats.ByPlan); err != nil {
		return stats, err
	}
	if err := s.fillPGSearchOpsBreakdown(days, "country_code", &stats.ByCountry); err != nil {
		return stats, err
	}
	if err := s.fillPGSearchOpsBreakdown(days, "marketplace_id", &stats.ByMarketplace); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *PostgresStore) ListSearchRuns(filter models.AdminSearchRunFilter) ([]models.AdminSearchRun, error) {
	days := filter.Days
	if days <= 0 {
		days = 7
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := s.db.Query(`
		SELECT l.id, l.search_config_id, COALESCE(sc.name, ''), l.user_id, COALESCE(u.email, ''),
		       l.mission_id, COALESCE(sp.name, ''), l.plan, l.marketplace_id, l.country_code,
		       l.started_at, l.finished_at, l.queue_wait_ms, l.priority, l.status, l.results_found,
		       l.new_listings, l.deal_hits, l.throttled, l.error_code, l.searches_avoided
		FROM search_run_log l
		LEFT JOIN search_configs sc ON sc.id = l.search_config_id
		LEFT JOIN shopping_profiles sp ON sp.id = l.mission_id
		LEFT JOIN users u ON u.id = l.user_id
		WHERE l.started_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND ($2 = '' OR l.status = $2)
		  AND ($3 = '' OR l.marketplace_id = $3)
		  AND ($4 = '' OR l.country_code = $4)
		  AND ($5 = '' OR l.user_id = $5)
		ORDER BY l.started_at DESC
		LIMIT $6
	`, days,
		strings.TrimSpace(filter.Status),
		marketplace.NormalizeMarketplaceID(filter.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(filter.CountryCode)),
		strings.TrimSpace(filter.UserID),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.AdminSearchRun, 0, limit)
	for rows.Next() {
		var run models.AdminSearchRun
		if err := rows.Scan(
			&run.ID, &run.SearchConfigID, &run.SearchName, &run.UserID, &run.UserEmail,
			&run.MissionID, &run.MissionName, &run.Plan, &run.MarketplaceID, &run.CountryCode,
			&run.StartedAt, &run.FinishedAt, &run.QueueWaitMs, &run.Priority, &run.Status, &run.ResultsFound,
			&run.NewListings, &run.DealHits, &run.Throttled, &run.ErrorCode, &run.SearchesAvoided,
		); err != nil {
			return nil, err
		}
		run.MarketplaceID = marketplace.NormalizeMarketplaceID(run.MarketplaceID)
		run.CountryCode = strings.ToUpper(strings.TrimSpace(run.CountryCode))
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAdminAuditLog(limit int) ([]models.AdminAuditLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, actor_user_id, actor_role, request_id, action, target_type, target_id, before_json, after_json, created_at
		FROM admin_audit_log
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.AdminAuditLogEntry, 0, limit)
	for rows.Next() {
		var entry models.AdminAuditLogEntry
		if err := rows.Scan(&entry.ID, &entry.ActorUserID, &entry.ActorRole, &entry.RequestID, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.BeforeJSON, &entry.AfterJSON, &entry.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpsertStripeWebhookEvent(entry models.StripeWebhookEventLog) error {
	attemptCount := entry.AttemptCount
	if attemptCount <= 0 {
		attemptCount = 1
	}
	receivedAt := entry.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO stripe_webhook_events (
			event_id, event_type, object_id, api_account, request_id, status, error_message,
			attempt_count, payload_json, received_at, processed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT(event_id) DO UPDATE SET
			event_type = EXCLUDED.event_type,
			object_id = EXCLUDED.object_id,
			api_account = EXCLUDED.api_account,
			request_id = EXCLUDED.request_id,
			status = EXCLUDED.status,
			error_message = EXCLUDED.error_message,
			attempt_count = GREATEST(stripe_webhook_events.attempt_count, EXCLUDED.attempt_count),
			payload_json = EXCLUDED.payload_json,
			received_at = EXCLUDED.received_at,
			processed_at = EXCLUDED.processed_at
	`,
		strings.TrimSpace(entry.EventID), strings.TrimSpace(entry.EventType), strings.TrimSpace(entry.ObjectID),
		strings.TrimSpace(entry.APIAccount), strings.TrimSpace(entry.RequestID), strings.TrimSpace(entry.Status),
		strings.TrimSpace(entry.ErrorMessage), attemptCount, strings.TrimSpace(entry.PayloadJSON), receivedAt, nullTime(entry.ProcessedAt),
	)
	return err
}

func (s *PostgresStore) UpsertStripeSubscriptionSnapshot(snapshot models.StripeSubscriptionSnapshot) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_subscription_snapshots (
			subscription_id, customer_id, user_id, status, plan_price_id, plan_interval, currency,
			unit_amount, quantity, current_period_start, current_period_end, cancel_at_period_end,
			canceled_at, paused, latest_invoice_id, default_payment_method, raw_json, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW(), NOW())
		ON CONFLICT(subscription_id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			user_id = EXCLUDED.user_id,
			status = EXCLUDED.status,
			plan_price_id = EXCLUDED.plan_price_id,
			plan_interval = EXCLUDED.plan_interval,
			currency = EXCLUDED.currency,
			unit_amount = EXCLUDED.unit_amount,
			quantity = EXCLUDED.quantity,
			current_period_start = EXCLUDED.current_period_start,
			current_period_end = EXCLUDED.current_period_end,
			cancel_at_period_end = EXCLUDED.cancel_at_period_end,
			canceled_at = EXCLUDED.canceled_at,
			paused = EXCLUDED.paused,
			latest_invoice_id = EXCLUDED.latest_invoice_id,
			default_payment_method = EXCLUDED.default_payment_method,
			raw_json = EXCLUDED.raw_json,
			updated_at = NOW()
	`,
		strings.TrimSpace(snapshot.SubscriptionID), strings.TrimSpace(snapshot.CustomerID), strings.TrimSpace(snapshot.UserID),
		strings.TrimSpace(snapshot.Status), strings.TrimSpace(snapshot.PlanPriceID), strings.TrimSpace(snapshot.PlanInterval),
		strings.ToUpper(strings.TrimSpace(snapshot.Currency)), snapshot.UnitAmount, snapshot.Quantity,
		nullTime(snapshot.CurrentPeriodStart), nullTime(snapshot.CurrentPeriodEnd),
		snapshot.CancelAtPeriodEnd, nullTime(snapshot.CanceledAt), snapshot.Paused,
		strings.TrimSpace(snapshot.LatestInvoiceID), strings.TrimSpace(snapshot.DefaultPaymentMethod), strings.TrimSpace(snapshot.RawJSON),
	)
	return err
}

func (s *PostgresStore) AppendStripeSubscriptionHistory(entry models.StripeSubscriptionHistoryEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_subscription_history (
			subscription_id, event_id, event_type, status, plan_price_id, currency, unit_amount, quantity,
			period_start, period_end, cancel_at_period_end, raw_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`,
		strings.TrimSpace(entry.SubscriptionID), strings.TrimSpace(entry.EventID), strings.TrimSpace(entry.EventType),
		strings.TrimSpace(entry.Status), strings.TrimSpace(entry.PlanPriceID), strings.ToUpper(strings.TrimSpace(entry.Currency)),
		entry.UnitAmount, entry.Quantity, nullTime(entry.PeriodStart), nullTime(entry.PeriodEnd), entry.CancelAtEnd, strings.TrimSpace(entry.RawJSON),
	)
	return err
}

func (s *PostgresStore) UpsertStripeInvoiceSummary(invoice models.StripeInvoiceSummary) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_invoice_summaries (
			invoice_id, subscription_id, customer_id, user_id, status, currency, amount_due, amount_paid,
			amount_remaining, attempt_count, paid, hosted_invoice_url, invoice_pdf, period_start, period_end,
			due_date, finalized_at, raw_json, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, NOW(), NOW())
		ON CONFLICT(invoice_id) DO UPDATE SET
			subscription_id = EXCLUDED.subscription_id,
			customer_id = EXCLUDED.customer_id,
			user_id = EXCLUDED.user_id,
			status = EXCLUDED.status,
			currency = EXCLUDED.currency,
			amount_due = EXCLUDED.amount_due,
			amount_paid = EXCLUDED.amount_paid,
			amount_remaining = EXCLUDED.amount_remaining,
			attempt_count = EXCLUDED.attempt_count,
			paid = EXCLUDED.paid,
			hosted_invoice_url = EXCLUDED.hosted_invoice_url,
			invoice_pdf = EXCLUDED.invoice_pdf,
			period_start = EXCLUDED.period_start,
			period_end = EXCLUDED.period_end,
			due_date = EXCLUDED.due_date,
			finalized_at = EXCLUDED.finalized_at,
			raw_json = EXCLUDED.raw_json,
			updated_at = NOW()
	`,
		strings.TrimSpace(invoice.InvoiceID), strings.TrimSpace(invoice.SubscriptionID), strings.TrimSpace(invoice.CustomerID),
		strings.TrimSpace(invoice.UserID), strings.TrimSpace(invoice.Status), strings.ToUpper(strings.TrimSpace(invoice.Currency)),
		invoice.AmountDue, invoice.AmountPaid, invoice.AmountRemaining, invoice.AttemptCount, invoice.Paid,
		strings.TrimSpace(invoice.HostedInvoiceURL), strings.TrimSpace(invoice.InvoicePDF),
		nullTime(invoice.PeriodStart), nullTime(invoice.PeriodEnd), nullTime(invoice.DueDate), nullTime(invoice.FinalizedAt),
		strings.TrimSpace(invoice.RawJSON),
	)
	return err
}

func (s *PostgresStore) RecordStripeMutation(entry models.StripeMutationLog) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_mutation_log (
			idempotency_key, actor_user_id, actor_role, action, target_id, request_json, response_json, status,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT(idempotency_key) DO UPDATE SET
			actor_user_id = EXCLUDED.actor_user_id,
			actor_role = EXCLUDED.actor_role,
			action = EXCLUDED.action,
			target_id = EXCLUDED.target_id,
			request_json = EXCLUDED.request_json,
			response_json = EXCLUDED.response_json,
			status = EXCLUDED.status,
			updated_at = NOW()
	`,
		strings.TrimSpace(entry.IdempotencyKey), strings.TrimSpace(entry.ActorUserID), strings.TrimSpace(entry.ActorRole),
		strings.TrimSpace(entry.Action), strings.TrimSpace(entry.TargetID), strings.TrimSpace(entry.RequestJSON),
		strings.TrimSpace(entry.ResponseJSON), strings.TrimSpace(entry.Status),
	)
	return err
}

func (s *PostgresStore) StartBillingReconcileRun(run models.BillingReconcileRun) (int64, error) {
	var id int64
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	err := s.db.QueryRow(`
		INSERT INTO billing_reconcile_runs (triggered_by, status, summary_json, error_json, started_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`,
		strings.TrimSpace(run.TriggeredBy), strings.TrimSpace(run.Status), strings.TrimSpace(run.SummaryJSON),
		strings.TrimSpace(run.ErrorJSON), startedAt,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) FinishBillingReconcileRun(id int64, status, summaryJSON, errorJSON string) error {
	_, err := s.db.Exec(`
		UPDATE billing_reconcile_runs
		SET status = $1, summary_json = $2, error_json = $3, finished_at = NOW()
		WHERE id = $4
	`, strings.TrimSpace(status), strings.TrimSpace(summaryJSON), strings.TrimSpace(errorJSON), id)
	return err
}

func (s *PostgresStore) GetLatestBusinessReconcileRun() (*models.BillingReconcileRun, error) {
	row := s.db.QueryRow(`
		SELECT id, triggered_by, status, summary_json, error_json, started_at, COALESCE(finished_at, TO_TIMESTAMP(0))
		FROM billing_reconcile_runs
		ORDER BY started_at DESC
		LIMIT 1
	`)
	var run models.BillingReconcileRun
	if err := row.Scan(&run.ID, &run.TriggeredBy, &run.Status, &run.SummaryJSON, &run.ErrorJSON, &run.StartedAt, &run.FinishedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if run.FinishedAt.Equal(time.Unix(0, 0).UTC()) {
		run.FinishedAt = time.Time{}
	}
	return &run, nil
}

func (s *PostgresStore) ListUsersWithStripeCustomerIDs() ([]models.User, error) {
	rows, err := s.db.Query(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users
		WHERE COALESCE(stripe_customer_id, '') <> ''
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.Role, &user.IsAdmin,
			&user.StripeCustomer, &user.CountryCode, &user.Region, &user.City, &user.PostalCode,
			&user.PreferredRadiusKm, &user.CrossBorderEnabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		user.CountryCode = strings.ToUpper(strings.TrimSpace(user.CountryCode))
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *PostgresStore) GetStripeSubscriptionSnapshot(subscriptionID string) (*models.StripeSubscriptionSnapshot, error) {
	row := s.db.QueryRow(`
		SELECT subscription_id, customer_id, user_id, status, plan_price_id, plan_interval, currency, unit_amount, quantity,
		       COALESCE(current_period_start, TO_TIMESTAMP(0)), COALESCE(current_period_end, TO_TIMESTAMP(0)),
		       cancel_at_period_end, COALESCE(canceled_at, TO_TIMESTAMP(0)), paused, latest_invoice_id,
		       default_payment_method, raw_json, updated_at, created_at
		FROM stripe_subscription_snapshots
		WHERE subscription_id = $1
	`, strings.TrimSpace(subscriptionID))
	var snapshot models.StripeSubscriptionSnapshot
	if err := row.Scan(&snapshot.SubscriptionID, &snapshot.CustomerID, &snapshot.UserID, &snapshot.Status, &snapshot.PlanPriceID,
		&snapshot.PlanInterval, &snapshot.Currency, &snapshot.UnitAmount, &snapshot.Quantity,
		&snapshot.CurrentPeriodStart, &snapshot.CurrentPeriodEnd, &snapshot.CancelAtPeriodEnd, &snapshot.CanceledAt,
		&snapshot.Paused, &snapshot.LatestInvoiceID, &snapshot.DefaultPaymentMethod, &snapshot.RawJSON, &snapshot.UpdatedAt, &snapshot.CreatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	zero := time.Unix(0, 0).UTC()
	if snapshot.CurrentPeriodStart.Equal(zero) {
		snapshot.CurrentPeriodStart = time.Time{}
	}
	if snapshot.CurrentPeriodEnd.Equal(zero) {
		snapshot.CurrentPeriodEnd = time.Time{}
	}
	if snapshot.CanceledAt.Equal(zero) {
		snapshot.CanceledAt = time.Time{}
	}
	return &snapshot, nil
}

func (s *PostgresStore) GetBusinessOverview(days int) (models.BusinessOverview, error) {
	if days <= 0 {
		days = 30
	}
	overview := models.BusinessOverview{WindowDays: days}

	rows, err := s.db.Query(`
		SELECT status, plan_interval, currency, unit_amount, quantity, customer_id
		FROM stripe_subscription_snapshots
	`)
	if err != nil {
		return overview, err
	}
	defer rows.Close()

	activeCustomers := map[string]bool{}
	for rows.Next() {
		var status, interval, currency, customerID string
		var unitAmount, quantity int64
		if err := rows.Scan(&status, &interval, &currency, &unitAmount, &quantity, &customerID); err != nil {
			return overview, err
		}
		overview.SubscriptionsTotal++
		if subscriptionIsPaidActive(status) {
			overview.SubscriptionsActive++
			if quantity <= 0 {
				quantity = 1
			}
			monthlyAmount := float64(unitAmount * quantity)
			if strings.EqualFold(strings.TrimSpace(interval), "year") {
				monthlyAmount = monthlyAmount / 12.0
			}
			overview.MRR += amountToEUR(monthlyAmount, currency)
			if strings.TrimSpace(customerID) != "" {
				activeCustomers[customerID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return overview, err
	}
	overview.ActivePaidAccounts = len(activeCustomers)
	overview.ARR = overview.MRR * 12

	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_invoice_summaries
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND paid = FALSE
		  AND amount_remaining > 0
	`, days).Scan(&overview.FailedPayments); err != nil {
		return overview, err
	}

	recentRevenue, err := s.sumPaidRevenueInWindow(days)
	if err != nil {
		return overview, err
	}
	previousRevenue, err := s.sumPaidRevenueRange(days*2, days)
	if err != nil {
		return overview, err
	}
	overview.RevenueEUR30d = recentRevenue
	if previousRevenue > 0 {
		overview.RevenueTrendPct = ((recentRevenue - previousRevenue) / previousRevenue) * 100
	}

	var churned int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_subscription_history
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND (status IN ('canceled', 'incomplete_expired') OR event_type LIKE '%deleted')
	`, days).Scan(&churned); err != nil {
		return overview, err
	}
	if overview.ActivePaidAccounts+churned > 0 {
		overview.ChurnRatePct = float64(churned) * 100 / float64(overview.ActivePaidAccounts+churned)
	}

	var lastWebhook sql.NullTime
	if err := s.db.QueryRow(`SELECT MAX(received_at) FROM stripe_webhook_events`).Scan(&lastWebhook); err != nil {
		return overview, err
	}
	if lastWebhook.Valid {
		overview.WebhookLagMinutes = int(time.Since(lastWebhook.Time).Minutes())
	}

	latestRun, err := s.GetLatestBusinessReconcileRun()
	if err != nil {
		return overview, err
	}
	if latestRun != nil {
		ref := latestRun.StartedAt
		if !latestRun.FinishedAt.IsZero() {
			ref = latestRun.FinishedAt
		}
		overview.ReconcileLagMinutes = int(time.Since(ref).Minutes())
	}
	return overview, nil
}

func (s *PostgresStore) ListBusinessSubscriptions(filter models.BusinessSubscriptionFilter) ([]models.BusinessSubscriptionRow, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT ss.subscription_id, ss.customer_id, ss.user_id, COALESCE(u.email, ''), COALESCE(u.tier, 'free'),
		       ss.status, ss.plan_price_id, ss.plan_interval, ss.currency, ss.unit_amount, ss.quantity,
		       COALESCE(ss.current_period_start, TO_TIMESTAMP(0)), COALESCE(ss.current_period_end, TO_TIMESTAMP(0)),
		       ss.cancel_at_period_end, ss.paused, ss.latest_invoice_id,
		       COALESCE(inv.status, ''), COALESCE(inv.amount_due, 0), COALESCE(inv.amount_paid, 0), COALESCE(inv.amount_remaining, 0),
		       COALESCE(inv.attempt_count, 0), ss.updated_at
		FROM stripe_subscription_snapshots ss
		LEFT JOIN users u ON u.id = ss.user_id
		LEFT JOIN stripe_invoice_summaries inv ON inv.invoice_id = ss.latest_invoice_id
		WHERE ($1 = '' OR ss.status = $1)
		  AND ($2 = '' OR ss.plan_price_id = $2)
		  AND ($3 = '' OR ss.user_id = $3)
		  AND ($4 = '' OR UPPER(COALESCE(u.country_code, '')) = $4)
		ORDER BY ss.updated_at DESC
		LIMIT $5
	`,
		strings.TrimSpace(filter.Status),
		strings.TrimSpace(filter.PlanPriceID),
		strings.TrimSpace(filter.UserID),
		strings.ToUpper(strings.TrimSpace(filter.CountryCode)),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.BusinessSubscriptionRow, 0, limit)
	zero := time.Unix(0, 0).UTC()
	for rows.Next() {
		var row models.BusinessSubscriptionRow
		if err := rows.Scan(
			&row.SubscriptionID, &row.CustomerID, &row.UserID, &row.UserEmail, &row.UserTier,
			&row.Status, &row.PlanPriceID, &row.PlanInterval, &row.Currency, &row.UnitAmount, &row.Quantity,
			&row.CurrentPeriodStart, &row.CurrentPeriodEnd, &row.CancelAtPeriodEnd, &row.Paused, &row.LatestInvoiceID,
			&row.InvoiceStatus, &row.AmountDue, &row.AmountPaid, &row.AmountRemaining, &row.AttemptCount, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if row.CurrentPeriodStart.Equal(zero) {
			row.CurrentPeriodStart = time.Time{}
		}
		if row.CurrentPeriodEnd.Equal(zero) {
			row.CurrentPeriodEnd = time.Time{}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetBusinessRevenue(days int) ([]models.BusinessRevenuePoint, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := s.db.Query(`
		SELECT DATE_TRUNC('day', created_at) AS bucket_day,
		       UPPER(COALESCE(currency, 'EUR')) AS currency,
		       COALESCE(SUM(amount_paid), 0) AS amount_paid,
		       COUNT(*) AS invoices
		FROM stripe_invoice_summaries
		WHERE paid = TRUE
		  AND created_at >= NOW() - MAKE_INTERVAL(days => $1)
		GROUP BY bucket_day, currency
		ORDER BY bucket_day ASC
	`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]models.BusinessRevenuePoint, 0)
	for rows.Next() {
		var point models.BusinessRevenuePoint
		if err := rows.Scan(&point.BucketStart, &point.Currency, &point.AmountPaid, &point.Invoices); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (s *PostgresStore) GetBusinessFunnel(days int) (models.BusinessFunnel, error) {
	if days <= 0 {
		days = 30
	}
	funnel := models.BusinessFunnel{WindowDays: days}

	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM users
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
	`, days).Scan(&funnel.Signups); err != nil {
		return funnel, err
	}
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(DISTINCT u.id), 0)
		FROM users u
		WHERE u.created_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND (
			EXISTS (SELECT 1 FROM shopping_profiles sp WHERE sp.user_id = u.id)
			OR EXISTS (SELECT 1 FROM search_configs sc WHERE sc.user_id = u.id)
		  )
	`, days).Scan(&funnel.Activated); err != nil {
		return funnel, err
	}
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(DISTINCT u.id), 0)
		FROM users u
		JOIN stripe_subscription_snapshots ss ON ss.user_id = u.id
		WHERE u.created_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND ss.status IN ('active', 'trialing', 'past_due', 'unpaid')
	`, days).Scan(&funnel.Paid); err != nil {
		return funnel, err
	}
	if funnel.Signups > 0 {
		funnel.SignupToPaidPct = float64(funnel.Paid) * 100 / float64(funnel.Signups)
	}
	if funnel.Activated > 0 {
		funnel.ActivationToPaid = float64(funnel.Paid) * 100 / float64(funnel.Activated)
	}
	return funnel, nil
}

func (s *PostgresStore) GetBusinessCohorts(months int) ([]models.BusinessCohortRow, error) {
	if months <= 0 {
		months = 6
	}
	rows, err := s.db.Query(`
		SELECT TO_CHAR(DATE_TRUNC('month', u.created_at), 'YYYY-MM') AS cohort_month,
		       COUNT(*) AS users,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = TRUE
					  AND DATE_TRUNC('month', inv.created_at) = DATE_TRUNC('month', u.created_at)
			   ) THEN 1 ELSE 0 END) AS paid_m0,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = TRUE
					  AND DATE_TRUNC('month', inv.created_at) = DATE_TRUNC('month', u.created_at + INTERVAL '1 month')
			   ) THEN 1 ELSE 0 END) AS paid_m1,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = TRUE
					  AND DATE_TRUNC('month', inv.created_at) = DATE_TRUNC('month', u.created_at + INTERVAL '2 month')
			   ) THEN 1 ELSE 0 END) AS paid_m2,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (EXTRACT(EPOCH FROM (ss.updated_at - u.created_at)) / 86400.0) <= 30 THEN 1
					ELSE 0 END) AS churn_early,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (EXTRACT(EPOCH FROM (ss.updated_at - u.created_at)) / 86400.0) > 30
					 AND (EXTRACT(EPOCH FROM (ss.updated_at - u.created_at)) / 86400.0) <= 90 THEN 1
					ELSE 0 END) AS churn_middle,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (EXTRACT(EPOCH FROM (ss.updated_at - u.created_at)) / 86400.0) > 90 THEN 1
					ELSE 0 END) AS churn_late
		FROM users u
		LEFT JOIN stripe_subscription_snapshots ss ON ss.user_id = u.id
		WHERE u.created_at >= NOW() - MAKE_INTERVAL(months => $1)
		GROUP BY cohort_month
		ORDER BY cohort_month DESC
		LIMIT $1
	`, months)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cohorts := make([]models.BusinessCohortRow, 0, months)
	for rows.Next() {
		var row models.BusinessCohortRow
		if err := rows.Scan(&row.CohortMonth, &row.Users, &row.PaidMonth0, &row.PaidMonth1, &row.PaidMonth2,
			&row.ChurnBucketEarly, &row.ChurnBucketMiddle, &row.ChurnBucketLate); err != nil {
			return nil, err
		}
		if row.Users > 0 {
			row.RetentionMonth1 = float64(row.PaidMonth1) * 100 / float64(row.Users)
			row.RetentionMonth2 = float64(row.PaidMonth2) * 100 / float64(row.Users)
		}
		cohorts = append(cohorts, row)
	}
	return cohorts, rows.Err()
}

func (s *PostgresStore) GetBusinessAlerts(days int) ([]models.BusinessAlert, error) {
	if days <= 0 {
		days = 7
	}
	alerts := make([]models.BusinessAlert, 0)

	var failed24h int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_invoice_summaries
		WHERE paid = FALSE
		  AND amount_remaining > 0
		  AND created_at >= NOW() - INTERVAL '1 day'
	`).Scan(&failed24h); err != nil {
		return nil, err
	}
	if failed24h >= 3 {
		alerts = append(alerts, models.BusinessAlert{
			Key:         "failed_payments_spike",
			Severity:    "high",
			Title:       "Failed payment spike",
			Description: "More than 3 failed invoices in the last 24h.",
			Value:       fmt.Sprintf("%d", failed24h),
			Threshold:   ">= 3",
		})
	}

	var churned int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_subscription_history
		WHERE created_at >= NOW() - MAKE_INTERVAL(days => $1)
		  AND (status IN ('canceled', 'incomplete_expired') OR event_type LIKE '%deleted')
	`, days).Scan(&churned); err != nil {
		return nil, err
	}
	overview, err := s.GetBusinessOverview(days)
	if err != nil {
		return nil, err
	}
	denom := overview.ActivePaidAccounts + churned
	if denom > 0 {
		churnPct := float64(churned) * 100 / float64(denom)
		if churnPct >= 10 {
			alerts = append(alerts, models.BusinessAlert{
				Key:         "churn_spike",
				Severity:    "high",
				Title:       "Churn spike",
				Description: "Cancellation ratio crossed the configured threshold.",
				Value:       fmt.Sprintf("%.1f%%", churnPct),
				Threshold:   ">= 10%",
			})
		}
	}
	if overview.WebhookLagMinutes >= 20 {
		alerts = append(alerts, models.BusinessAlert{
			Key:         "webhook_lag",
			Severity:    "medium",
			Title:       "Stripe webhook lag",
			Description: "No fresh Stripe webhook events were processed recently.",
			Value:       fmt.Sprintf("%d min", overview.WebhookLagMinutes),
			Threshold:   ">= 20 min",
		})
	}
	if overview.ReconcileLagMinutes >= 120 {
		alerts = append(alerts, models.BusinessAlert{
			Key:         "reconcile_lag",
			Severity:    "medium",
			Title:       "Reconcile lag",
			Description: "Billing reconciliation is older than expected.",
			Value:       fmt.Sprintf("%d min", overview.ReconcileLagMinutes),
			Threshold:   ">= 120 min",
		})
	}
	return alerts, nil
}

func (s *PostgresStore) sumPaidRevenueInWindow(days int) (float64, error) {
	rows, err := s.db.Query(`
		SELECT UPPER(COALESCE(currency, 'EUR')), COALESCE(SUM(amount_paid), 0)
		FROM stripe_invoice_summaries
		WHERE paid = TRUE
		  AND created_at >= NOW() - MAKE_INTERVAL(days => $1)
		GROUP BY UPPER(COALESCE(currency, 'EUR'))
	`, days)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	total := 0.0
	for rows.Next() {
		var currency string
		var amount int64
		if err := rows.Scan(&currency, &amount); err != nil {
			return 0, err
		}
		total += amountToEUR(float64(amount), currency)
	}
	return total, rows.Err()
}

func (s *PostgresStore) sumPaidRevenueRange(fromDaysAgo, toDaysAgo int) (float64, error) {
	rows, err := s.db.Query(`
		SELECT UPPER(COALESCE(currency, 'EUR')), COALESCE(SUM(amount_paid), 0)
		FROM stripe_invoice_summaries
		WHERE paid = TRUE
		  AND created_at < NOW() - MAKE_INTERVAL(days => $1)
		  AND created_at >= NOW() - MAKE_INTERVAL(days => $2)
		GROUP BY UPPER(COALESCE(currency, 'EUR'))
	`, toDaysAgo, fromDaysAgo)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	total := 0.0
	for rows.Next() {
		var currency string
		var amount int64
		if err := rows.Scan(&currency, &amount); err != nil {
			return 0, err
		}
		total += amountToEUR(float64(amount), currency)
	}
	return total, rows.Err()
}

func (s *PostgresStore) fillPGSearchOpsBreakdown(days int, column string, out *map[string]int) error {
	query := `
		SELECT ` + column + `, COUNT(*)
		FROM search_run_log
		WHERE started_at >= NOW() - MAKE_INTERVAL(days => $1)
		GROUP BY ` + column
	rows, err := s.db.Query(query, days)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		if strings.TrimSpace(key) == "" {
			key = "unknown"
		}
		(*out)[key] = count
	}
	return rows.Err()
}

func (s *PostgresStore) ListRecentListings(userID string, limit int, missionID int64) ([]models.Listing, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT item_id, profile_id, title, price, price_type, image_urls,
		       url, condition, marketplace_id,
		       score, fair_price, offer_price, confidence, reasoning, risk_flags,
		       COALESCE(recommended_action, 'ask_seller'),
		       comparables_count, comparables_median_age_days,
		       last_seen, feedback,
		       COALESCE(currency_status, ''),
		       COALESCE(outreach_status, 'none')
		FROM listings
		WHERE item_id LIKE $1
		  AND ($2 = 0 OR profile_id = $2)
		  AND COALESCE(feedback, '') <> 'dismissed'
		ORDER BY last_seen DESC
		LIMIT $3
	`, scopedItemPrefix(userID), missionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON string
		if err := rows.Scan(
			&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
			&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
			&listing.Date, &listing.Feedback,
			&listing.CurrencyStatus,
			&listing.OutreachStatus,
		); err != nil {
			return nil, err
		}
		listing.ItemID = unscopedItemID(listing.ItemID)
		if strings.TrimSpace(listing.MarketplaceID) == "" {
			listing.MarketplaceID = "marktplaats"
		}
		listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
		_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
		_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
		listings = append(listings, listing)
	}
	return listings, rows.Err()
}

// ListRecentListingsPaginated returns a page of listings with server-side
// filtering and sorting. See models.MatchesFilter for the full contract.
//
// Default (zero-value filter) reproduces the Phase 1 ordering:
// last_seen DESC, item_id ASC — byte-for-byte identical to the pre-Phase-3
// behaviour when no filter params are supplied.
func (s *PostgresStore) ListRecentListingsPaginated(userID string, limit, offset int, missionID int64, filter models.MatchesFilter) ([]models.Listing, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	prefix := scopedItemPrefix(userID)

	// Build the shared WHERE clause extras and positional args. Postgres uses
	// $N placeholders; the base params occupy $1..$2 so extras start at $3.
	//
	// Base: ($1=prefix, $2=missionID) — Postgres reuses $2 in both the
	// equality check and the IS-zero guard within ($2 = 0 OR profile_id = $2),
	// so only 2 driver args are needed for the base clause.
	// N tracks the last placeholder index already used; extras claim n+1, n+2, …
	whereExtras := ""
	extraArgs := []any{}
	n := 2 // last used placeholder; next extra gets $3

	if filter.Market != "" {
		n++
		whereExtras += fmt.Sprintf("\n  AND l.marketplace_id = $%d", n)
		extraArgs = append(extraArgs, filter.Market)
	}
	if filter.Condition != "" {
		n++
		whereExtras += fmt.Sprintf("\n  AND l.condition = $%d", n)
		extraArgs = append(extraArgs, filter.Condition)
	}
	if filter.MinScore > 0 {
		n++
		whereExtras += fmt.Sprintf("\n  AND l.score >= $%d", n)
		extraArgs = append(extraArgs, float64(filter.MinScore))
	}

	baseArgs := []any{prefix, missionID}
	countArgs := append(baseArgs, extraArgs...)

	// Count query — same WHERE, no LIMIT/OFFSET.
	// Uses "l." alias so whereExtras (which uses l.) is consistent.
	countQuery := `
		SELECT COUNT(*)
		FROM listings l
		WHERE l.item_id LIKE $1
		  AND ($2 = 0 OR l.profile_id = $2)
		  AND COALESCE(l.feedback, '') <> 'dismissed'` + whereExtras

	var total int
	if err := s.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// ORDER BY per sort mode — every mode includes the unique item_id tie-breaker.
	// Postgres supports NULLS LAST natively.
	orderBy := postgresOrderBy(filter.Sort)

	// LIMIT and OFFSET occupy the next two placeholders; user_id for the
	// outreach LEFT JOIN occupies the one after that.
	limitPlaceholder := n + 1
	offsetPlaceholder := n + 2
	userIDPlaceholder := n + 3
	pageArgs := append(countArgs, limit, offset, userID)
	pageQuery := fmt.Sprintf(`
		SELECT l.item_id, l.profile_id, l.title, l.price, l.price_type, l.image_urls,
		       l.url, l.condition, l.marketplace_id,
		       l.score, l.fair_price, l.offer_price, l.confidence, l.reasoning, l.risk_flags,
		       COALESCE(l.recommended_action, 'ask_seller'),
		       l.comparables_count, l.comparables_median_age_days,
		       l.last_seen, COALESCE(l.feedback, ''),
		       COALESCE(l.currency_status, ''),
		       COALESCE(l.outreach_status, 'none'),
		       ot.sent_at AS outreach_sent_at
		FROM listings l
		LEFT JOIN outreach_threads ot
		    ON ot.user_id = $%d
		   AND ot.listing_id = SPLIT_PART(l.item_id, '::', 2)
		   AND ot.marketplace_id = l.marketplace_id
		WHERE l.item_id LIKE $1
		  AND ($2 = 0 OR l.profile_id = $2)
		  AND COALESCE(l.feedback, '') <> 'dismissed'`+whereExtras+`
		%s
		LIMIT $%d OFFSET $%d`, userIDPlaceholder, orderBy, limitPlaceholder, offsetPlaceholder)

	rows, err := s.db.Query(pageQuery, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON string
		if err := rows.Scan(
			&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
			&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
			&listing.Date, &listing.Feedback,
			&listing.CurrencyStatus,
			&listing.OutreachStatus,
			&listing.OutreachSentAt,
		); err != nil {
			return nil, 0, err
		}
		listing.ItemID = unscopedItemID(listing.ItemID)
		if strings.TrimSpace(listing.MarketplaceID) == "" {
			listing.MarketplaceID = "marktplaats"
		}
		listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
		_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
		_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
		listings = append(listings, listing)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return listings, total, nil
}

// postgresOrderBy returns the ORDER BY clause for the given sort mode.
// All modes include item_id ASC as a unique tie-breaker.
//
// Price sorts treat offer_price = 0 as unknown/unparsed (same as NULL) and
// push those rows to the end of results in both price_asc and price_desc,
// matching the SQLite CASE-based behaviour in sqliteOrderBy.
func postgresOrderBy(sort string) string {
	switch sort {
	case "score":
		return "ORDER BY score DESC, item_id ASC"
	case "price_asc":
		return "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price ASC NULLS LAST, item_id ASC"
	case "price_desc":
		return "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price DESC NULLS LAST, item_id ASC"
	default: // "newest" and zero-value — preserves Phase 1 default
		return "ORDER BY last_seen DESC, item_id ASC"
	}
}

func (s *PostgresStore) GetListing(userID, itemID string) (*models.Listing, error) {
	row := s.db.QueryRow(`
		SELECT l.item_id, l.profile_id, l.title, l.price, l.price_type, l.image_urls,
		       l.url, l.condition, l.marketplace_id,
		       l.score, l.fair_price, l.offer_price, l.confidence, l.reasoning, l.risk_flags,
		       COALESCE(l.recommended_action, 'ask_seller'),
		       l.comparables_count, l.comparables_median_age_days,
		       l.last_seen, COALESCE(l.feedback, ''),
		       COALESCE(l.currency_status, ''),
		       COALESCE(l.outreach_status, 'none'),
		       ot.sent_at AS outreach_sent_at
		FROM listings l
		LEFT JOIN outreach_threads ot
		    ON ot.user_id = $2
		   AND ot.listing_id = SPLIT_PART(l.item_id, '::', 2)
		   AND ot.marketplace_id = l.marketplace_id
		WHERE l.item_id = $1
	`, scopedItemID(userID, itemID), userID)

	var listing models.Listing
	var imageURLsJSON, riskFlagsJSON string
	if err := row.Scan(
		&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
		&listing.URL, &listing.Condition, &listing.MarketplaceID,
		&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
		&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
		&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
		&listing.Date, &listing.Feedback,
		&listing.CurrencyStatus,
		&listing.OutreachStatus,
		&listing.OutreachSentAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	listing.ItemID = unscopedItemID(listing.ItemID)
	if strings.TrimSpace(listing.MarketplaceID) == "" {
		listing.MarketplaceID = "marktplaats"
	}
	listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
	_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
	_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
	return &listing, nil
}

// SetListingFeedback marks a listing as approved/dismissed or clears feedback.
// feedback must be one of "", "approved", "dismissed".
func (s *PostgresStore) SetListingFeedback(userID, itemID, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	switch feedback {
	case "", "approved", "dismissed":
	default:
		return fmt.Errorf("invalid feedback value %q", feedback)
	}
	_, err := s.db.Exec(`
		UPDATE listings
		SET feedback = $1,
		    feedback_at = CASE WHEN $1 = '' THEN NULL ELSE NOW() END
		WHERE item_id = $2
	`, feedback, scopedItemID(userID, itemID))
	return err
}

// UpdateOutreachStatus sets the outreach lifecycle status for a listing owned
// by the given user. Status must be one of: none, sent, replied, won, lost.
// Returns ErrListingNotFound when the listing does not exist or is not owned by userID.
func (s *PostgresStore) UpdateOutreachStatus(ctx context.Context, userID, itemID, status string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE listings
		SET outreach_status = $1
		WHERE item_id = $2
	`, status, scopedItemID(userID, itemID))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrListingNotFound
	}
	return nil
}

// GetApprovedComparables returns listings the user has explicitly approved for
// this mission. They act as high-confidence comparables when scoring new
// listings — the scorer treats them as ground-truth relevant hits so the
// reasoner can calibrate fair value and relevance against confirmed matches.
func (s *PostgresStore) GetApprovedComparables(userID string, missionID int64, limit int) ([]models.ComparableDeal, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT item_id, title, price, score, first_seen
		FROM listings
		WHERE item_id LIKE $1
		  AND feedback = 'approved'
		  AND ($2 = 0 OR profile_id = $2)
		  AND price > 0
		ORDER BY feedback_at DESC
		LIMIT $3
	`, scopedItemPrefix(userID), missionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deals []models.ComparableDeal
	for rows.Next() {
		var deal models.ComparableDeal
		if err := rows.Scan(&deal.ItemID, &deal.Title, &deal.Price, &deal.Score, &deal.LastSeen); err != nil {
			return nil, err
		}
		deal.ItemID = unscopedItemID(deal.ItemID)
		deal.Similarity = 1.0
		deal.MatchReason = "user-approved match"
		deals = append(deals, deal)
	}
	return deals, rows.Err()
}

func (s *PostgresStore) ListActionDrafts(userID string) ([]models.ActionDraft, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, item_id, action_type, content, status, created_at
		FROM action_log
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var drafts []models.ActionDraft
	for rows.Next() {
		var draft models.ActionDraft
		if err := rows.Scan(&draft.ID, &draft.UserID, &draft.ItemID, &draft.ActionType, &draft.Content, &draft.Status, &draft.CreatedAt); err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

func (s *PostgresStore) IsNew(userID, itemID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM listings WHERE item_id = $1`, scopedItemID(userID, itemID)).Scan(&count)
	return count == 0, err
}

func (s *PostgresStore) GetListingScore(userID, itemID string) (float64, bool, error) {
	var score float64
	err := s.db.QueryRow(`SELECT score FROM listings WHERE item_id = $1`, scopedItemID(userID, itemID)).Scan(&score)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return score, err == nil, err
}

func (s *PostgresStore) GetListingScoringState(userID, itemID string) (price int, reasoningSource string, comparablesCount int, found bool, err error) {
	err = s.db.QueryRow(`
		SELECT price, reasoning_source, comparables_count
		FROM listings
		WHERE item_id = $1
	`, scopedItemID(userID, itemID)).Scan(&price, &reasoningSource, &comparablesCount)
	if err == sql.ErrNoRows {
		return 0, "", 0, false, nil
	}
	if err != nil {
		return 0, "", 0, false, err
	}
	return price, reasoningSource, comparablesCount, true, nil
}

func (s *PostgresStore) GetAIScoreCache(cacheKey string, promptVersion int) (score float64, reasoning string, found bool, err error) {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return 0, "", false, nil
	}
	if promptVersion <= 0 {
		promptVersion = 1
	}
	err = s.db.QueryRow(`
		SELECT score, reasoning
		FROM ai_score_cache
		WHERE key = $1 AND prompt_version = $2
	`, cacheKey, promptVersion).Scan(&score, &reasoning)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return score, reasoning, true, nil
}

func (s *PostgresStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlags := scored.RiskFlags
	if riskFlags == nil {
		riskFlags = []string{}
	}
	riskFlagsJSON, _ := json.Marshal(riskFlags)
	recommendedAction := scored.RecommendedAction
	if recommendedAction == "" {
		recommendedAction = "ask_seller"
	}
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, profile_id, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, reasoning_source, risk_flags,
			recommended_action,
			comparables_count, comparables_median_age_days,
			currency_status,
			first_seen, last_seen
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,NOW(),NOW())
		ON CONFLICT(item_id) DO UPDATE SET
			price                       = EXCLUDED.price,
			score                       = EXCLUDED.score,
			profile_id                  = EXCLUDED.profile_id,
			image_urls                  = EXCLUDED.image_urls,
			url                         = EXCLUDED.url,
			condition                   = EXCLUDED.condition,
			marketplace_id              = EXCLUDED.marketplace_id,
			fair_price                  = EXCLUDED.fair_price,
			offer_price                 = EXCLUDED.offer_price,
			confidence                  = EXCLUDED.confidence,
			reasoning                   = EXCLUDED.reasoning,
			reasoning_source            = EXCLUDED.reasoning_source,
			risk_flags                  = EXCLUDED.risk_flags,
			recommended_action          = EXCLUDED.recommended_action,
			comparables_count           = EXCLUDED.comparables_count,
			comparables_median_age_days = EXCLUDED.comparables_median_age_days,
			currency_status             = EXCLUDED.currency_status,
			last_seen                   = NOW()
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, l.ProfileID, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, scored.ReasoningSource, string(riskFlagsJSON),
		recommendedAction,
		scored.ComparablesCount, scored.ComparablesMedianAgeDays,
		l.CurrencyStatus,
	)
	return err
}

func (s *PostgresStore) TouchListing(userID, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET last_seen = NOW() WHERE item_id = $1`, scopedItemID(userID, itemID))
	return err
}

func (s *PostgresStore) SetAIScoreCache(cacheKey string, score float64, reasoning string, promptVersion int) error {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return nil
	}
	if promptVersion <= 0 {
		promptVersion = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO ai_score_cache (key, score, reasoning, created_at, prompt_version)
		VALUES ($1, $2, $3, (EXTRACT(EPOCH FROM NOW()))::BIGINT, $4)
		ON CONFLICT(key) DO UPDATE SET
			score = EXCLUDED.score,
			reasoning = EXCLUDED.reasoning,
			created_at = EXCLUDED.created_at,
			prompt_version = EXCLUDED.prompt_version
	`, cacheKey, score, strings.TrimSpace(reasoning), promptVersion)
	return err
}

func (s *PostgresStore) RecordPrice(query string, modelKey string, categoryID int, marketplaceID string, price int) error {
	_, err := s.db.Exec(
		`INSERT INTO price_history (query, model_key, category_id, marketplace_id, price) VALUES ($1, $2, $3, $4, $5)`,
		query, modelKey, categoryID, marketplaceID, price,
	)
	return err
}

func (s *PostgresStore) GetMarketAverage(query string, modelKey string, categoryID int, marketplaceID string, minSamples int) (int, bool, error) {
	source := "none"
	avgResult := 0

	// Try model_key pool first when available.
	if modelKey != "" {
		avg, ok, err := s.marketAverageByKey(
			`model_key = $1 AND marketplace_id = $2`, modelKey, marketplaceID, minSamples,
		)
		if err != nil {
			slog.Info("market_average_resolved",
				"query", query,
				"model_key", modelKey,
				"source", "none",
				"avg_cents", 0,
				"marketplace_id", marketplaceID,
			)
			return 0, false, err
		}
		if ok {
			slog.Info("market_average_resolved",
				"query", query,
				"model_key", modelKey,
				"source", "model_key",
				"avg_cents", avg,
				"marketplace_id", marketplaceID,
			)
			return avg, true, nil
		}
		// model_key pool insufficient — fall through to query pool.
	}

	// Fall back to raw query pool (existing behaviour).
	avg, ok, err := s.marketAverageByKey(
		`query = $1 AND marketplace_id = $2`, query, marketplaceID, minSamples,
	)
	if err != nil {
		slog.Info("market_average_resolved",
			"query", query,
			"model_key", modelKey,
			"source", source,
			"avg_cents", avgResult,
			"marketplace_id", marketplaceID,
		)
		return 0, false, err
	}
	if ok {
		source = "query"
		avgResult = avg
	}
	slog.Info("market_average_resolved",
		"query", query,
		"model_key", modelKey,
		"source", source,
		"avg_cents", avgResult,
		"marketplace_id", marketplaceID,
	)
	return avgResult, ok, nil
}

// marketAverageByKey runs the shared average query with a caller-supplied WHERE
// clause. whereClause must use $1 and $2 as positional parameters for keyVal
// and marketplaceID respectively; minSamples is passed as $3.
//
// NOTE: category_id filter is intentionally omitted — model_key is already
// category-specific via brand+model tokens. Including category_id would
// produce false misses for broad-category missions (category_id=0).
func (s *PostgresStore) marketAverageByKey(whereClause string, keyVal string, marketplaceID string, minSamples int) (int, bool, error) {
	var avg sql.NullFloat64
	var count int
	err := s.db.QueryRow(fmt.Sprintf(`
		SELECT AVG(price)::float8, COUNT(*)
		FROM (
			SELECT price FROM price_history
			WHERE %s
			  AND timestamp > NOW() - INTERVAL '30 days'
			ORDER BY timestamp DESC
			LIMIT $3
		) recent
	`, whereClause), keyVal, marketplaceID, minSamples).Scan(&avg, &count)
	if err != nil {
		return 0, false, err
	}
	if count < minSamples || !avg.Valid {
		return 0, false, nil
	}
	return int(avg.Float64), true, nil
}

func (s *PostgresStore) MarkOffered(userID, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET offered = TRUE WHERE item_id = $1`, scopedItemID(userID, itemID))
	return err
}

func (s *PostgresStore) WasOffered(userID, itemID string) (bool, error) {
	var offered bool
	err := s.db.QueryRow(`SELECT offered FROM listings WHERE item_id = $1`, scopedItemID(userID, itemID)).Scan(&offered)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return offered, err
}

func (s *PostgresStore) GetPriceHistory(query string) ([]models.PricePoint, error) {
	rows, err := s.db.Query(`
		SELECT query, price, timestamp
		FROM price_history
		WHERE query = $1
		ORDER BY timestamp DESC
		LIMIT 100
	`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []models.PricePoint
	for rows.Next() {
		var p models.PricePoint
		if err := rows.Scan(&p.Query, &p.Price, &p.Timestamp); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

func (s *PostgresStore) GetComparableDeals(userID, query, excludeItemID string, limit int) ([]models.ComparableDeal, error) {
	rows, err := s.db.Query(`
		SELECT item_id, title, price, score, first_seen
		FROM listings
		WHERE query = $1
		  AND item_id LIKE $2
		  AND item_id != $3
		  AND price > 0
		  AND COALESCE(feedback, '') <> 'dismissed'
		ORDER BY last_seen DESC
		LIMIT $4
	`, query, scopedItemPrefix(userID), scopedItemID(userID, excludeItemID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deals []models.ComparableDeal
	for rows.Next() {
		var deal models.ComparableDeal
		if err := rows.Scan(&deal.ItemID, &deal.Title, &deal.Price, &deal.Score, &deal.LastSeen); err != nil {
			return nil, err
		}
		deal.ItemID = unscopedItemID(deal.ItemID)
		deal.MatchReason = strings.TrimSpace(deal.Title)
		deals = append(deals, deal)
	}
	return deals, rows.Err()
}

func scanPGMission(scanner interface{ Scan(dest ...any) error }) (models.Mission, error) {
	return scanPGMissionInternal(scanner, false)
}

func scanPGMissionWithStats(scanner interface{ Scan(dest ...any) error }) (models.Mission, error) {
	return scanPGMissionInternal(scanner, true)
}

func scanPGMissionInternal(scanner interface{ Scan(dest ...any) error }, withStats bool) (models.Mission, error) {
	var mission models.Mission
	var preferredJSON, requiredJSON, niceJSON, queriesJSON, avoidFlagsJSON, marketplaceScopeJSON string
	if withStats {
		err := scanner.Scan(
			&mission.ID, &mission.UserID, &mission.Name, &mission.TargetQuery, &mission.CategoryID,
			&mission.BudgetMax, &mission.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
			&mission.RiskTolerance, &mission.ZipCode, &mission.Distance, &queriesJSON,
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.CountryCode, &mission.Region, &mission.City, &mission.PostalCode,
			&mission.CrossBorderEnabled, &marketplaceScopeJSON, &mission.Category,
			&mission.Active, &mission.CreatedAt, &mission.UpdatedAt, &mission.MatchCount, &mission.LastMatchAt,
		)
		if err != nil {
			return mission, err
		}
	} else {
		err := scanner.Scan(
			&mission.ID, &mission.UserID, &mission.Name, &mission.TargetQuery, &mission.CategoryID,
			&mission.BudgetMax, &mission.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
			&mission.RiskTolerance, &mission.ZipCode, &mission.Distance, &queriesJSON,
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.CountryCode, &mission.Region, &mission.City, &mission.PostalCode,
			&mission.CrossBorderEnabled, &marketplaceScopeJSON, &mission.Category,
			&mission.Active, &mission.CreatedAt, &mission.UpdatedAt,
		)
		if err != nil {
			return mission, err
		}
	}
	_ = json.Unmarshal([]byte(preferredJSON), &mission.PreferredCondition)
	_ = json.Unmarshal([]byte(requiredJSON), &mission.RequiredFeatures)
	_ = json.Unmarshal([]byte(niceJSON), &mission.NiceToHave)
	_ = json.Unmarshal([]byte(queriesJSON), &mission.SearchQueries)
	_ = json.Unmarshal([]byte(avoidFlagsJSON), &mission.AvoidFlags)
	_ = json.Unmarshal([]byte(marketplaceScopeJSON), &mission.MarketplaceScope)
	mission.CountryCode = strings.ToUpper(strings.TrimSpace(mission.CountryCode))
	mission.MarketplaceScope = normalizeMarketplaceScope(mission.MarketplaceScope)
	if mission.TravelRadius == 0 && mission.Distance > 0 {
		mission.TravelRadius = mission.Distance / 1000
	}
	if strings.TrimSpace(mission.Status) == "" {
		if mission.Active {
			mission.Status = "active"
		} else {
			mission.Status = "paused"
		}
	}
	if strings.TrimSpace(mission.Urgency) == "" {
		mission.Urgency = "flexible"
	}
	if strings.TrimSpace(mission.Category) == "" {
		mission.Category = "other"
	}
	return mission, nil
}

func scanPGUser(row *sql.Row) (*models.User, error) {
	var user models.User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.Role, &user.IsAdmin, &user.StripeCustomer,
		&user.CountryCode, &user.Region, &user.City, &user.PostalCode, &user.PreferredRadiusKm, &user.CrossBorderEnabled, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	user.CountryCode = strings.ToUpper(strings.TrimSpace(user.CountryCode))
	return &user, nil
}

func scanPGSearchSpec(scanner interface{ Scan(dest ...any) error }) (models.SearchSpec, error) {
	var spec models.SearchSpec
	var conditionJSON, attributesJSON string
	var checkIntervalSeconds int64
	var nextRunAt, lastRunAt, lastSignalAt, lastErrorAt sql.NullTime
	err := scanner.Scan(
		&spec.ID, &spec.UserID, &spec.ProfileID, &spec.Name, &spec.Query, &spec.MarketplaceID, &spec.CountryCode, &spec.City, &spec.PostalCode, &spec.RadiusKm, &spec.CategoryID,
		&spec.MaxPrice, &spec.MinPrice, &conditionJSON, &spec.OfferPercentage, &spec.AutoMessage,
		&spec.MessageTemplate, &attributesJSON, &spec.Enabled, &checkIntervalSeconds, &spec.PriorityClass,
		&nextRunAt, &lastRunAt, &lastSignalAt, &lastErrorAt, &spec.LastResultCount, &spec.ConsecutiveEmptyRuns, &spec.ConsecutiveFailures,
	)
	if err != nil {
		return spec, err
	}
	spec.MarketplaceID = marketplace.NormalizeMarketplaceID(spec.MarketplaceID)
	spec.CountryCode = strings.ToUpper(strings.TrimSpace(spec.CountryCode))
	spec.CheckInterval = time.Duration(checkIntervalSeconds) * time.Second
	_ = json.Unmarshal([]byte(conditionJSON), &spec.Condition)
	_ = json.Unmarshal([]byte(attributesJSON), &spec.Attributes)
	if nextRunAt.Valid {
		spec.NextRunAt = nextRunAt.Time
	}
	if lastRunAt.Valid {
		spec.LastRunAt = lastRunAt.Time
	}
	if lastSignalAt.Valid {
		spec.LastSignalAt = lastSignalAt.Time
	}
	if lastErrorAt.Valid {
		spec.LastErrorAt = lastErrorAt.Time
	}
	return spec, nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func scanShortlistEntry(scanner interface{ Scan(dest ...any) error }) (models.ShortlistEntry, error) {
	var entry models.ShortlistEntry
	var concernsJSON, questionsJSON string
	err := scanner.Scan(
		&entry.ID, &entry.UserID, &entry.MissionID, &entry.ItemID, &entry.Title, &entry.URL,
		&entry.RecommendationLabel, &entry.RecommendationScore, &entry.AskPrice, &entry.FairPrice,
		&entry.Verdict, &concernsJSON, &questionsJSON, &entry.Status, &entry.CreatedAt, &entry.UpdatedAt,
		&entry.Condition, &entry.MarketplaceID, &entry.OutreachStatus,
	)
	if err != nil {
		return entry, err
	}
	_ = json.Unmarshal([]byte(concernsJSON), &entry.Concerns)
	_ = json.Unmarshal([]byte(questionsJSON), &entry.SuggestedQuestions)
	return entry, nil
}

func randomPostgresID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
