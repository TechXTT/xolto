package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("opening postgres database: %w", err)
	}
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
			action TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			before_json TEXT NOT NULL DEFAULT '',
			after_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created ON admin_audit_log(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor ON admin_audit_log(actor_user_id, created_at DESC);
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
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS profile_id BIGINT NOT NULL DEFAULT 0`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback TEXT NOT NULL DEFAULT ''`)
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS feedback_at TIMESTAMPTZ NULL`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_listings_feedback ON listings(profile_id, feedback)`)
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
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_usage_log (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT '',
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
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_usage_user ON ai_usage_log(user_id, created_at DESC)`)
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
		SELECT id, user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
		       ask_price, fair_price, verdict, concerns::text, suggested_questions::text, status, created_at, updated_at
		FROM shortlist_entries
		WHERE user_id = $1
		ORDER BY updated_at DESC
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
		SELECT id, user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
		       ask_price, fair_price, verdict, concerns::text, suggested_questions::text, status, created_at, updated_at
		FROM shortlist_entries
		WHERE user_id = $1 AND item_id = $2
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
		INSERT INTO users (id, email, password_hash, name, tier)
		VALUES ($1, $2, $3, $4, 'free')
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
		SELECT id, email, password_hash, name, tier, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email)))
	return scanPGUser(row)
}

func (s *PostgresStore) GetUserByID(id string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, is_admin, stripe_customer_id, country_code, region, city, postal_code,
		       preferred_radius_km, cross_border_enabled, created_at, updated_at
		FROM users WHERE id = $1
	`, id)
	return scanPGUser(row)
}

func (s *PostgresStore) GetUserByAuthIdentity(provider, subject string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT u.id, u.email, u.password_hash, u.name, u.tier, u.is_admin, u.stripe_customer_id, u.country_code, u.region, u.city,
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

func (s *PostgresStore) SetUserAdmin(userID string, isAdmin bool) error {
	_, err := s.db.Exec(`UPDATE users SET is_admin = $1, updated_at = NOW() WHERE id = $2`, isAdmin, userID)
	return err
}

func (s *PostgresStore) RecordAIUsage(entry models.AIUsageEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO ai_usage_log (user_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, entry.UserID, entry.CallType, entry.Model, entry.PromptTokens, entry.CompletionTokens, entry.TotalTokens,
		entry.LatencyMs, entry.Success, entry.ErrorMsg)
	return err
}

func (s *PostgresStore) ListAllUsers() ([]models.AdminUserSummary, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.email, u.password_hash, u.name, u.tier, u.is_admin, u.stripe_customer_id, u.created_at, u.updated_at,
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
		if err := rows.Scan(&s.ID, &s.Email, &s.PasswordHash, &s.Name, &s.Tier, &s.IsAdmin, &s.StripeCustomer,
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
		SELECT id, user_id, call_type, model, prompt_tokens, completion_tokens, total_tokens, latency_ms, success, error_msg, created_at
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
		if err := rows.Scan(&e.ID, &e.UserID, &e.CallType, &e.Model, &e.PromptTokens, &e.CompletionTokens,
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
			actor_user_id, action, target_type, target_id, before_json, after_json
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, strings.TrimSpace(entry.ActorUserID), strings.TrimSpace(entry.Action), strings.TrimSpace(entry.TargetType),
		strings.TrimSpace(entry.TargetID), strings.TrimSpace(entry.BeforeJSON), strings.TrimSpace(entry.AfterJSON))
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
		SELECT id, actor_user_id, action, target_type, target_id, before_json, after_json, created_at
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
		if err := rows.Scan(&entry.ID, &entry.ActorUserID, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.BeforeJSON, &entry.AfterJSON, &entry.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
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
		       last_seen, feedback
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
			&listing.Reason, &riskFlagsJSON, &listing.Date, &listing.Feedback,
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

// GetApprovedComparables returns listings the user has explicitly approved for
// this mission. They act as high-confidence comparables when scoring new
// listings — the scorer treats them as ground-truth relevant hits so the
// reasoner can calibrate fair value and relevance against confirmed matches.
func (s *PostgresStore) GetApprovedComparables(userID string, missionID int64, limit int) ([]models.ComparableDeal, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT item_id, title, price, score, last_seen
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

func (s *PostgresStore) GetListingScoringState(userID, itemID string) (price int, reasoningSource string, found bool, err error) {
	err = s.db.QueryRow(`
		SELECT price, reasoning_source
		FROM listings
		WHERE item_id = $1
	`, scopedItemID(userID, itemID)).Scan(&price, &reasoningSource)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return price, reasoningSource, true, nil
}

func (s *PostgresStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, profile_id, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, reasoning_source, risk_flags,
			first_seen, last_seen
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NOW(),NOW())
		ON CONFLICT(item_id) DO UPDATE SET
			price          = EXCLUDED.price,
			score          = EXCLUDED.score,
			profile_id     = EXCLUDED.profile_id,
			image_urls     = EXCLUDED.image_urls,
			url            = EXCLUDED.url,
			condition      = EXCLUDED.condition,
			marketplace_id = EXCLUDED.marketplace_id,
			fair_price     = EXCLUDED.fair_price,
			offer_price    = EXCLUDED.offer_price,
			confidence     = EXCLUDED.confidence,
			reasoning      = EXCLUDED.reasoning,
			reasoning_source = EXCLUDED.reasoning_source,
			risk_flags     = EXCLUDED.risk_flags,
			last_seen      = NOW()
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, l.ProfileID, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, scored.ReasoningSource, string(riskFlagsJSON),
	)
	return err
}

func (s *PostgresStore) TouchListing(userID, itemID string) error {
	_, err := s.db.Exec(`UPDATE listings SET last_seen = NOW() WHERE item_id = $1`, scopedItemID(userID, itemID))
	return err
}

func (s *PostgresStore) RecordPrice(query string, categoryID int, price int) error {
	_, err := s.db.Exec(`INSERT INTO price_history (query, category_id, price) VALUES ($1, $2, $3)`, query, categoryID, price)
	return err
}

func (s *PostgresStore) GetMarketAverage(query string, categoryID int, minSamples int) (int, bool, error) {
	type result struct {
		Avg   sql.NullFloat64
		Count int
	}
	var res result
	err := s.db.QueryRow(`
		SELECT AVG(price)::float8, COUNT(*)
		FROM (
			SELECT price
			FROM price_history
			WHERE query = $1 AND category_id = $2
			  AND timestamp > NOW() - INTERVAL '7 days'
			ORDER BY timestamp DESC
			LIMIT $3
		) recent
	`, query, categoryID, minSamples).Scan(&res.Avg, &res.Count)
	if err != nil {
		return 0, false, err
	}
	if res.Count < minSamples || !res.Avg.Valid {
		return 0, false, nil
	}
	return int(res.Avg.Float64), true, nil
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
		SELECT item_id, title, price, score, last_seen
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
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.IsAdmin, &user.StripeCustomer,
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
