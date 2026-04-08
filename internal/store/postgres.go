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

	"github.com/TechXTT/marktbot/internal/models"
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
			offered    BOOLEAN NOT NULL DEFAULT FALSE,
			query      TEXT NOT NULL DEFAULT '',
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
			check_interval_seconds BIGINT NOT NULL DEFAULT 300,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_search_configs_user ON search_configs(user_id, enabled, updated_at DESC);

		CREATE TABLE IF NOT EXISTS stripe_events (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
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
	_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS risk_flags TEXT NOT NULL DEFAULT '[]'`)
	return nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) UpsertShoppingProfile(profile models.ShoppingProfile) (int64, error) {
	preferredJSON, _ := json.Marshal(profile.PreferredCondition)
	requiredJSON, _ := json.Marshal(profile.RequiredFeatures)
	niceJSON, _ := json.Marshal(profile.NiceToHave)
	queriesJSON, _ := json.Marshal(profile.SearchQueries)

	if profile.ID > 0 {
		_, err := s.db.Exec(`
			UPDATE shopping_profiles
			SET name = $1, target_query = $2, category_id = $3, budget_max = $4, budget_stretch = $5,
			    preferred_condition = $6::jsonb, required_features = $7::jsonb, nice_to_have = $8::jsonb,
			    risk_tolerance = $9, zip_code = $10, distance = $11, search_queries = $12::jsonb,
			    active = $13, updated_at = NOW()
			WHERE id = $14
		`,
			profile.Name, profile.TargetQuery, profile.CategoryID, profile.BudgetMax, profile.BudgetStretch,
			string(preferredJSON), string(requiredJSON), string(niceJSON), profile.RiskTolerance,
			profile.ZipCode, profile.Distance, string(queriesJSON), profile.Active, profile.ID,
		)
		return profile.ID, err
	}

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO shopping_profiles (
			user_id, name, target_query, category_id, budget_max, budget_stretch,
			preferred_condition, required_features, nice_to_have, risk_tolerance,
			zip_code, distance, search_queries, active
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13::jsonb, $14)
		RETURNING id
	`,
		profile.UserID, profile.Name, profile.TargetQuery, profile.CategoryID, profile.BudgetMax, profile.BudgetStretch,
		string(preferredJSON), string(requiredJSON), string(niceJSON), profile.RiskTolerance,
		profile.ZipCode, profile.Distance, string(queriesJSON), profile.Active,
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) GetActiveShoppingProfile(userID string) (*models.ShoppingProfile, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, name, target_query, category_id, budget_max, budget_stretch,
		       preferred_condition::text, required_features::text, nice_to_have::text, risk_tolerance,
		       zip_code, distance, search_queries::text, active, created_at, updated_at
		FROM shopping_profiles
		WHERE user_id = $1 AND active = TRUE
		ORDER BY updated_at DESC
		LIMIT 1
	`, userID)

	var profile models.ShoppingProfile
	var preferredJSON, requiredJSON, niceJSON, queriesJSON string
	err := row.Scan(
		&profile.ID, &profile.UserID, &profile.Name, &profile.TargetQuery, &profile.CategoryID,
		&profile.BudgetMax, &profile.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
		&profile.RiskTolerance, &profile.ZipCode, &profile.Distance, &queriesJSON, &profile.Active,
		&profile.CreatedAt, &profile.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(preferredJSON), &profile.PreferredCondition)
	_ = json.Unmarshal([]byte(requiredJSON), &profile.RequiredFeatures)
	_ = json.Unmarshal([]byte(niceJSON), &profile.NiceToHave)
	_ = json.Unmarshal([]byte(queriesJSON), &profile.SearchQueries)
	return &profile, nil
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
		entry.UserID, entry.ProfileID, entry.ItemID, entry.Title, entry.URL,
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
	if session.DraftProfile != nil {
		raw, err := json.Marshal(session.DraftProfile)
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
		var draft models.ShoppingProfile
		if err := json.Unmarshal([]byte(draftJSON), &draft); err == nil {
			session.DraftProfile = &draft
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

func (s *PostgresStore) GetUserByEmail(email string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, stripe_customer_id, created_at, updated_at
		FROM users WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email)))
	return scanPGUser(row)
}

func (s *PostgresStore) GetUserByID(id string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, stripe_customer_id, created_at, updated_at
		FROM users WHERE id = $1
	`, id)
	return scanPGUser(row)
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

func (s *PostgresStore) GetSearchConfigs(userID string) ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, name, query, marketplace_id, category_id, max_price, min_price,
		       condition_json::text, offer_percentage, auto_message, message_template, attributes_json::text,
		       enabled, check_interval_seconds
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

func (s *PostgresStore) GetAllEnabledSearchConfigs() ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, name, query, marketplace_id, category_id, max_price, min_price,
		       condition_json::text, offer_percentage, auto_message, message_template, attributes_json::text,
		       enabled, check_interval_seconds
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
	err := s.db.QueryRow(`SELECT COUNT(*) FROM search_configs WHERE user_id = $1`, userID).Scan(&count)
	return count, err
}

func (s *PostgresStore) CreateSearchConfig(spec models.SearchSpec) (int64, error) {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO search_configs (
			user_id, name, query, marketplace_id, category_id, max_price, min_price,
			condition_json, offer_percentage, auto_message, message_template, attributes_json,
			enabled, check_interval_seconds
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12::jsonb, $13, $14)
		RETURNING id
	`, spec.UserID, spec.Name, spec.Query, spec.MarketplaceID, spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, spec.AutoMessage, spec.MessageTemplate, string(attributesJSON),
		spec.Enabled, int64(spec.CheckInterval/time.Second),
	).Scan(&id)
	return id, err
}

func (s *PostgresStore) UpdateSearchConfig(spec models.SearchSpec) error {
	conditionJSON, _ := json.Marshal(spec.Condition)
	attributesJSON, _ := json.Marshal(spec.Attributes)
	_, err := s.db.Exec(`
		UPDATE search_configs
		SET name = $1, query = $2, marketplace_id = $3, category_id = $4, max_price = $5, min_price = $6,
		    condition_json = $7::jsonb, offer_percentage = $8, auto_message = $9, message_template = $10,
		    attributes_json = $11::jsonb, enabled = $12, check_interval_seconds = $13, updated_at = NOW()
		WHERE id = $14 AND user_id = $15
	`, spec.Name, spec.Query, spec.MarketplaceID, spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, spec.AutoMessage, spec.MessageTemplate,
		string(attributesJSON), spec.Enabled, int64(spec.CheckInterval/time.Second), spec.ID, spec.UserID,
	)
	return err
}

func (s *PostgresStore) DeleteSearchConfig(id int64, userID string) error {
	_, err := s.db.Exec(`DELETE FROM search_configs WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func (s *PostgresStore) ListRecentListings(userID string, limit int) ([]models.Listing, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT item_id, title, price, price_type, image_urls,
		       url, condition, marketplace_id,
		       score, fair_price, offer_price, confidence, reasoning, risk_flags,
		       last_seen
		FROM listings
		WHERE item_id LIKE $1
		ORDER BY last_seen DESC
		LIMIT $2
	`, scopedItemPrefix(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON string
		if err := rows.Scan(
			&listing.ItemID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &listing.Date,
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

func (s *PostgresStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, risk_flags,
			first_seen, last_seen
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW(),NOW())
		ON CONFLICT(item_id) DO UPDATE SET
			price          = EXCLUDED.price,
			score          = EXCLUDED.score,
			image_urls     = EXCLUDED.image_urls,
			url            = EXCLUDED.url,
			condition      = EXCLUDED.condition,
			marketplace_id = EXCLUDED.marketplace_id,
			fair_price     = EXCLUDED.fair_price,
			offer_price    = EXCLUDED.offer_price,
			confidence     = EXCLUDED.confidence,
			reasoning      = EXCLUDED.reasoning,
			risk_flags     = EXCLUDED.risk_flags,
			last_seen      = NOW()
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, string(riskFlagsJSON),
	)
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
		WHERE query = $1 AND item_id LIKE $2 AND item_id != $3 AND price > 0
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

func scanPGUser(row *sql.Row) (*models.User, error) {
	var user models.User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.StripeCustomer, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func scanPGSearchSpec(scanner interface{ Scan(dest ...any) error }) (models.SearchSpec, error) {
	var spec models.SearchSpec
	var conditionJSON, attributesJSON string
	var checkIntervalSeconds int64
	err := scanner.Scan(
		&spec.ID, &spec.UserID, &spec.Name, &spec.Query, &spec.MarketplaceID, &spec.CategoryID,
		&spec.MaxPrice, &spec.MinPrice, &conditionJSON, &spec.OfferPercentage, &spec.AutoMessage,
		&spec.MessageTemplate, &attributesJSON, &spec.Enabled, &checkIntervalSeconds,
	)
	if err != nil {
		return spec, err
	}
	spec.CheckInterval = time.Duration(checkIntervalSeconds) * time.Second
	_ = json.Unmarshal([]byte(conditionJSON), &spec.Condition)
	_ = json.Unmarshal([]byte(attributesJSON), &spec.Attributes)
	return spec, nil
}

func scanShortlistEntry(scanner interface{ Scan(dest ...any) error }) (models.ShortlistEntry, error) {
	var entry models.ShortlistEntry
	var concernsJSON, questionsJSON string
	err := scanner.Scan(
		&entry.ID, &entry.UserID, &entry.ProfileID, &entry.ItemID, &entry.Title, &entry.URL,
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
