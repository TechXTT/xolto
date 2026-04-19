package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

type CleanupStats struct {
	ListingsDeleted     int64
	PriceHistoryDeleted int64
}

type DBPoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

const (
	defaultDBMaxOpenConns    = 25
	defaultDBMaxIdleConns    = 5
	defaultDBConnMaxLifetime = 30 * time.Minute
)

func DefaultDBPoolConfig() DBPoolConfig {
	return DBPoolConfig{
		MaxOpenConns:    defaultDBMaxOpenConns,
		MaxIdleConns:    defaultDBMaxIdleConns,
		ConnMaxLifetime: defaultDBConnMaxLifetime,
	}
}

func NormalizeDBPoolConfig(cfg DBPoolConfig) DBPoolConfig {
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = defaultDBMaxOpenConns
	}
	if cfg.MaxIdleConns < 0 {
		cfg.MaxIdleConns = defaultDBMaxIdleConns
	}
	if cfg.MaxIdleConns > cfg.MaxOpenConns {
		cfg.MaxIdleConns = cfg.MaxOpenConns
	}
	if cfg.ConnMaxLifetime <= 0 {
		cfg.ConnMaxLifetime = defaultDBConnMaxLifetime
	}
	return cfg
}

func applyDBPoolConfig(db *sql.DB, cfg DBPoolConfig) DBPoolConfig {
	cfg = NormalizeDBPoolConfig(cfg)
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	return cfg
}

var _ Store = (*SQLiteStore)(nil)

func New(dbPath string) (*SQLiteStore, error) {
	return NewWithPool(dbPath, DefaultDBPoolConfig())
}

func NewWithPool(dbPath string, poolCfg DBPoolConfig) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	applyDBPoolConfig(db, poolCfg)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting journal mode: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS listings (
			item_id     TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			price       INTEGER NOT NULL,
			price_type  TEXT NOT NULL DEFAULT '',
			score       REAL NOT NULL DEFAULT 0,
			reasoning_source TEXT NOT NULL DEFAULT '',
			offered     INTEGER NOT NULL DEFAULT 0,
			query       TEXT NOT NULL DEFAULT '',
			profile_id  INTEGER NOT NULL DEFAULT 0,
			image_urls  TEXT NOT NULL DEFAULT '[]',
			first_seen  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS price_history (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			query          TEXT NOT NULL,
			category_id    INTEGER NOT NULL DEFAULT 0,
			marketplace_id TEXT NOT NULL DEFAULT '',
			price          INTEGER NOT NULL,
			timestamp      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_price_history_query ON price_history(query, timestamp);

		CREATE TABLE IF NOT EXISTS shopping_profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			target_query TEXT NOT NULL DEFAULT '',
			category_id INTEGER NOT NULL DEFAULT 0,
			budget_max INTEGER NOT NULL DEFAULT 0,
			budget_stretch INTEGER NOT NULL DEFAULT 0,
			preferred_condition TEXT NOT NULL DEFAULT '[]',
			required_features TEXT NOT NULL DEFAULT '[]',
			nice_to_have TEXT NOT NULL DEFAULT '[]',
			risk_tolerance TEXT NOT NULL DEFAULT 'balanced',
			zip_code TEXT NOT NULL DEFAULT '',
			distance INTEGER NOT NULL DEFAULT 0,
			search_queries TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			urgency TEXT NOT NULL DEFAULT 'flexible',
			avoid_flags TEXT NOT NULL DEFAULT '[]',
			travel_radius INTEGER NOT NULL DEFAULT 0,
			country_code TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			postal_code TEXT NOT NULL DEFAULT '',
			cross_border_enabled INTEGER NOT NULL DEFAULT 0,
			marketplace_scope TEXT NOT NULL DEFAULT '[]',
			category TEXT NOT NULL DEFAULT 'other',
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_shopping_profiles_user ON shopping_profiles(user_id, active);

		CREATE TABLE IF NOT EXISTS shortlist_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			profile_id INTEGER NOT NULL DEFAULT 0,
			item_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			recommendation_label TEXT NOT NULL DEFAULT '',
			recommendation_score REAL NOT NULL DEFAULT 0,
			ask_price INTEGER NOT NULL DEFAULT 0,
			fair_price INTEGER NOT NULL DEFAULT 0,
			verdict TEXT NOT NULL DEFAULT '',
			concerns TEXT NOT NULL DEFAULT '[]',
			suggested_questions TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'watching',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, item_id)
		);

		CREATE INDEX IF NOT EXISTS idx_shortlist_entries_user ON shortlist_entries(user_id, status);

		CREATE TABLE IF NOT EXISTS conversation_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			intent TEXT NOT NULL DEFAULT '',
			input_text TEXT NOT NULL DEFAULT '',
			output_text TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS assistant_sessions (
			user_id TEXT PRIMARY KEY,
			pending_intent TEXT NOT NULL DEFAULT '',
			pending_question TEXT NOT NULL DEFAULT '',
			draft_profile TEXT NOT NULL DEFAULT '',
			last_assistant_msg TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS action_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			item_id TEXT NOT NULL DEFAULT '',
			action_type TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
			cross_border_enabled INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS search_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			profile_id INTEGER NOT NULL DEFAULT 0,
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
			condition_json TEXT NOT NULL DEFAULT '[]',
			offer_percentage INTEGER NOT NULL DEFAULT 70,
			auto_message INTEGER NOT NULL DEFAULT 0,
			message_template TEXT NOT NULL DEFAULT '',
			attributes_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			check_interval_seconds INTEGER NOT NULL DEFAULT 300,
			priority_class INTEGER NOT NULL DEFAULT 0,
			next_run_at DATETIME NULL,
			last_run_at DATETIME NULL,
			last_signal_at DATETIME NULL,
			last_error_at DATETIME NULL,
			last_result_count INTEGER NOT NULL DEFAULT 0,
			consecutive_empty_runs INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_search_configs_user ON search_configs(user_id, enabled, updated_at);
		CREATE INDEX IF NOT EXISTS idx_search_configs_profile ON search_configs(profile_id, enabled, updated_at);
		CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen);

		CREATE TABLE IF NOT EXISTS stripe_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS stripe_processed_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			processed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_processed_events_processed_at ON stripe_processed_events(processed_at);

		CREATE TABLE IF NOT EXISTS user_auth_identities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			provider_subject TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '',
			email_verified INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, provider_subject),
			UNIQUE(user_id, provider)
		);

		CREATE INDEX IF NOT EXISTS idx_user_auth_identities_user ON user_auth_identities(user_id, provider);

		CREATE TABLE IF NOT EXISTS search_run_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			search_config_id INTEGER NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			mission_id INTEGER NOT NULL DEFAULT 0,
			plan TEXT NOT NULL DEFAULT '',
			marketplace_id TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			queue_wait_ms INTEGER NOT NULL DEFAULT 0,
			priority INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			results_found INTEGER NOT NULL DEFAULT 0,
			new_listings INTEGER NOT NULL DEFAULT 0,
			deal_hits INTEGER NOT NULL DEFAULT 0,
			throttled INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT '',
			searches_avoided INTEGER NOT NULL DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_search_run_log_started ON search_run_log(started_at);
		CREATE INDEX IF NOT EXISTS idx_search_run_log_marketplace ON search_run_log(marketplace_id, started_at);
		CREATE INDEX IF NOT EXISTS idx_search_run_log_country ON search_run_log(country_code, started_at);

		CREATE TABLE IF NOT EXISTS admin_audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			before_json TEXT NOT NULL DEFAULT '',
			after_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created ON admin_audit_log(created_at);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor ON admin_audit_log(actor_user_id, created_at);

		CREATE TABLE IF NOT EXISTS stripe_webhook_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			event_type TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			api_account TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'received',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 1,
			payload_json TEXT NOT NULL DEFAULT '',
			received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			processed_at DATETIME NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_received ON stripe_webhook_events(received_at);
		CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_status ON stripe_webhook_events(status, received_at);

		CREATE TABLE IF NOT EXISTS stripe_subscription_snapshots (
			subscription_id TEXT PRIMARY KEY,
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			plan_interval TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount INTEGER NOT NULL DEFAULT 0,
			quantity INTEGER NOT NULL DEFAULT 0,
			current_period_start DATETIME NULL,
			current_period_end DATETIME NULL,
			cancel_at_period_end INTEGER NOT NULL DEFAULT 0,
			canceled_at DATETIME NULL,
			paused INTEGER NOT NULL DEFAULT 0,
			latest_invoice_id TEXT NOT NULL DEFAULT '',
			default_payment_method TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_customer ON stripe_subscription_snapshots(customer_id, updated_at);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_status ON stripe_subscription_snapshots(status, updated_at);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_user ON stripe_subscription_snapshots(user_id, updated_at);

		CREATE TABLE IF NOT EXISTS stripe_subscription_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_id TEXT NOT NULL DEFAULT '',
			event_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount INTEGER NOT NULL DEFAULT 0,
			quantity INTEGER NOT NULL DEFAULT 0,
			period_start DATETIME NULL,
			period_end DATETIME NULL,
			cancel_at_period_end INTEGER NOT NULL DEFAULT 0,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_subscription_history_sub ON stripe_subscription_history(subscription_id, created_at);

		CREATE TABLE IF NOT EXISTS stripe_invoice_summaries (
			invoice_id TEXT PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			amount_due INTEGER NOT NULL DEFAULT 0,
			amount_paid INTEGER NOT NULL DEFAULT 0,
			amount_remaining INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			paid INTEGER NOT NULL DEFAULT 0,
			hosted_invoice_url TEXT NOT NULL DEFAULT '',
			invoice_pdf TEXT NOT NULL DEFAULT '',
			period_start DATETIME NULL,
			period_end DATETIME NULL,
			due_date DATETIME NULL,
			finalized_at DATETIME NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_customer ON stripe_invoice_summaries(customer_id, updated_at);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_status ON stripe_invoice_summaries(status, updated_at);
		CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_subscription ON stripe_invoice_summaries(subscription_id, updated_at);

		CREATE TABLE IF NOT EXISTS stripe_mutation_log (
			idempotency_key TEXT PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_stripe_mutation_log_actor ON stripe_mutation_log(actor_user_id, created_at);

			CREATE TABLE IF NOT EXISTS billing_reconcile_runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				triggered_by TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				summary_json TEXT NOT NULL DEFAULT '',
				error_json TEXT NOT NULL DEFAULT '',
				started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				finished_at DATETIME NULL
			);
			CREATE INDEX IF NOT EXISTS idx_billing_reconcile_runs_started ON billing_reconcile_runs(started_at DESC);

			CREATE TABLE IF NOT EXISTS ai_score_cache (
				key TEXT PRIMARY KEY,
				score REAL NOT NULL DEFAULT 0,
				reasoning TEXT NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER)),
				prompt_version INTEGER NOT NULL DEFAULT 1
			);
			CREATE INDEX IF NOT EXISTS idx_ai_score_cache_created ON ai_score_cache(created_at DESC);
		`)
	if err != nil {
		return err
	}
	// Add image_urls column to existing databases that pre-date this field.
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN image_urls TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN url TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN condition TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN marketplace_id TEXT NOT NULL DEFAULT 'marktplaats'`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN fair_price INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN offer_price INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN confidence REAL NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN reasoning_source TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN risk_flags TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN recommended_action TEXT NOT NULL DEFAULT 'ask_seller'`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN comparables_count INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN comparables_median_age_days INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN profile_id INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN feedback TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN feedback_at DATETIME NULL`)
	// XOL-33 (M2-D): currency_status tracks how the listing price was normalised.
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN currency_status TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_listings_feedback ON listings(profile_id, feedback)`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN urgency TEXT NOT NULL DEFAULT 'flexible'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN avoid_flags TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN travel_radius INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN region TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN cross_border_enabled INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN marketplace_scope TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN category TEXT NOT NULL DEFAULT 'other'`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN region TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN preferred_radius_km INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN cross_border_enabled INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN profile_id INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN country_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN city TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN postal_code TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN radius_km INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN priority_class INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN next_run_at DATETIME NULL`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN last_run_at DATETIME NULL`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN last_signal_at DATETIME NULL`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN last_error_at DATETIME NULL`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN last_result_count INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN consecutive_empty_runs INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_configs_profile ON search_configs(profile_id, enabled, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_configs_due ON search_configs(enabled, next_run_at, user_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen)`)

	// Admin & AI usage tracking.
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`UPDATE users SET role = 'admin' WHERE is_admin = 1 AND COALESCE(role, '') = ''`)
	_, _ = db.Exec(`UPDATE users SET role = 'user' WHERE is_admin = 0 AND COALESCE(role, '') = ''`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_usage_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL DEFAULT '',
			mission_id INTEGER NOT NULL DEFAULT 0,
			call_type TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			success INTEGER NOT NULL DEFAULT 1,
			error_msg TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`ALTER TABLE ai_usage_log ADD COLUMN mission_id INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_usage_user ON ai_usage_log(user_id, created_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_usage_user_mission ON ai_usage_log(user_id, mission_id, created_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_usage_created ON ai_usage_log(created_at)`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS user_auth_identities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			provider_subject TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '',
			email_verified INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, provider_subject),
			UNIQUE(user_id, provider)
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_auth_identities_user ON user_auth_identities(user_id, provider)`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS search_run_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			search_config_id INTEGER NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			mission_id INTEGER NOT NULL DEFAULT 0,
			plan TEXT NOT NULL DEFAULT '',
			marketplace_id TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			queue_wait_ms INTEGER NOT NULL DEFAULT 0,
			priority INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			results_found INTEGER NOT NULL DEFAULT 0,
			new_listings INTEGER NOT NULL DEFAULT 0,
			deal_hits INTEGER NOT NULL DEFAULT 0,
			throttled INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT '',
			searches_avoided INTEGER NOT NULL DEFAULT 0
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_run_log_started ON search_run_log(started_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_run_log_marketplace ON search_run_log(marketplace_id, started_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_run_log_country ON search_run_log(country_code, started_at)`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS admin_audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_user_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			before_json TEXT NOT NULL DEFAULT '',
			after_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created ON admin_audit_log(created_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor ON admin_audit_log(actor_user_id, created_at)`)
	_, _ = db.Exec(`ALTER TABLE admin_audit_log ADD COLUMN actor_role TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE admin_audit_log ADD COLUMN request_id TEXT NOT NULL DEFAULT ''`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_webhook_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			event_type TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			api_account TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'received',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 1,
			payload_json TEXT NOT NULL DEFAULT '',
			received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			processed_at DATETIME NULL
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_received ON stripe_webhook_events(received_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_status ON stripe_webhook_events(status, received_at)`)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_processed_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL UNIQUE,
			processed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_processed_events_processed_at ON stripe_processed_events(processed_at)`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_subscription_snapshots (
			subscription_id TEXT PRIMARY KEY,
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			plan_interval TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount INTEGER NOT NULL DEFAULT 0,
			quantity INTEGER NOT NULL DEFAULT 0,
			current_period_start DATETIME NULL,
			current_period_end DATETIME NULL,
			cancel_at_period_end INTEGER NOT NULL DEFAULT 0,
			canceled_at DATETIME NULL,
			paused INTEGER NOT NULL DEFAULT 0,
			latest_invoice_id TEXT NOT NULL DEFAULT '',
			default_payment_method TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_customer ON stripe_subscription_snapshots(customer_id, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_status ON stripe_subscription_snapshots(status, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_user ON stripe_subscription_snapshots(user_id, updated_at)`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_subscription_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_id TEXT NOT NULL DEFAULT '',
			event_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			plan_price_id TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			unit_amount INTEGER NOT NULL DEFAULT 0,
			quantity INTEGER NOT NULL DEFAULT 0,
			period_start DATETIME NULL,
			period_end DATETIME NULL,
			cancel_at_period_end INTEGER NOT NULL DEFAULT 0,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_subscription_history_sub ON stripe_subscription_history(subscription_id, created_at)`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_invoice_summaries (
			invoice_id TEXT PRIMARY KEY,
			subscription_id TEXT NOT NULL DEFAULT '',
			customer_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT '',
			amount_due INTEGER NOT NULL DEFAULT 0,
			amount_paid INTEGER NOT NULL DEFAULT 0,
			amount_remaining INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			paid INTEGER NOT NULL DEFAULT 0,
			hosted_invoice_url TEXT NOT NULL DEFAULT '',
			invoice_pdf TEXT NOT NULL DEFAULT '',
			period_start DATETIME NULL,
			period_end DATETIME NULL,
			due_date DATETIME NULL,
			finalized_at DATETIME NULL,
			raw_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_customer ON stripe_invoice_summaries(customer_id, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_status ON stripe_invoice_summaries(status, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_subscription ON stripe_invoice_summaries(subscription_id, updated_at)`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS stripe_mutation_log (
			idempotency_key TEXT PRIMARY KEY,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_mutation_log_actor ON stripe_mutation_log(actor_user_id, created_at)`)

	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS billing_reconcile_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			triggered_by TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			summary_json TEXT NOT NULL DEFAULT '',
			error_json TEXT NOT NULL DEFAULT '',
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME NULL
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_billing_reconcile_runs_started ON billing_reconcile_runs(started_at DESC)`)

	// XOL-24: outreach thread reply-time tracking.
	migrateOutreachThreadsSQLite(db)

	// XOL-53 SUP-2: support events intake table.
	migrateSupportEventsSQLite(db)

	// XOL-71: isolate price_history by marketplace_id.
	_, _ = db.Exec(`ALTER TABLE price_history ADD COLUMN marketplace_id TEXT NOT NULL DEFAULT ''`)

	// XOL-79 (C-6): outreach lifecycle status per saved listing.
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN outreach_status TEXT NOT NULL DEFAULT 'none'`)

	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Cleanup removes stored listing and/or price-history state.
func (s *SQLiteStore) Cleanup(includeListings, includePriceHistory bool) (CleanupStats, error) {
	stats := CleanupStats{}

	tx, err := s.db.Begin()
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	if includeListings {
		result, err := tx.Exec("DELETE FROM listings")
		if err != nil {
			return stats, err
		}
		stats.ListingsDeleted, _ = result.RowsAffected()
	}

	if includePriceHistory {
		result, err := tx.Exec("DELETE FROM price_history")
		if err != nil {
			return stats, err
		}
		stats.PriceHistoryDeleted, _ = result.RowsAffected()
	}

	if err := tx.Commit(); err != nil {
		return stats, err
	}

	return stats, nil
}

func (s *SQLiteStore) UpsertMission(mission models.Mission) (int64, error) {
	conditionsJSON, err := json.Marshal(mission.PreferredCondition)
	if err != nil {
		return 0, err
	}
	requiredJSON, err := json.Marshal(mission.RequiredFeatures)
	if err != nil {
		return 0, err
	}
	niceToHaveJSON, err := json.Marshal(mission.NiceToHave)
	if err != nil {
		return 0, err
	}
	queriesJSON, err := json.Marshal(mission.SearchQueries)
	if err != nil {
		return 0, err
	}
	avoidFlagsJSON, err := json.Marshal(mission.AvoidFlags)
	if err != nil {
		return 0, err
	}
	marketplaceScopeJSON, err := json.Marshal(normalizeMarketplaceScope(mission.MarketplaceScope))
	if err != nil {
		return 0, err
	}

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
		_, err = s.db.Exec(`
			UPDATE shopping_profiles
			SET name = ?, target_query = ?, category_id = ?, budget_max = ?, budget_stretch = ?,
				preferred_condition = ?, required_features = ?, nice_to_have = ?, risk_tolerance = ?,
				zip_code = ?, distance = ?, search_queries = ?,
				status = ?, urgency = ?, avoid_flags = ?, travel_radius = ?, country_code = ?, region = ?, city = ?,
				postal_code = ?, cross_border_enabled = ?, marketplace_scope = ?, category = ?,
				active = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`,
			mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
			string(conditionsJSON), string(requiredJSON), string(niceToHaveJSON), mission.RiskTolerance,
			mission.ZipCode, mission.Distance, string(queriesJSON),
			mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius,
			strings.ToUpper(strings.TrimSpace(mission.CountryCode)), strings.TrimSpace(mission.Region), strings.TrimSpace(mission.City),
			strings.TrimSpace(mission.PostalCode), boolToInt(mission.CrossBorderEnabled), string(marketplaceScopeJSON), mission.Category,
			boolToInt(mission.Active), mission.ID,
		)
		if err != nil {
			return 0, err
		}
		return mission.ID, nil
	}

	result, err := s.db.Exec(`
		INSERT INTO shopping_profiles (
			user_id, name, target_query, category_id, budget_max, budget_stretch,
			preferred_condition, required_features, nice_to_have, risk_tolerance,
			zip_code, distance, search_queries,
			status, urgency, avoid_flags, travel_radius, country_code, region, city, postal_code,
			cross_border_enabled, marketplace_scope, category,
			active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		mission.UserID, mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
		string(conditionsJSON), string(requiredJSON), string(niceToHaveJSON), mission.RiskTolerance,
		mission.ZipCode, mission.Distance, string(queriesJSON),
		mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius,
		strings.ToUpper(strings.TrimSpace(mission.CountryCode)), strings.TrimSpace(mission.Region), strings.TrimSpace(mission.City), strings.TrimSpace(mission.PostalCode),
		boolToInt(mission.CrossBorderEnabled), string(marketplaceScopeJSON), mission.Category,
		boolToInt(mission.Active),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *SQLiteStore) GetActiveMission(userID string) (*models.Mission, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, name, target_query, category_id, budget_max, budget_stretch,
			preferred_condition, required_features, nice_to_have, risk_tolerance,
			zip_code, distance, search_queries,
			status, urgency, avoid_flags, travel_radius, country_code, region, city, postal_code,
			cross_border_enabled, marketplace_scope, category,
			active, created_at, updated_at
		FROM shopping_profiles
		WHERE user_id = ? AND active = 1 AND status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1
	`, userID)
	mission, err := scanSQLiteMission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mission, nil
}

func (s *SQLiteStore) GetMission(id int64) (*models.Mission, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, name, target_query, category_id, budget_max, budget_stretch,
			preferred_condition, required_features, nice_to_have, risk_tolerance,
			zip_code, distance, search_queries,
			status, urgency, avoid_flags, travel_radius, country_code, region, city, postal_code,
			cross_border_enabled, marketplace_scope, category,
			active, created_at, updated_at
		FROM shopping_profiles
		WHERE id = ?
	`, id)
	mission, err := scanSQLiteMission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mission, nil
}

func (s *SQLiteStore) ListMissions(userID string) ([]models.Mission, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.user_id, m.name, m.target_query, m.category_id, m.budget_max, m.budget_stretch,
			m.preferred_condition, m.required_features, m.nice_to_have, m.risk_tolerance,
			m.zip_code, m.distance, m.search_queries,
			m.status, m.urgency, m.avoid_flags, m.travel_radius, m.country_code, m.region, m.city, m.postal_code,
			m.cross_border_enabled, m.marketplace_scope, m.category,
			m.active, m.created_at, m.updated_at,
			COUNT(l.item_id) AS match_count,
			COALESCE(MAX(l.last_seen), '') AS last_match_at
		FROM shopping_profiles m
		LEFT JOIN listings l
			ON l.profile_id = m.id AND l.item_id LIKE ?
		WHERE m.user_id = ?
		GROUP BY m.id
		ORDER BY m.updated_at DESC, m.id DESC
	`, scopedItemPrefix(userID), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.Mission, 0)
	for rows.Next() {
		mission, err := scanSQLiteMissionWithStats(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mission)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteMission(id int64, userID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM shopping_profiles WHERE id = ? AND user_id = ?`, id, userID)
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

	if _, err := tx.Exec(`DELETE FROM search_configs WHERE profile_id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM listings WHERE profile_id = ? AND item_id LIKE ?`, id, scopedItemPrefix(userID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shortlist_entries WHERE profile_id = ? AND user_id = ?`, id, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) UpdateMissionStatus(id int64, status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "active", "paused", "completed":
	default:
		return fmt.Errorf("unsupported mission status %q", status)
	}
	active := 1
	if status != "active" {
		active = 0
	}
	_, err := s.db.Exec(`
		UPDATE shopping_profiles
		SET status = ?, active = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, active, id)
	return err
}

func (s *SQLiteStore) GetMissionLastRecheck(_ context.Context, _ int64, _ string) (time.Time, error) {
	return time.Time{}, sql.ErrNoRows
}

func (s *SQLiteStore) SetMissionRecheck(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *SQLiteStore) ResetMissionSearchSpecsNextRun(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *SQLiteStore) SaveShortlistEntry(entry models.ShortlistEntry) error {
	concernsJSON, err := json.Marshal(entry.Concerns)
	if err != nil {
		return err
	}
	questionsJSON, err := json.Marshal(entry.SuggestedQuestions)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO shortlist_entries (
			user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
			ask_price, fair_price, verdict, concerns, suggested_questions, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			title = excluded.title,
			url = excluded.url,
			recommendation_label = excluded.recommendation_label,
			recommendation_score = excluded.recommendation_score,
			ask_price = excluded.ask_price,
			fair_price = excluded.fair_price,
			verdict = excluded.verdict,
			concerns = excluded.concerns,
			suggested_questions = excluded.suggested_questions,
			status = excluded.status,
			updated_at = CURRENT_TIMESTAMP
	`,
		entry.UserID, entry.MissionID, entry.ItemID, entry.Title, entry.URL,
		string(entry.RecommendationLabel), entry.RecommendationScore, entry.AskPrice, entry.FairPrice,
		entry.Verdict, string(concernsJSON), string(questionsJSON), entry.Status,
	)
	return err
}

func (s *SQLiteStore) GetShortlist(userID string) ([]models.ShortlistEntry, error) {
	rows, err := s.db.Query(`
		SELECT se.id, se.user_id, se.profile_id, se.item_id, se.title, se.url,
		       se.recommendation_label, se.recommendation_score,
		       se.ask_price, se.fair_price, se.verdict, se.concerns, se.suggested_questions,
		       se.status, se.created_at, se.updated_at,
		       COALESCE(l.condition, '') AS condition,
		       COALESCE(l.marketplace_id, '') AS marketplace_id,
		       COALESCE(l.outreach_status, 'none') AS outreach_status
		FROM shortlist_entries se
		LEFT JOIN listings l ON l.item_id = (se.user_id || '::' || se.item_id)
		WHERE se.user_id = ?
		ORDER BY se.updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.ShortlistEntry
	for rows.Next() {
		var entry models.ShortlistEntry
		var concernsJSON, questionsJSON, createdAt, updatedAt string
		if err := rows.Scan(
			&entry.ID, &entry.UserID, &entry.MissionID, &entry.ItemID, &entry.Title, &entry.URL,
			&entry.RecommendationLabel, &entry.RecommendationScore, &entry.AskPrice, &entry.FairPrice,
			&entry.Verdict, &concernsJSON, &questionsJSON, &entry.Status, &createdAt, &updatedAt,
			&entry.Condition, &entry.MarketplaceID, &entry.OutreachStatus,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(concernsJSON), &entry.Concerns)
		_ = json.Unmarshal([]byte(questionsJSON), &entry.SuggestedQuestions)
		entry.CreatedAt, _ = parseSQLiteTime(createdAt)
		entry.UpdatedAt, _ = parseSQLiteTime(updatedAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) GetShortlistEntry(userID, itemID string) (*models.ShortlistEntry, error) {
	row := s.db.QueryRow(`
		SELECT se.id, se.user_id, se.profile_id, se.item_id, se.title, se.url,
		       se.recommendation_label, se.recommendation_score,
		       se.ask_price, se.fair_price, se.verdict, se.concerns, se.suggested_questions,
		       se.status, se.created_at, se.updated_at,
		       COALESCE(l.condition, '') AS condition,
		       COALESCE(l.marketplace_id, '') AS marketplace_id,
		       COALESCE(l.outreach_status, 'none') AS outreach_status
		FROM shortlist_entries se
		LEFT JOIN listings l ON l.item_id = (se.user_id || '::' || se.item_id)
		WHERE se.user_id = ? AND se.item_id = ?
	`, userID, itemID)

	var entry models.ShortlistEntry
	var concernsJSON, questionsJSON, createdAt, updatedAt string
	err := row.Scan(
		&entry.ID, &entry.UserID, &entry.MissionID, &entry.ItemID, &entry.Title, &entry.URL,
		&entry.RecommendationLabel, &entry.RecommendationScore, &entry.AskPrice, &entry.FairPrice,
		&entry.Verdict, &concernsJSON, &questionsJSON, &entry.Status, &createdAt, &updatedAt,
		&entry.Condition, &entry.MarketplaceID, &entry.OutreachStatus,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(concernsJSON), &entry.Concerns)
	_ = json.Unmarshal([]byte(questionsJSON), &entry.SuggestedQuestions)
	entry.CreatedAt, _ = parseSQLiteTime(createdAt)
	entry.UpdatedAt, _ = parseSQLiteTime(updatedAt)
	return &entry, nil
}

func (s *SQLiteStore) SaveConversationArtifact(userID string, intent models.ConversationIntent, input, output string) error {
	_, err := s.db.Exec(`
		INSERT INTO conversation_artifacts (user_id, intent, input_text, output_text)
		VALUES (?, ?, ?, ?)
	`, userID, string(intent), input, output)
	return err
}

func (s *SQLiteStore) SaveAssistantSession(session models.AssistantSession) error {
	draftJSON := ""
	if session.DraftMission != nil {
		raw, err := json.Marshal(session.DraftMission)
		if err != nil {
			return err
		}
		draftJSON = string(raw)
	}

	_, err := s.db.Exec(`
		INSERT INTO assistant_sessions (user_id, pending_intent, pending_question, draft_profile, last_assistant_msg, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET
			pending_intent = excluded.pending_intent,
			pending_question = excluded.pending_question,
			draft_profile = excluded.draft_profile,
			last_assistant_msg = excluded.last_assistant_msg,
			updated_at = CURRENT_TIMESTAMP
	`, session.UserID, string(session.PendingIntent), session.PendingQuestion, draftJSON, session.LastAssistantMsg)
	return err
}

func (s *SQLiteStore) GetAssistantSession(userID string) (*models.AssistantSession, error) {
	row := s.db.QueryRow(`
		SELECT user_id, pending_intent, pending_question, draft_profile, last_assistant_msg, updated_at
		FROM assistant_sessions
		WHERE user_id = ?
	`, userID)

	var session models.AssistantSession
	var pendingIntent, draftJSON, updatedAt string
	err := row.Scan(&session.UserID, &pendingIntent, &session.PendingQuestion, &draftJSON, &session.LastAssistantMsg, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session.PendingIntent = models.ConversationIntent(pendingIntent)
	if strings.TrimSpace(draftJSON) != "" {
		var draft models.Mission
		if err := json.Unmarshal([]byte(draftJSON), &draft); err == nil {
			session.DraftMission = &draft
		}
	}
	session.UpdatedAt, _ = parseSQLiteTime(updatedAt)
	return &session, nil
}

func (s *SQLiteStore) ClearAssistantSession(userID string) error {
	_, err := s.db.Exec(`DELETE FROM assistant_sessions WHERE user_id = ?`, userID)
	return err
}

func (s *SQLiteStore) SaveActionDraft(draft models.ActionDraft) error {
	_, err := s.db.Exec(`
		INSERT INTO action_log (user_id, item_id, action_type, content, status)
		VALUES (?, ?, ?, ?, ?)
	`, draft.UserID, draft.ItemID, draft.ActionType, draft.Content, draft.Status)
	return err
}

func (s *SQLiteStore) CreateUser(email, hash, name string) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`
		INSERT INTO users (id, email, password_hash, name, tier, role)
		VALUES (?, ?, ?, ?, 'free', '')
	`, id, strings.ToLower(strings.TrimSpace(email)), hash, strings.TrimSpace(name))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *SQLiteStore) UpdateUserProfile(user models.User) error {
	_, err := s.db.Exec(`
		UPDATE users
		SET name = ?, country_code = ?, region = ?, city = ?, postal_code = ?, preferred_radius_km = ?, cross_border_enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, strings.TrimSpace(user.Name), strings.ToUpper(strings.TrimSpace(user.CountryCode)), strings.TrimSpace(user.Region),
		strings.TrimSpace(user.City), strings.TrimSpace(user.PostalCode), user.PreferredRadiusKm, boolToInt(user.CrossBorderEnabled), user.ID)
	return err
}

func (s *SQLiteStore) GetUserByEmail(email string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE email = ?
	`, strings.ToLower(strings.TrimSpace(email)))
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByID(id string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByAuthIdentity(provider, subject string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT u.id, u.email, u.password_hash, u.name, u.tier, u.role, u.is_admin, u.stripe_customer_id, u.country_code, u.region, u.city,
		       u.postal_code, u.preferred_radius_km, u.cross_border_enabled, u.created_at, u.updated_at
		FROM users u
		JOIN user_auth_identities i ON i.user_id = u.id
		WHERE i.provider = ? AND i.provider_subject = ?
	`, strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(subject))
	return scanUser(row)
}

func (s *SQLiteStore) UpsertUserAuthIdentity(identity models.AuthIdentity) error {
	_, err := s.db.Exec(`
		INSERT INTO user_auth_identities (user_id, provider, provider_subject, email, email_verified)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(provider, provider_subject) DO UPDATE SET
			user_id = excluded.user_id,
			email = excluded.email,
			email_verified = excluded.email_verified,
			updated_at = CURRENT_TIMESTAMP
	`, identity.UserID, strings.ToLower(strings.TrimSpace(identity.Provider)), strings.TrimSpace(identity.ProviderSubject),
		strings.ToLower(strings.TrimSpace(identity.Email)), boolToInt(identity.EmailVerified))
	return err
}

func (s *SQLiteStore) ListUserAuthMethods(userID string) ([]string, error) {
	user, err := s.GetUserByID(userID)
	if err != nil || user == nil {
		return nil, err
	}
	methods := []string{}
	if strings.HasPrefix(strings.TrimSpace(user.PasswordHash), "$2") {
		methods = append(methods, "email_password")
	}
	rows, err := s.db.Query(`SELECT provider FROM user_auth_identities WHERE user_id = ? ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for _, method := range methods {
		seen[method] = true
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

func (s *SQLiteStore) UpdateUserTier(userID, tier string) error {
	_, err := s.db.Exec(`UPDATE users SET tier = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, tier, userID)
	return err
}

func (s *SQLiteStore) UpdateUserRole(userID, role string) error {
	_, err := s.db.Exec(`UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, strings.TrimSpace(role), userID)
	return err
}

func (s *SQLiteStore) UpdateStripeCustomer(userID, customerID string) error {
	_, err := s.db.Exec(`UPDATE users SET stripe_customer_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, customerID, userID)
	return err
}

func (s *SQLiteStore) UpdateUserTierByStripeCustomer(customerID, tier string) error {
	_, err := s.db.Exec(`UPDATE users SET tier = ?, updated_at = CURRENT_TIMESTAMP WHERE stripe_customer_id = ?`, tier, customerID)
	return err
}

func (s *SQLiteStore) RecordStripeEvent(eventID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO stripe_events (event_id) VALUES (?)`, eventID)
	return err
}

func (s *SQLiteStore) RecordStripeProcessedEvent(eventID string) (bool, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false, nil
	}
	result, err := s.db.Exec(`INSERT OR IGNORE INTO stripe_processed_events (event_id) VALUES (?)`, eventID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *SQLiteStore) SetUserAdmin(userID string, isAdmin bool) error {
	v := 0
	if isAdmin {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE users SET is_admin = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, v, userID)
	return err
}

func (s *SQLiteStore) RecordAIUsage(entry models.AIUsageEntry) error {
	success := 1
	if !entry.Success {
		success = 0
	}
	_, err := s.db.Exec(`
		INSERT INTO ai_usage_log (user_id, mission_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.UserID, entry.MissionID, entry.CallType, entry.Model, entry.PromptTokens, entry.CompletionTokens, entry.TotalTokens,
		entry.LatencyMs, success, entry.ErrorMsg)
	return err
}

func (s *SQLiteStore) ListAllUsers() ([]models.AdminUserSummary, error) {
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
		var createdAt, updatedAt string
		if err := rows.Scan(&s.ID, &s.Email, &s.PasswordHash, &s.Name, &s.Tier, &s.Role, &s.IsAdmin, &s.StripeCustomer,
			&createdAt, &updatedAt, &s.MissionCount, &s.SearchCount, &s.AICallCount, &s.AITokens); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = parseSQLiteTime(createdAt)
		s.UpdatedAt, _ = parseSQLiteTime(updatedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetAIUsageStats(days int) (models.AIUsageStats, error) {
	var stats models.AIUsageStats
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0)
		FROM ai_usage_log
		WHERE created_at >= datetime('now', '-' || ? || ' days')
	`, days).Scan(&stats.TotalCalls, &stats.TotalTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.FailedCalls)
	return stats, err
}

func (s *SQLiteStore) GetAIUsageTimeline(days int) ([]models.AIUsageEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, mission_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg, created_at
		FROM ai_usage_log
		WHERE created_at >= datetime('now', '-' || ? || ' days')
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
		var successInt int
		var createdAt string
		if err := rows.Scan(&e.ID, &e.UserID, &e.MissionID, &e.CallType, &e.Model, &e.PromptTokens, &e.CompletionTokens,
			&e.TotalTokens, &e.LatencyMs, &successInt, &e.ErrorMsg, &createdAt); err != nil {
			return nil, err
		}
		e.Success = successInt != 0
		e.CreatedAt, _ = parseSQLiteTime(createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetUserAIUsageStats(userID string, days int) (models.AIUsageStats, error) {
	var stats models.AIUsageStats
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0)
		FROM ai_usage_log
		WHERE user_id = ? AND created_at >= datetime('now', '-' || ? || ' days')
	`, userID, days).Scan(&stats.TotalCalls, &stats.TotalTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.FailedCalls)
	return stats, err
}

func (s *SQLiteStore) GetSearchConfigs(userID string) ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json, offer_percentage, auto_message, message_template, attributes_json,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE user_id = ?
		ORDER BY updated_at DESC, id DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []models.SearchSpec
	for rows.Next() {
		spec, err := scanSearchSpec(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (s *SQLiteStore) GetSearchConfigByID(id int64) (*models.SearchSpec, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json, offer_percentage, auto_message, message_template, attributes_json,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE id = ?
	`, id)
	spec, err := scanSearchSpec(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *SQLiteStore) CountSearchConfigs(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM search_configs WHERE user_id = ? AND enabled = 1`, userID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) CountActiveMissions(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM shopping_profiles WHERE user_id = ? AND status = 'active'`, userID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetAllEnabledSearchConfigs() ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
		       condition_json, offer_percentage, auto_message, message_template, attributes_json,
		       enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
		       last_result_count, consecutive_empty_runs, consecutive_failures
		FROM search_configs
		WHERE enabled = 1
		ORDER BY user_id, updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var specs []models.SearchSpec
	for rows.Next() {
		spec, err := scanSearchSpec(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (s *SQLiteStore) ListRecentListings(userID string, limit int, missionID int64) ([]models.Listing, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT item_id, profile_id, title, price, price_type, image_urls,
		       url, condition, marketplace_id,
		       score, fair_price, offer_price, confidence, reasoning, risk_flags,
		       COALESCE(recommended_action, 'ask_seller'),
		       comparables_count, comparables_median_age_days,
		       last_seen, COALESCE(feedback, ''),
		       COALESCE(currency_status, ''),
		       COALESCE(outreach_status, 'none')
		FROM listings
		WHERE item_id LIKE ?
		  AND (? = 0 OR profile_id = ?)
		  AND COALESCE(feedback, '') <> 'dismissed'
		ORDER BY last_seen DESC
		LIMIT ?
	`, scopedItemPrefix(userID), missionID, missionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON, lastSeen string
		if err := rows.Scan(
			&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
			&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
			&lastSeen, &listing.Feedback,
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
		listing.Date, _ = parseSQLiteTime(lastSeen)
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
func (s *SQLiteStore) ListRecentListingsPaginated(userID string, limit, offset int, missionID int64, filter models.MatchesFilter) ([]models.Listing, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	prefix := scopedItemPrefix(userID)

	// Build the shared WHERE clause extras and args for market / condition /
	// min_score filters. We append to a base args slice so the positional
	// parameters stay consistent between the COUNT query and the page query.
	//
	// Base positional params: (prefix, missionID, missionID)  — indices 1,2,3
	// (SQLite uses ?, not $N, so we track by append position.)
	whereExtras := ""
	baseArgs := []any{prefix, missionID, missionID}

	if filter.Market != "" {
		whereExtras += "\n  AND l.marketplace_id = ?"
		baseArgs = append(baseArgs, filter.Market)
	}
	if filter.Condition != "" {
		whereExtras += "\n  AND l.condition = ?"
		baseArgs = append(baseArgs, filter.Condition)
	}
	if filter.MinScore > 0 {
		whereExtras += "\n  AND l.score >= ?"
		baseArgs = append(baseArgs, float64(filter.MinScore))
	}

	// Count query — same WHERE, no LIMIT/OFFSET.
	// Uses the same "l." column alias as the page query so whereExtras is shared.
	countQuery := `
		SELECT COUNT(*)
		FROM listings l
		WHERE l.item_id LIKE ?
		  AND (? = 0 OR l.profile_id = ?)
		  AND COALESCE(l.feedback, '') <> 'dismissed'` + whereExtras

	var total int
	if err := s.db.QueryRow(countQuery, baseArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// ORDER BY per sort mode — every mode includes the unique item_id tie-breaker.
	orderBy := sqliteOrderBy(filter.Sort)

	// Page query. The LEFT JOIN on outreach_threads derives user_id from the
	// scoped item_id (format "userID::itemID") using SQLite string functions so
	// that no new bind parameter is required.
	pageArgs := append(baseArgs, limit, offset)
	pageQuery := `
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
		    ON ot.user_id = SUBSTR(l.item_id, 1, INSTR(l.item_id, '::') - 1)
		   AND ot.listing_id = SUBSTR(l.item_id, INSTR(l.item_id, '::') + 2)
		   AND ot.marketplace_id = l.marketplace_id
		WHERE l.item_id LIKE ?
		  AND (? = 0 OR l.profile_id = ?)
		  AND COALESCE(l.feedback, '') <> 'dismissed'` + whereExtras + `
		` + orderBy + `
		LIMIT ? OFFSET ?`

	rows, err := s.db.Query(pageQuery, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON, lastSeen string
		var outreachSentAt sql.NullString
		if err := rows.Scan(
			&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
			&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
			&lastSeen, &listing.Feedback,
			&listing.CurrencyStatus,
			&listing.OutreachStatus,
			&outreachSentAt,
		); err != nil {
			return nil, 0, err
		}
		if outreachSentAt.Valid && outreachSentAt.String != "" {
			if t, err := parseSQLiteTime(outreachSentAt.String); err == nil {
				listing.OutreachSentAt = &t
			}
		}
		listing.ItemID = unscopedItemID(listing.ItemID)
		if strings.TrimSpace(listing.MarketplaceID) == "" {
			listing.MarketplaceID = "marktplaats"
		}
		listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
		listing.Date, _ = parseSQLiteTime(lastSeen)
		_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
		_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
		listings = append(listings, listing)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return listings, total, nil
}

// sqliteOrderBy returns the ORDER BY clause (without the keyword) for the
// given sort mode. All modes include item_id ASC as a unique tie-breaker.
// offer_price NULLs are sorted last in both price modes.
//
// SQLite does not support NULLS LAST by default; we use a CASE workaround.
func sqliteOrderBy(sort string) string {
	switch sort {
	case "score":
		return "ORDER BY score DESC, item_id ASC"
	case "price_asc":
		return "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price ASC, item_id ASC"
	case "price_desc":
		return "ORDER BY CASE WHEN offer_price IS NULL OR offer_price = 0 THEN 1 ELSE 0 END ASC, offer_price DESC, item_id ASC"
	default: // "newest" and zero-value — preserves Phase 1 default
		return "ORDER BY last_seen DESC, item_id ASC"
	}
}

func (s *SQLiteStore) GetListing(userID, itemID string) (*models.Listing, error) {
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
		    ON ot.user_id = SUBSTR(l.item_id, 1, INSTR(l.item_id, '::') - 1)
		   AND ot.listing_id = SUBSTR(l.item_id, INSTR(l.item_id, '::') + 2)
		   AND ot.marketplace_id = l.marketplace_id
		WHERE l.item_id = ?
	`, scopedItemID(userID, itemID))

	var listing models.Listing
	var imageURLsJSON, riskFlagsJSON, lastSeen string
	var outreachSentAt sql.NullString
	if err := row.Scan(
		&listing.ItemID, &listing.ProfileID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
		&listing.URL, &listing.Condition, &listing.MarketplaceID,
		&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
		&listing.Reason, &riskFlagsJSON, &listing.RecommendedAction,
		&listing.ComparablesCount, &listing.ComparablesMedianAgeDays,
		&lastSeen, &listing.Feedback,
		&listing.CurrencyStatus,
		&listing.OutreachStatus,
		&outreachSentAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if outreachSentAt.Valid && outreachSentAt.String != "" {
		if t, err := parseSQLiteTime(outreachSentAt.String); err == nil {
			listing.OutreachSentAt = &t
		}
	}
	listing.ItemID = unscopedItemID(listing.ItemID)
	if strings.TrimSpace(listing.MarketplaceID) == "" {
		listing.MarketplaceID = "marktplaats"
	}
	listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
	listing.Date, _ = parseSQLiteTime(lastSeen)
	_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
	_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
	return &listing, nil
}

// SetListingFeedback marks a listing as approved/dismissed or clears feedback.
// feedback must be one of "", "approved", "dismissed".
func (s *SQLiteStore) SetListingFeedback(userID, itemID, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	switch feedback {
	case "", "approved", "dismissed":
	default:
		return fmt.Errorf("invalid feedback value %q", feedback)
	}
	_, err := s.db.Exec(`
		UPDATE listings
		SET feedback = ?,
		    feedback_at = CASE WHEN ? = '' THEN NULL ELSE CURRENT_TIMESTAMP END
		WHERE item_id = ?
	`, feedback, feedback, scopedItemID(userID, itemID))
	return err
}

// UpdateOutreachStatus sets the outreach lifecycle status for a listing owned
// by the given user. Status must be one of: none, sent, replied, won, lost.
// Returns ErrListingNotFound when the listing does not exist or is not owned by userID.
func (s *SQLiteStore) UpdateOutreachStatus(ctx context.Context, userID, itemID, status string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE listings
		SET outreach_status = ?
		WHERE item_id = ?
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
// this mission, to be used as high-confidence comparables when scoring new
// listings.
func (s *SQLiteStore) GetApprovedComparables(userID string, missionID int64, limit int) ([]models.ComparableDeal, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT item_id, title, price, score, first_seen
		FROM listings
		WHERE item_id LIKE ?
		  AND feedback = 'approved'
		  AND (? = 0 OR profile_id = ?)
		  AND price > 0
		ORDER BY feedback_at DESC
		LIMIT ?
	`, scopedItemPrefix(userID), missionID, missionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deals []models.ComparableDeal
	for rows.Next() {
		var deal models.ComparableDeal
		var firstSeen string
		if err := rows.Scan(&deal.ItemID, &deal.Title, &deal.Price, &deal.Score, &firstSeen); err != nil {
			return nil, err
		}
		deal.ItemID = unscopedItemID(deal.ItemID)
		if t, err := parseSQLiteTime(firstSeen); err == nil {
			deal.LastSeen = t
		}
		deal.Similarity = 1.0
		deal.MatchReason = "user-approved match"
		deals = append(deals, deal)
	}
	return deals, rows.Err()
}

func (s *SQLiteStore) ListActionDrafts(userID string) ([]models.ActionDraft, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, item_id, action_type, content, status, created_at
		FROM action_log
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var drafts []models.ActionDraft
	for rows.Next() {
		var draft models.ActionDraft
		var createdAt string
		if err := rows.Scan(&draft.ID, &draft.UserID, &draft.ItemID, &draft.ActionType, &draft.Content, &draft.Status, &createdAt); err != nil {
			return nil, err
		}
		draft.CreatedAt, _ = parseSQLiteTime(createdAt)
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

func (s *SQLiteStore) CreateSearchConfig(spec models.SearchSpec) (int64, error) {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	result, err := s.db.Exec(`
		INSERT INTO search_configs (
			user_id, profile_id, name, query, marketplace_id, country_code, city, postal_code, radius_km, category_id, max_price, min_price,
			condition_json, offer_percentage, auto_message, message_template, attributes_json,
			enabled, check_interval_seconds, priority_class, next_run_at, last_run_at, last_signal_at, last_error_at,
			last_result_count, consecutive_empty_runs, consecutive_failures
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, spec.UserID, spec.ProfileID, spec.Name, spec.Query, marketplace.NormalizeMarketplaceID(spec.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(spec.CountryCode)), strings.TrimSpace(spec.City), strings.TrimSpace(spec.PostalCode), spec.RadiusKm,
		spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, boolToInt(spec.AutoMessage), spec.MessageTemplate, string(attributesJSON),
		boolToInt(spec.Enabled), int64(spec.CheckInterval/time.Second), spec.PriorityClass,
		sqliteTimeOrNil(spec.NextRunAt), sqliteTimeOrNil(spec.LastRunAt), sqliteTimeOrNil(spec.LastSignalAt), sqliteTimeOrNil(spec.LastErrorAt),
		spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *SQLiteStore) UpdateSearchConfig(spec models.SearchSpec) error {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET profile_id = ?, name = ?, query = ?, marketplace_id = ?, country_code = ?, city = ?, postal_code = ?, radius_km = ?,
		    category_id = ?, max_price = ?, min_price = ?,
		    condition_json = ?, offer_percentage = ?, auto_message = ?, message_template = ?,
		    attributes_json = ?, enabled = ?, check_interval_seconds = ?, priority_class = ?, next_run_at = ?, last_run_at = ?,
		    last_signal_at = ?, last_error_at = ?, last_result_count = ?, consecutive_empty_runs = ?, consecutive_failures = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ?
	`, spec.ProfileID, spec.Name, spec.Query, marketplace.NormalizeMarketplaceID(spec.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(spec.CountryCode)), strings.TrimSpace(spec.City), strings.TrimSpace(spec.PostalCode), spec.RadiusKm,
		spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, boolToInt(spec.AutoMessage), spec.MessageTemplate,
		string(attributesJSON), boolToInt(spec.Enabled), int64(spec.CheckInterval/time.Second), spec.PriorityClass,
		sqliteTimeOrNil(spec.NextRunAt), sqliteTimeOrNil(spec.LastRunAt), sqliteTimeOrNil(spec.LastSignalAt), sqliteTimeOrNil(spec.LastErrorAt),
		spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures, spec.ID, spec.UserID)
	return err
}

func (s *SQLiteStore) UpdateSearchRuntime(spec models.SearchSpec) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET priority_class = ?, next_run_at = ?, last_run_at = ?, last_signal_at = ?, last_error_at = ?,
		    last_result_count = ?, consecutive_empty_runs = ?, consecutive_failures = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, spec.PriorityClass, sqliteTimeOrNil(spec.NextRunAt), sqliteTimeOrNil(spec.LastRunAt), sqliteTimeOrNil(spec.LastSignalAt),
		sqliteTimeOrNil(spec.LastErrorAt), spec.LastResultCount, spec.ConsecutiveEmptyRuns, spec.ConsecutiveFailures, spec.ID)
	return err
}

func (s *SQLiteStore) SetSearchEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, boolToInt(enabled), id)
	return err
}

func (s *SQLiteStore) SetSearchNextRunAt(id int64, nextRunAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET next_run_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, sqliteTimeOrNil(nextRunAt), id)
	return err
}

func (s *SQLiteStore) DeleteSearchConfig(id int64, userID string) error {
	_, err := s.db.Exec(`DELETE FROM search_configs WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// IsNew returns true if we haven't seen this listing before.
func (s *SQLiteStore) IsNew(userID, itemID string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM listings WHERE item_id = ?", scopedItemID(userID, itemID)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// GetListingScore returns the previously stored score for a listing.
func (s *SQLiteStore) GetListingScore(userID, itemID string) (float64, bool, error) {
	var score float64
	err := s.db.QueryRow("SELECT score FROM listings WHERE item_id = ?", scopedItemID(userID, itemID)).Scan(&score)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return score, true, nil
}

func (s *SQLiteStore) GetListingScoringState(userID, itemID string) (price int, reasoningSource string, comparablesCount int, found bool, err error) {
	err = s.db.QueryRow(`
		SELECT price, reasoning_source, comparables_count
		FROM listings
		WHERE item_id = ?
	`, scopedItemID(userID, itemID)).Scan(&price, &reasoningSource, &comparablesCount)
	if err == sql.ErrNoRows {
		return 0, "", 0, false, nil
	}
	if err != nil {
		return 0, "", 0, false, err
	}
	return price, reasoningSource, comparablesCount, true, nil
}

func (s *SQLiteStore) GetAIScoreCache(cacheKey string, promptVersion int) (score float64, reasoning string, found bool, err error) {
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
		WHERE key = ? AND prompt_version = ?
	`, cacheKey, promptVersion).Scan(&score, &reasoning)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return score, reasoning, true, nil
}

// SaveListing stores or updates a listing and its scored analysis.
func (s *SQLiteStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, profile_id, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, reasoning_source, risk_flags,
			recommended_action,
			comparables_count, comparables_median_age_days,
			currency_status,
			first_seen, last_seen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(item_id) DO UPDATE SET
			price                       = excluded.price,
			score                       = excluded.score,
			profile_id                  = excluded.profile_id,
			image_urls                  = excluded.image_urls,
			url                         = excluded.url,
			condition                   = excluded.condition,
			marketplace_id              = excluded.marketplace_id,
			fair_price                  = excluded.fair_price,
			offer_price                 = excluded.offer_price,
			confidence                  = excluded.confidence,
			reasoning                   = excluded.reasoning,
			reasoning_source            = excluded.reasoning_source,
			risk_flags                  = excluded.risk_flags,
			recommended_action          = excluded.recommended_action,
			comparables_count           = excluded.comparables_count,
			comparables_median_age_days = excluded.comparables_median_age_days,
			currency_status             = excluded.currency_status,
			last_seen                   = CURRENT_TIMESTAMP
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, l.ProfileID, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, scored.ReasoningSource, string(riskFlagsJSON),
		scored.RecommendedAction,
		scored.ComparablesCount, scored.ComparablesMedianAgeDays,
		l.CurrencyStatus,
	)
	return err
}

func (s *SQLiteStore) TouchListing(userID, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET last_seen = CURRENT_TIMESTAMP WHERE item_id = ?`, scopedItemID(userID, itemID))
	return err
}

func (s *SQLiteStore) SetAIScoreCache(cacheKey string, score float64, reasoning string, promptVersion int) error {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return nil
	}
	if promptVersion <= 0 {
		promptVersion = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO ai_score_cache (key, score, reasoning, created_at, prompt_version)
		VALUES (?, ?, ?, CAST(strftime('%s','now') AS INTEGER), ?)
		ON CONFLICT(key) DO UPDATE SET
			score = excluded.score,
			reasoning = excluded.reasoning,
			created_at = excluded.created_at,
			prompt_version = excluded.prompt_version
	`, cacheKey, score, strings.TrimSpace(reasoning), promptVersion)
	return err
}

func (s *SQLiteStore) RecordSearchRun(entry models.SearchRunLog) error {
	_, err := s.db.Exec(`
		INSERT INTO search_run_log (
			search_config_id, user_id, mission_id, plan, marketplace_id, country_code,
			started_at, finished_at, queue_wait_ms, priority, status, results_found, new_listings, deal_hits,
			throttled, error_code, searches_avoided
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.SearchConfigID, entry.UserID, entry.MissionID, entry.Plan, marketplace.NormalizeMarketplaceID(entry.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(entry.CountryCode)), sqliteTimeOrZero(entry.StartedAt), sqliteTimeOrZero(entry.FinishedAt),
		entry.QueueWaitMs, entry.Priority, entry.Status, entry.ResultsFound, entry.NewListings, entry.DealHits,
		boolToInt(entry.Throttled), entry.ErrorCode, entry.SearchesAvoided)
	return err
}

func (s *SQLiteStore) RecordAdminAuditLog(entry models.AdminAuditLogEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO admin_audit_log (
			actor_user_id, actor_role, request_id, action, target_type, target_id, before_json, after_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, strings.TrimSpace(entry.ActorUserID), strings.TrimSpace(entry.ActorRole), strings.TrimSpace(entry.RequestID),
		strings.TrimSpace(entry.Action), strings.TrimSpace(entry.TargetType), strings.TrimSpace(entry.TargetID),
		strings.TrimSpace(entry.BeforeJSON), strings.TrimSpace(entry.AfterJSON))
	return err
}

func (s *SQLiteStore) GetSearchOpsStats(days int) (models.SearchOpsStats, error) {
	stats := models.SearchOpsStats{
		ByStatus:      map[string]int{},
		ByPlan:        map[string]int{},
		ByCountry:     map[string]int{},
		ByMarketplace: map[string]int{},
	}
	var queueAvg sql.NullFloat64
	var freshness sql.NullFloat64
	if err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN IFNULL(error_code, '') <> '' AND error_code <> 'out_of_scope' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(results_found), 0), COALESCE(SUM(new_listings), 0), COALESCE(SUM(deal_hits), 0),
		       COALESCE(SUM(CASE WHEN throttled = 1 THEN 1 ELSE 0 END), 0), COALESCE(SUM(searches_avoided), 0),
		       AVG(queue_wait_ms),
		       AVG(CASE WHEN sc.last_signal_at IS NULL THEN NULL ELSE (julianday('now') - julianday(sc.last_signal_at)) * 24 * 60 END)
		FROM search_run_log
		LEFT JOIN search_configs sc ON sc.id = search_run_log.search_config_id
		WHERE started_at >= datetime('now', '-' || ? || ' days')
	`, days).Scan(&stats.TotalRuns, &stats.SuccessfulRuns, &stats.FailedRuns,
		&stats.TotalResultsFound, &stats.TotalNewListings, &stats.TotalDealHits,
		&stats.TotalThrottled, &stats.SearchesAvoidedByScoping, &queueAvg, &freshness); err != nil {
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
	if err := s.fillSQLiteSearchOpsBreakdown(days, "status", &stats.ByStatus); err != nil {
		return stats, err
	}
	if err := s.fillSQLiteSearchOpsBreakdown(days, "plan", &stats.ByPlan); err != nil {
		return stats, err
	}
	if err := s.fillSQLiteSearchOpsBreakdown(days, "country_code", &stats.ByCountry); err != nil {
		return stats, err
	}
	if err := s.fillSQLiteSearchOpsBreakdown(days, "marketplace_id", &stats.ByMarketplace); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *SQLiteStore) ListSearchRuns(filter models.AdminSearchRunFilter) ([]models.AdminSearchRun, error) {
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
		WHERE l.started_at >= datetime('now', '-' || ? || ' days')
		  AND (? = '' OR l.status = ?)
		  AND (? = '' OR l.marketplace_id = ?)
		  AND (? = '' OR l.country_code = ?)
		  AND (? = '' OR l.user_id = ?)
		ORDER BY l.started_at DESC
		LIMIT ?
	`, days,
		strings.TrimSpace(filter.Status), strings.TrimSpace(filter.Status),
		marketplace.NormalizeMarketplaceID(filter.MarketplaceID), marketplace.NormalizeMarketplaceID(filter.MarketplaceID),
		strings.ToUpper(strings.TrimSpace(filter.CountryCode)), strings.ToUpper(strings.TrimSpace(filter.CountryCode)),
		strings.TrimSpace(filter.UserID), strings.TrimSpace(filter.UserID),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.AdminSearchRun, 0, limit)
	for rows.Next() {
		var run models.AdminSearchRun
		var startedAt, finishedAt string
		var throttledInt int
		if err := rows.Scan(
			&run.ID, &run.SearchConfigID, &run.SearchName, &run.UserID, &run.UserEmail,
			&run.MissionID, &run.MissionName, &run.Plan, &run.MarketplaceID, &run.CountryCode,
			&startedAt, &finishedAt, &run.QueueWaitMs, &run.Priority, &run.Status, &run.ResultsFound,
			&run.NewListings, &run.DealHits, &throttledInt, &run.ErrorCode, &run.SearchesAvoided,
		); err != nil {
			return nil, err
		}
		run.Throttled = throttledInt != 0
		run.MarketplaceID = marketplace.NormalizeMarketplaceID(run.MarketplaceID)
		run.CountryCode = strings.ToUpper(strings.TrimSpace(run.CountryCode))
		run.StartedAt, _ = parseSQLiteTime(startedAt)
		run.FinishedAt, _ = parseSQLiteTime(finishedAt)
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListAdminAuditLog(limit int) ([]models.AdminAuditLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT id, actor_user_id, actor_role, request_id, action, target_type, target_id, before_json, after_json, created_at
		FROM admin_audit_log
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.AdminAuditLogEntry, 0, limit)
	for rows.Next() {
		var entry models.AdminAuditLogEntry
		var createdAt string
		if err := rows.Scan(&entry.ID, &entry.ActorUserID, &entry.ActorRole, &entry.RequestID, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.BeforeJSON, &entry.AfterJSON, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt, _ = parseSQLiteTime(createdAt)
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertStripeWebhookEvent(entry models.StripeWebhookEventLog) error {
	receivedAt := sqliteTimeOrZero(entry.ReceivedAt)
	processedAt := sqliteTimeOrNil(entry.ProcessedAt)
	attemptCount := entry.AttemptCount
	if attemptCount <= 0 {
		attemptCount = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO stripe_webhook_events (
			event_id, event_type, object_id, api_account, request_id, status, error_message,
			attempt_count, payload_json, received_at, processed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO UPDATE SET
			event_type = excluded.event_type,
			object_id = excluded.object_id,
			api_account = excluded.api_account,
			request_id = excluded.request_id,
			status = excluded.status,
			error_message = excluded.error_message,
			attempt_count = CASE
				WHEN excluded.attempt_count > stripe_webhook_events.attempt_count THEN excluded.attempt_count
				ELSE stripe_webhook_events.attempt_count
			END,
			payload_json = excluded.payload_json,
			received_at = excluded.received_at,
			processed_at = excluded.processed_at
	`,
		strings.TrimSpace(entry.EventID), strings.TrimSpace(entry.EventType), strings.TrimSpace(entry.ObjectID),
		strings.TrimSpace(entry.APIAccount), strings.TrimSpace(entry.RequestID), strings.TrimSpace(entry.Status),
		strings.TrimSpace(entry.ErrorMessage), attemptCount, strings.TrimSpace(entry.PayloadJSON), receivedAt, processedAt,
	)
	return err
}

func (s *SQLiteStore) UpsertStripeSubscriptionSnapshot(snapshot models.StripeSubscriptionSnapshot) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_subscription_snapshots (
			subscription_id, customer_id, user_id, status, plan_price_id, plan_interval, currency,
			unit_amount, quantity, current_period_start, current_period_end, cancel_at_period_end,
			canceled_at, paused, latest_invoice_id, default_payment_method, raw_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(subscription_id) DO UPDATE SET
			customer_id = excluded.customer_id,
			user_id = excluded.user_id,
			status = excluded.status,
			plan_price_id = excluded.plan_price_id,
			plan_interval = excluded.plan_interval,
			currency = excluded.currency,
			unit_amount = excluded.unit_amount,
			quantity = excluded.quantity,
			current_period_start = excluded.current_period_start,
			current_period_end = excluded.current_period_end,
			cancel_at_period_end = excluded.cancel_at_period_end,
			canceled_at = excluded.canceled_at,
			paused = excluded.paused,
			latest_invoice_id = excluded.latest_invoice_id,
			default_payment_method = excluded.default_payment_method,
			raw_json = excluded.raw_json,
			updated_at = CURRENT_TIMESTAMP
	`,
		strings.TrimSpace(snapshot.SubscriptionID), strings.TrimSpace(snapshot.CustomerID), strings.TrimSpace(snapshot.UserID),
		strings.TrimSpace(snapshot.Status), strings.TrimSpace(snapshot.PlanPriceID), strings.TrimSpace(snapshot.PlanInterval),
		strings.ToUpper(strings.TrimSpace(snapshot.Currency)), snapshot.UnitAmount, snapshot.Quantity,
		sqliteTimeOrNil(snapshot.CurrentPeriodStart), sqliteTimeOrNil(snapshot.CurrentPeriodEnd),
		boolToInt(snapshot.CancelAtPeriodEnd), sqliteTimeOrNil(snapshot.CanceledAt), boolToInt(snapshot.Paused),
		strings.TrimSpace(snapshot.LatestInvoiceID), strings.TrimSpace(snapshot.DefaultPaymentMethod), strings.TrimSpace(snapshot.RawJSON),
	)
	return err
}

func (s *SQLiteStore) AppendStripeSubscriptionHistory(entry models.StripeSubscriptionHistoryEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_subscription_history (
			subscription_id, event_id, event_type, status, plan_price_id, currency, unit_amount, quantity,
			period_start, period_end, cancel_at_period_end, raw_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		strings.TrimSpace(entry.SubscriptionID), strings.TrimSpace(entry.EventID), strings.TrimSpace(entry.EventType),
		strings.TrimSpace(entry.Status), strings.TrimSpace(entry.PlanPriceID), strings.ToUpper(strings.TrimSpace(entry.Currency)),
		entry.UnitAmount, entry.Quantity, sqliteTimeOrNil(entry.PeriodStart), sqliteTimeOrNil(entry.PeriodEnd),
		boolToInt(entry.CancelAtEnd), strings.TrimSpace(entry.RawJSON),
	)
	return err
}

func (s *SQLiteStore) UpsertStripeInvoiceSummary(invoice models.StripeInvoiceSummary) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_invoice_summaries (
			invoice_id, subscription_id, customer_id, user_id, status, currency, amount_due, amount_paid,
			amount_remaining, attempt_count, paid, hosted_invoice_url, invoice_pdf, period_start, period_end,
			due_date, finalized_at, raw_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(invoice_id) DO UPDATE SET
			subscription_id = excluded.subscription_id,
			customer_id = excluded.customer_id,
			user_id = excluded.user_id,
			status = excluded.status,
			currency = excluded.currency,
			amount_due = excluded.amount_due,
			amount_paid = excluded.amount_paid,
			amount_remaining = excluded.amount_remaining,
			attempt_count = excluded.attempt_count,
			paid = excluded.paid,
			hosted_invoice_url = excluded.hosted_invoice_url,
			invoice_pdf = excluded.invoice_pdf,
			period_start = excluded.period_start,
			period_end = excluded.period_end,
			due_date = excluded.due_date,
			finalized_at = excluded.finalized_at,
			raw_json = excluded.raw_json,
			updated_at = CURRENT_TIMESTAMP
	`,
		strings.TrimSpace(invoice.InvoiceID), strings.TrimSpace(invoice.SubscriptionID), strings.TrimSpace(invoice.CustomerID),
		strings.TrimSpace(invoice.UserID), strings.TrimSpace(invoice.Status), strings.ToUpper(strings.TrimSpace(invoice.Currency)),
		invoice.AmountDue, invoice.AmountPaid, invoice.AmountRemaining, invoice.AttemptCount, boolToInt(invoice.Paid),
		strings.TrimSpace(invoice.HostedInvoiceURL), strings.TrimSpace(invoice.InvoicePDF),
		sqliteTimeOrNil(invoice.PeriodStart), sqliteTimeOrNil(invoice.PeriodEnd), sqliteTimeOrNil(invoice.DueDate),
		sqliteTimeOrNil(invoice.FinalizedAt), strings.TrimSpace(invoice.RawJSON),
	)
	return err
}

func (s *SQLiteStore) RecordStripeMutation(entry models.StripeMutationLog) error {
	_, err := s.db.Exec(`
		INSERT INTO stripe_mutation_log (
			idempotency_key, actor_user_id, actor_role, action, target_id, request_json, response_json, status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(idempotency_key) DO UPDATE SET
			actor_user_id = excluded.actor_user_id,
			actor_role = excluded.actor_role,
			action = excluded.action,
			target_id = excluded.target_id,
			request_json = excluded.request_json,
			response_json = excluded.response_json,
			status = excluded.status,
			updated_at = CURRENT_TIMESTAMP
	`,
		strings.TrimSpace(entry.IdempotencyKey), strings.TrimSpace(entry.ActorUserID), strings.TrimSpace(entry.ActorRole),
		strings.TrimSpace(entry.Action), strings.TrimSpace(entry.TargetID), strings.TrimSpace(entry.RequestJSON),
		strings.TrimSpace(entry.ResponseJSON), strings.TrimSpace(entry.Status),
	)
	return err
}

func (s *SQLiteStore) StartBillingReconcileRun(run models.BillingReconcileRun) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO billing_reconcile_runs (triggered_by, status, summary_json, error_json, started_at)
		VALUES (?, ?, ?, ?, ?)
	`,
		strings.TrimSpace(run.TriggeredBy), strings.TrimSpace(run.Status),
		strings.TrimSpace(run.SummaryJSON), strings.TrimSpace(run.ErrorJSON), sqliteTimeOrZero(run.StartedAt),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *SQLiteStore) FinishBillingReconcileRun(id int64, status, summaryJSON, errorJSON string) error {
	_, err := s.db.Exec(`
		UPDATE billing_reconcile_runs
		SET status = ?, summary_json = ?, error_json = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, strings.TrimSpace(status), strings.TrimSpace(summaryJSON), strings.TrimSpace(errorJSON), id)
	return err
}

func (s *SQLiteStore) GetLatestBusinessReconcileRun() (*models.BillingReconcileRun, error) {
	row := s.db.QueryRow(`
		SELECT id, triggered_by, status, summary_json, error_json, started_at, finished_at
		FROM billing_reconcile_runs
		ORDER BY started_at DESC
		LIMIT 1
	`)
	var run models.BillingReconcileRun
	var startedAt string
	var finishedAt sql.NullString
	err := row.Scan(&run.ID, &run.TriggeredBy, &run.Status, &run.SummaryJSON, &run.ErrorJSON, &startedAt, &finishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.StartedAt, _ = parseSQLiteTime(startedAt)
	run.FinishedAt = parseNullSQLiteTime(finishedAt)
	return &run, nil
}

func (s *SQLiteStore) ListUsersWithStripeCustomerIDs() ([]models.User, error) {
	rows, err := s.db.Query(`
		SELECT id, email, password_hash, name, tier, role, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users
		WHERE IFNULL(stripe_customer_id, '') <> ''
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var user models.User
		var crossBorder int
		var createdAt, updatedAt string
		if err := rows.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.Role, &user.IsAdmin,
			&user.StripeCustomer, &user.CountryCode, &user.Region, &user.City, &user.PostalCode,
			&user.PreferredRadiusKm, &crossBorder, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		user.CrossBorderEnabled = crossBorder == 1
		user.CountryCode = strings.ToUpper(strings.TrimSpace(user.CountryCode))
		user.CreatedAt, _ = parseSQLiteTime(createdAt)
		user.UpdatedAt, _ = parseSQLiteTime(updatedAt)
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) GetStripeSubscriptionSnapshot(subscriptionID string) (*models.StripeSubscriptionSnapshot, error) {
	row := s.db.QueryRow(`
		SELECT subscription_id, customer_id, user_id, status, plan_price_id, plan_interval, currency, unit_amount, quantity,
		       current_period_start, current_period_end, cancel_at_period_end, canceled_at, paused, latest_invoice_id,
		       default_payment_method, raw_json, updated_at, created_at
		FROM stripe_subscription_snapshots
		WHERE subscription_id = ?
	`, strings.TrimSpace(subscriptionID))
	var snapshot models.StripeSubscriptionSnapshot
	var periodStart, periodEnd, canceledAt sql.NullString
	var updatedAt, createdAt string
	var cancelAtEnd, paused int
	err := row.Scan(&snapshot.SubscriptionID, &snapshot.CustomerID, &snapshot.UserID, &snapshot.Status, &snapshot.PlanPriceID,
		&snapshot.PlanInterval, &snapshot.Currency, &snapshot.UnitAmount, &snapshot.Quantity, &periodStart, &periodEnd,
		&cancelAtEnd, &canceledAt, &paused, &snapshot.LatestInvoiceID, &snapshot.DefaultPaymentMethod, &snapshot.RawJSON,
		&updatedAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot.CurrentPeriodStart = parseNullSQLiteTime(periodStart)
	snapshot.CurrentPeriodEnd = parseNullSQLiteTime(periodEnd)
	snapshot.CanceledAt = parseNullSQLiteTime(canceledAt)
	snapshot.CancelAtPeriodEnd = cancelAtEnd == 1
	snapshot.Paused = paused == 1
	snapshot.UpdatedAt, _ = parseSQLiteTime(updatedAt)
	snapshot.CreatedAt, _ = parseSQLiteTime(createdAt)
	return &snapshot, nil
}

func (s *SQLiteStore) GetBusinessOverview(days int) (models.BusinessOverview, error) {
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

	var failedPayments int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_invoice_summaries
		WHERE created_at >= datetime('now', '-' || ? || ' days')
		  AND paid = 0
		  AND amount_remaining > 0
	`, days).Scan(&failedPayments); err != nil {
		return overview, err
	}
	overview.FailedPayments = failedPayments

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
		WHERE created_at >= datetime('now', '-' || ? || ' days')
		  AND (status IN ('canceled', 'incomplete_expired') OR event_type LIKE '%deleted')
	`, days).Scan(&churned); err != nil {
		return overview, err
	}
	if overview.ActivePaidAccounts+churned > 0 {
		overview.ChurnRatePct = float64(churned) * 100 / float64(overview.ActivePaidAccounts+churned)
	}

	var lastWebhook sql.NullString
	if err := s.db.QueryRow(`
		SELECT MAX(received_at) FROM stripe_webhook_events
	`).Scan(&lastWebhook); err != nil {
		return overview, err
	}
	if lastWebhook.Valid {
		if parsed, err := parseSQLiteTime(lastWebhook.String); err == nil {
			overview.WebhookLagMinutes = int(time.Since(parsed).Minutes())
		}
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

func (s *SQLiteStore) ListBusinessSubscriptions(filter models.BusinessSubscriptionFilter) ([]models.BusinessSubscriptionRow, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	status := strings.TrimSpace(filter.Status)
	planPriceID := strings.TrimSpace(filter.PlanPriceID)
	userID := strings.TrimSpace(filter.UserID)
	country := strings.ToUpper(strings.TrimSpace(filter.CountryCode))

	rows, err := s.db.Query(`
		SELECT ss.subscription_id, ss.customer_id, ss.user_id, COALESCE(u.email, ''), COALESCE(u.tier, 'free'),
		       ss.status, ss.plan_price_id, ss.plan_interval, ss.currency, ss.unit_amount, ss.quantity,
		       ss.current_period_start, ss.current_period_end, ss.cancel_at_period_end, ss.paused, ss.latest_invoice_id,
		       COALESCE(inv.status, ''), COALESCE(inv.amount_due, 0), COALESCE(inv.amount_paid, 0), COALESCE(inv.amount_remaining, 0),
		       COALESCE(inv.attempt_count, 0), ss.updated_at
		FROM stripe_subscription_snapshots ss
		LEFT JOIN users u ON u.id = ss.user_id
		LEFT JOIN stripe_invoice_summaries inv ON inv.invoice_id = ss.latest_invoice_id
		WHERE (? = '' OR ss.status = ?)
		  AND (? = '' OR ss.plan_price_id = ?)
		  AND (? = '' OR ss.user_id = ?)
		  AND (? = '' OR UPPER(COALESCE(u.country_code, '')) = ?)
		ORDER BY ss.updated_at DESC
		LIMIT ?
	`, status, status, planPriceID, planPriceID, userID, userID, country, country, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]models.BusinessSubscriptionRow, 0, limit)
	for rows.Next() {
		var row models.BusinessSubscriptionRow
		var periodStart, periodEnd, updatedAt sql.NullString
		var cancelAtEnd, paused int
		if err := rows.Scan(
			&row.SubscriptionID, &row.CustomerID, &row.UserID, &row.UserEmail, &row.UserTier,
			&row.Status, &row.PlanPriceID, &row.PlanInterval, &row.Currency, &row.UnitAmount, &row.Quantity,
			&periodStart, &periodEnd, &cancelAtEnd, &paused, &row.LatestInvoiceID,
			&row.InvoiceStatus, &row.AmountDue, &row.AmountPaid, &row.AmountRemaining, &row.AttemptCount,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		row.CancelAtPeriodEnd = cancelAtEnd == 1
		row.Paused = paused == 1
		row.CurrentPeriodStart = parseNullSQLiteTime(periodStart)
		row.CurrentPeriodEnd = parseNullSQLiteTime(periodEnd)
		row.UpdatedAt = parseNullSQLiteTime(updatedAt)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetBusinessRevenue(days int) ([]models.BusinessRevenuePoint, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := s.db.Query(`
		SELECT strftime('%Y-%m-%d', created_at) AS bucket_day,
		       UPPER(COALESCE(currency, 'EUR')) AS currency,
		       COALESCE(SUM(amount_paid), 0) AS amount_paid,
		       COUNT(*) AS invoices
		FROM stripe_invoice_summaries
		WHERE paid = 1
		  AND created_at >= datetime('now', '-' || ? || ' days')
		GROUP BY bucket_day, currency
		ORDER BY bucket_day ASC
	`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]models.BusinessRevenuePoint, 0)
	for rows.Next() {
		var day, currency string
		var point models.BusinessRevenuePoint
		if err := rows.Scan(&day, &currency, &point.AmountPaid, &point.Invoices); err != nil {
			return nil, err
		}
		parsed, _ := time.Parse("2006-01-02", day)
		point.BucketStart = parsed.UTC()
		point.Currency = currency
		points = append(points, point)
	}
	return points, rows.Err()
}

func (s *SQLiteStore) GetBusinessFunnel(days int) (models.BusinessFunnel, error) {
	if days <= 0 {
		days = 30
	}
	funnel := models.BusinessFunnel{WindowDays: days}

	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM users
		WHERE created_at >= datetime('now', '-' || ? || ' days')
	`, days).Scan(&funnel.Signups); err != nil {
		return funnel, err
	}
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(DISTINCT u.id), 0)
		FROM users u
		WHERE u.created_at >= datetime('now', '-' || ? || ' days')
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
		WHERE u.created_at >= datetime('now', '-' || ? || ' days')
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

func (s *SQLiteStore) GetBusinessCohorts(months int) ([]models.BusinessCohortRow, error) {
	if months <= 0 {
		months = 6
	}
	rows, err := s.db.Query(`
		SELECT strftime('%Y-%m', u.created_at) AS cohort_month,
		       COUNT(*) AS users,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = 1
					  AND strftime('%Y-%m', inv.created_at) = strftime('%Y-%m', u.created_at)
			   ) THEN 1 ELSE 0 END) AS paid_m0,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = 1
					  AND strftime('%Y-%m', inv.created_at) = strftime('%Y-%m', date(u.created_at, '+1 month'))
			   ) THEN 1 ELSE 0 END) AS paid_m1,
		       SUM(CASE WHEN EXISTS (
					SELECT 1 FROM stripe_invoice_summaries inv
					WHERE inv.user_id = u.id
					  AND inv.paid = 1
					  AND strftime('%Y-%m', inv.created_at) = strftime('%Y-%m', date(u.created_at, '+2 month'))
			   ) THEN 1 ELSE 0 END) AS paid_m2,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (julianday(ss.updated_at) - julianday(u.created_at)) <= 30 THEN 1
					ELSE 0 END) AS churn_early,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (julianday(ss.updated_at) - julianday(u.created_at)) > 30
					 AND (julianday(ss.updated_at) - julianday(u.created_at)) <= 90 THEN 1
					ELSE 0 END) AS churn_middle,
		       SUM(CASE
					WHEN ss.status IN ('canceled', 'incomplete_expired')
					 AND (julianday(ss.updated_at) - julianday(u.created_at)) > 90 THEN 1
					ELSE 0 END) AS churn_late
		FROM users u
		LEFT JOIN stripe_subscription_snapshots ss ON ss.user_id = u.id
		WHERE u.created_at >= datetime('now', '-' || ? || ' months')
		GROUP BY cohort_month
		ORDER BY cohort_month DESC
		LIMIT ?
	`, months, months)
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

func (s *SQLiteStore) GetBusinessAlerts(days int) ([]models.BusinessAlert, error) {
	if days <= 0 {
		days = 7
	}
	alerts := make([]models.BusinessAlert, 0)

	var failed24h int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_invoice_summaries
		WHERE paid = 0
		  AND amount_remaining > 0
		  AND created_at >= datetime('now', '-1 day')
	`).Scan(&failed24h); err != nil {
		return nil, err
	}
	if failed24h >= 3 {
		alerts = append(alerts, models.BusinessAlert{
			Key:         "failed_payments_spike",
			Severity:    "high",
			Title:       "Failed payment spike",
			Description: "More than 3 failed invoices in the last 24h.",
			Value:       strconv.Itoa(failed24h),
			Threshold:   ">= 3",
		})
	}

	var churned int
	if err := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*), 0)
		FROM stripe_subscription_history
		WHERE created_at >= datetime('now', '-' || ? || ' days')
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

func (s *SQLiteStore) sumPaidRevenueInWindow(days int) (float64, error) {
	rows, err := s.db.Query(`
		SELECT UPPER(COALESCE(currency, 'EUR')), COALESCE(SUM(amount_paid), 0)
		FROM stripe_invoice_summaries
		WHERE paid = 1
		  AND created_at >= datetime('now', '-' || ? || ' days')
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

func (s *SQLiteStore) sumPaidRevenueRange(fromDaysAgo, toDaysAgo int) (float64, error) {
	rows, err := s.db.Query(`
		SELECT UPPER(COALESCE(currency, 'EUR')), COALESCE(SUM(amount_paid), 0)
		FROM stripe_invoice_summaries
		WHERE paid = 1
		  AND created_at < datetime('now', '-' || ? || ' days')
		  AND created_at >= datetime('now', '-' || ? || ' days')
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

func (s *SQLiteStore) fillSQLiteSearchOpsBreakdown(days int, column string, out *map[string]int) error {
	rows, err := s.db.Query(`
		SELECT `+column+`, COUNT(*)
		FROM search_run_log
		WHERE started_at >= datetime('now', '-' || ? || ' days')
		GROUP BY `+column+`
	`, days)
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

// RecordPrice saves a price data point for market average calculation.
func (s *SQLiteStore) RecordPrice(query string, categoryID int, marketplaceID string, price int) error {
	_, err := s.db.Exec(
		"INSERT INTO price_history (query, category_id, marketplace_id, price) VALUES (?, ?, ?, ?)",
		query, categoryID, marketplaceID, price,
	)
	return err
}

// GetMarketAverage returns the average price in cents from recent listings for a query.
// Returns 0 and false if not enough samples are available.
func (s *SQLiteStore) GetMarketAverage(query string, categoryID int, marketplaceID string, minSamples int) (int, bool, error) {
	var avg sql.NullFloat64
	var count int

	err := s.db.QueryRow(`
		SELECT AVG(price), COUNT(*) FROM (
			SELECT price FROM price_history
			WHERE query = ? AND category_id = ? AND marketplace_id = ?
			AND timestamp > datetime('now', '-30 days')
			ORDER BY timestamp DESC
			LIMIT ?
		)
	`, query, categoryID, marketplaceID, minSamples).Scan(&avg, &count)
	if err != nil {
		return 0, false, err
	}

	if count < minSamples || !avg.Valid {
		return 0, false, nil
	}

	return int(avg.Float64), true, nil
}

// MarkOffered flags that we've sent a message for this listing.
func (s *SQLiteStore) MarkOffered(userID, itemID string) error {
	_, err := s.db.Exec("UPDATE listings SET offered = 1 WHERE item_id = ?", scopedItemID(userID, itemID))
	return err
}

// WasOffered checks if we already sent a message for this listing.
func (s *SQLiteStore) WasOffered(userID, itemID string) (bool, error) {
	var offered int
	err := s.db.QueryRow("SELECT offered FROM listings WHERE item_id = ?", scopedItemID(userID, itemID)).Scan(&offered)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return offered == 1, nil
}

// GetPriceHistory returns price points for trend analysis.
func (s *SQLiteStore) GetPriceHistory(query string) ([]models.PricePoint, error) {
	rows, err := s.db.Query(`
		SELECT query, price, timestamp FROM price_history
		WHERE query = ?
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
		var ts string
		if err := rows.Scan(&p.Query, &p.Price, &ts); err != nil {
			return nil, err
		}
		if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
			p.Timestamp = t
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// GetComparableDeals returns recent listings for the same configured query to help estimate fair value.
func (s *SQLiteStore) GetComparableDeals(userID, query, excludeItemID string, limit int) ([]models.ComparableDeal, error) {
	rows, err := s.db.Query(`
		SELECT item_id, title, price, score, first_seen
		FROM listings
		WHERE query = ?
		  AND item_id LIKE ?
		  AND item_id != ?
		  AND price > 0
		  AND COALESCE(feedback, '') <> 'dismissed'
		ORDER BY last_seen DESC
		LIMIT ?
	`, query, scopedItemPrefix(userID), scopedItemID(userID, excludeItemID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deals []models.ComparableDeal
	for rows.Next() {
		var deal models.ComparableDeal
		var firstSeen string
		if err := rows.Scan(&deal.ItemID, &deal.Title, &deal.Price, &deal.Score, &firstSeen); err != nil {
			return nil, err
		}
		deal.ItemID = unscopedItemID(deal.ItemID)
		deal.MatchReason = strings.TrimSpace(deal.Title)
		if t, err := parseSQLiteTime(firstSeen); err == nil {
			deal.LastSeen = t
		}
		deals = append(deals, deal)
	}

	return deals, rows.Err()
}

func scanSQLiteMission(scanner interface{ Scan(dest ...any) error }) (models.Mission, error) {
	return scanSQLiteMissionInternal(scanner, false)
}

func scanSQLiteMissionWithStats(scanner interface{ Scan(dest ...any) error }) (models.Mission, error) {
	return scanSQLiteMissionInternal(scanner, true)
}

func scanSQLiteMissionInternal(scanner interface{ Scan(dest ...any) error }, withStats bool) (models.Mission, error) {
	var mission models.Mission
	var preferredJSON, requiredJSON, niceJSON, queriesJSON, avoidFlagsJSON, marketplaceScopeJSON string
	var active, crossBorder int
	var createdAt, updatedAt string

	if withStats {
		var lastMatchAt string
		err := scanner.Scan(
			&mission.ID, &mission.UserID, &mission.Name, &mission.TargetQuery, &mission.CategoryID,
			&mission.BudgetMax, &mission.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
			&mission.RiskTolerance, &mission.ZipCode, &mission.Distance, &queriesJSON,
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.CountryCode, &mission.Region, &mission.City, &mission.PostalCode,
			&crossBorder, &marketplaceScopeJSON, &mission.Category,
			&active, &createdAt, &updatedAt, &mission.MatchCount, &lastMatchAt,
		)
		if err != nil {
			return mission, err
		}
		if strings.TrimSpace(lastMatchAt) != "" {
			mission.LastMatchAt, _ = parseSQLiteTime(lastMatchAt)
		}
	} else {
		err := scanner.Scan(
			&mission.ID, &mission.UserID, &mission.Name, &mission.TargetQuery, &mission.CategoryID,
			&mission.BudgetMax, &mission.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
			&mission.RiskTolerance, &mission.ZipCode, &mission.Distance, &queriesJSON,
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.CountryCode, &mission.Region, &mission.City, &mission.PostalCode,
			&crossBorder, &marketplaceScopeJSON, &mission.Category,
			&active, &createdAt, &updatedAt,
		)
		if err != nil {
			return mission, err
		}
	}

	mission.Active = active == 1
	_ = json.Unmarshal([]byte(preferredJSON), &mission.PreferredCondition)
	_ = json.Unmarshal([]byte(requiredJSON), &mission.RequiredFeatures)
	_ = json.Unmarshal([]byte(niceJSON), &mission.NiceToHave)
	_ = json.Unmarshal([]byte(queriesJSON), &mission.SearchQueries)
	_ = json.Unmarshal([]byte(avoidFlagsJSON), &mission.AvoidFlags)
	_ = json.Unmarshal([]byte(marketplaceScopeJSON), &mission.MarketplaceScope)
	mission.CreatedAt, _ = parseSQLiteTime(createdAt)
	mission.UpdatedAt, _ = parseSQLiteTime(updatedAt)
	mission.CountryCode = strings.ToUpper(strings.TrimSpace(mission.CountryCode))
	mission.CrossBorderEnabled = crossBorder == 1
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

func parseSQLiteTime(value string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", value)
}

func scanUser(row *sql.Row) (*models.User, error) {
	var user models.User
	var crossBorder int
	var createdAt, updatedAt string
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.Role, &user.IsAdmin, &user.StripeCustomer,
		&user.CountryCode, &user.Region, &user.City, &user.PostalCode, &user.PreferredRadiusKm, &crossBorder, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	user.CountryCode = strings.ToUpper(strings.TrimSpace(user.CountryCode))
	user.CrossBorderEnabled = crossBorder == 1
	user.CreatedAt, _ = parseSQLiteTime(createdAt)
	user.UpdatedAt, _ = parseSQLiteTime(updatedAt)
	return &user, nil
}

func scanSearchSpec(scanner interface {
	Scan(dest ...any) error
}) (models.SearchSpec, error) {
	var spec models.SearchSpec
	var conditionJSON, attributesJSON string
	var autoMessage, enabled int
	var nextRunAt, lastRunAt, lastSignalAt, lastErrorAt sql.NullString
	var checkIntervalSeconds int64
	err := scanner.Scan(
		&spec.ID, &spec.UserID, &spec.ProfileID, &spec.Name, &spec.Query, &spec.MarketplaceID, &spec.CountryCode, &spec.City, &spec.PostalCode, &spec.RadiusKm, &spec.CategoryID,
		&spec.MaxPrice, &spec.MinPrice, &conditionJSON, &spec.OfferPercentage, &autoMessage,
		&spec.MessageTemplate, &attributesJSON, &enabled, &checkIntervalSeconds, &spec.PriorityClass,
		&nextRunAt, &lastRunAt, &lastSignalAt, &lastErrorAt, &spec.LastResultCount, &spec.ConsecutiveEmptyRuns, &spec.ConsecutiveFailures,
	)
	if err != nil {
		return spec, err
	}
	spec.AutoMessage = autoMessage == 1
	spec.Enabled = enabled == 1
	spec.MarketplaceID = marketplace.NormalizeMarketplaceID(spec.MarketplaceID)
	spec.CountryCode = strings.ToUpper(strings.TrimSpace(spec.CountryCode))
	spec.CheckInterval = time.Duration(checkIntervalSeconds) * time.Second
	_ = json.Unmarshal([]byte(conditionJSON), &spec.Condition)
	_ = json.Unmarshal([]byte(attributesJSON), &spec.Attributes)
	spec.NextRunAt = parseNullSQLiteTime(nextRunAt)
	spec.LastRunAt = parseNullSQLiteTime(lastRunAt)
	spec.LastSignalAt = parseNullSQLiteTime(lastSignalAt)
	spec.LastErrorAt = parseNullSQLiteTime(lastErrorAt)
	return spec, nil
}

func normalizeMarketplaceScope(scope []string) []string {
	if len(scope) == 0 {
		return nil
	}
	out := make([]string, 0, len(scope))
	seen := map[string]bool{}
	for _, value := range scope {
		value = marketplace.NormalizeMarketplaceID(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func sqliteTimeOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func sqliteTimeOrZero(value time.Time) string {
	if value.IsZero() {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return value.UTC().Format(time.RFC3339)
}

func parseNullSQLiteTime(value sql.NullString) time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return time.Time{}
	}
	parsed, _ := parseSQLiteTime(value.String)
	return parsed
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func subscriptionIsPaidActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "trialing", "past_due", "unpaid":
		return true
	default:
		return false
	}
}

func amountToEUR(amountMinor float64, currency string) float64 {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	rate := 1.0
	switch currency {
	case "", "EUR":
		rate = 1.0
	case "USD":
		rate = 0.92
	case "GBP":
		rate = 1.16
	case "BGN":
		rate = 0.51
	case "DKK":
		rate = 0.13
	default:
		rate = 1.0
	}
	return (amountMinor / 100.0) * rate
}

func scopedItemID(userID, itemID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = "local"
	}
	return userID + "::" + itemID
}

func scopedItemPrefix(userID string) string {
	return scopedItemID(userID, "") + "%"
}

func unscopedItemID(value string) string {
	parts := strings.SplitN(value, "::", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return value
}

// ErrListingNotFound is returned when a listing lookup finds no row for the
// given (userID, itemID) pair.
var ErrListingNotFound = fmt.Errorf("listing not found")
