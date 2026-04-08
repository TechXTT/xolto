package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/models"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

type CleanupStats struct {
	ListingsDeleted     int64
	PriceHistoryDeleted int64
}

var _ Store = (*SQLiteStore)(nil)

func New(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

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
			offered     INTEGER NOT NULL DEFAULT 0,
			query       TEXT NOT NULL DEFAULT '',
			profile_id  INTEGER NOT NULL DEFAULT 0,
			image_urls  TEXT NOT NULL DEFAULT '[]',
			first_seen  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_seen   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS price_history (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			query     TEXT NOT NULL,
			category_id INTEGER NOT NULL DEFAULT 0,
			price     INTEGER NOT NULL,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
			stripe_customer_id TEXT NOT NULL DEFAULT '',
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
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN risk_flags TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN profile_id INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN urgency TEXT NOT NULL DEFAULT 'flexible'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN avoid_flags TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN travel_radius INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE shopping_profiles ADD COLUMN category TEXT NOT NULL DEFAULT 'other'`)
	_, _ = db.Exec(`ALTER TABLE search_configs ADD COLUMN profile_id INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_search_configs_profile ON search_configs(profile_id, enabled, updated_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_listings_profile ON listings(profile_id, last_seen)`)
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
				status = ?, urgency = ?, avoid_flags = ?, travel_radius = ?, category = ?,
				active = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`,
			mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
			string(conditionsJSON), string(requiredJSON), string(niceToHaveJSON), mission.RiskTolerance,
			mission.ZipCode, mission.Distance, string(queriesJSON),
			mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius, mission.Category,
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
			status, urgency, avoid_flags, travel_radius, category,
			active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		mission.UserID, mission.Name, mission.TargetQuery, mission.CategoryID, mission.BudgetMax, mission.BudgetStretch,
		string(conditionsJSON), string(requiredJSON), string(niceToHaveJSON), mission.RiskTolerance,
		mission.ZipCode, mission.Distance, string(queriesJSON),
		mission.Status, mission.Urgency, string(avoidFlagsJSON), mission.TravelRadius, mission.Category,
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
			status, urgency, avoid_flags, travel_radius, category,
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
			status, urgency, avoid_flags, travel_radius, category,
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
			m.status, m.urgency, m.avoid_flags, m.travel_radius, m.category,
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
		SELECT id, user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
			ask_price, fair_price, verdict, concerns, suggested_questions, status, created_at, updated_at
		FROM shortlist_entries
		WHERE user_id = ?
		ORDER BY updated_at DESC
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
		SELECT id, user_id, profile_id, item_id, title, url, recommendation_label, recommendation_score,
			ask_price, fair_price, verdict, concerns, suggested_questions, status, created_at, updated_at
		FROM shortlist_entries
		WHERE user_id = ? AND item_id = ?
	`, userID, itemID)

	var entry models.ShortlistEntry
	var concernsJSON, questionsJSON, createdAt, updatedAt string
	err := row.Scan(
		&entry.ID, &entry.UserID, &entry.MissionID, &entry.ItemID, &entry.Title, &entry.URL,
		&entry.RecommendationLabel, &entry.RecommendationScore, &entry.AskPrice, &entry.FairPrice,
		&entry.Verdict, &concernsJSON, &questionsJSON, &entry.Status, &createdAt, &updatedAt,
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
		INSERT INTO users (id, email, password_hash, name, tier)
		VALUES (?, ?, ?, ?, 'free')
	`, id, strings.ToLower(strings.TrimSpace(email)), hash, strings.TrimSpace(name))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *SQLiteStore) GetUserByEmail(email string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, stripe_customer_id, created_at, updated_at
		FROM users WHERE email = ?
	`, strings.ToLower(strings.TrimSpace(email)))
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByID(id string) (*models.User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, password_hash, name, tier, stripe_customer_id, created_at, updated_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

func (s *SQLiteStore) UpdateUserTier(userID, tier string) error {
	_, err := s.db.Exec(`UPDATE users SET tier = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, tier, userID)
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

func (s *SQLiteStore) GetSearchConfigs(userID string) ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, category_id, max_price, min_price,
		       condition_json, offer_percentage, auto_message, message_template, attributes_json,
		       enabled, check_interval_seconds
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
		var spec models.SearchSpec
		var conditionJSON, attributesJSON string
		var autoMessage, enabled int
		var checkIntervalSeconds int64
		if err := rows.Scan(
			&spec.ID, &spec.UserID, &spec.ProfileID, &spec.Name, &spec.Query, &spec.MarketplaceID, &spec.CategoryID,
			&spec.MaxPrice, &spec.MinPrice, &conditionJSON, &spec.OfferPercentage, &autoMessage,
			&spec.MessageTemplate, &attributesJSON, &enabled, &checkIntervalSeconds,
		); err != nil {
			return nil, err
		}
		spec.AutoMessage = autoMessage == 1
		spec.Enabled = enabled == 1
		spec.CheckInterval = time.Duration(checkIntervalSeconds) * time.Second
		_ = json.Unmarshal([]byte(conditionJSON), &spec.Condition)
		_ = json.Unmarshal([]byte(attributesJSON), &spec.Attributes)
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (s *SQLiteStore) CountSearchConfigs(userID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM search_configs WHERE user_id = ?`, userID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetAllEnabledSearchConfigs() ([]models.SearchSpec, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, profile_id, name, query, marketplace_id, category_id, max_price, min_price,
		       condition_json, offer_percentage, auto_message, message_template, attributes_json,
		       enabled, check_interval_seconds
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
		       last_seen
		FROM listings
		WHERE item_id LIKE ?
		  AND (? = 0 OR profile_id = ?)
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
			&listing.Reason, &riskFlagsJSON, &lastSeen,
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
			user_id, profile_id, name, query, marketplace_id, category_id, max_price, min_price,
			condition_json, offer_percentage, auto_message, message_template, attributes_json,
			enabled, check_interval_seconds
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, spec.UserID, spec.ProfileID, spec.Name, spec.Query, spec.MarketplaceID, spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, boolToInt(spec.AutoMessage), spec.MessageTemplate, string(attributesJSON),
		boolToInt(spec.Enabled), int64(spec.CheckInterval/time.Second))
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
		SET profile_id = ?, name = ?, query = ?, marketplace_id = ?, category_id = ?, max_price = ?, min_price = ?,
		    condition_json = ?, offer_percentage = ?, auto_message = ?, message_template = ?,
		    attributes_json = ?, enabled = ?, check_interval_seconds = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ?
	`, spec.ProfileID, spec.Name, spec.Query, spec.MarketplaceID, spec.CategoryID, spec.MaxPrice, spec.MinPrice,
		string(conditionJSON), spec.OfferPercentage, boolToInt(spec.AutoMessage), spec.MessageTemplate,
		string(attributesJSON), boolToInt(spec.Enabled), int64(spec.CheckInterval/time.Second), spec.ID, spec.UserID)
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

// SaveListing stores or updates a listing and its scored analysis.
func (s *SQLiteStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, profile_id, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, risk_flags,
			first_seen, last_seen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(item_id) DO UPDATE SET
			price          = excluded.price,
			score          = excluded.score,
			profile_id     = excluded.profile_id,
			image_urls     = excluded.image_urls,
			url            = excluded.url,
			condition      = excluded.condition,
			marketplace_id = excluded.marketplace_id,
			fair_price     = excluded.fair_price,
			offer_price    = excluded.offer_price,
			confidence     = excluded.confidence,
			reasoning      = excluded.reasoning,
			risk_flags     = excluded.risk_flags,
			last_seen      = CURRENT_TIMESTAMP
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, l.ProfileID, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, string(riskFlagsJSON),
	)
	return err
}

// RecordPrice saves a price data point for market average calculation.
func (s *SQLiteStore) RecordPrice(query string, categoryID int, price int) error {
	_, err := s.db.Exec(
		"INSERT INTO price_history (query, category_id, price) VALUES (?, ?, ?)",
		query, categoryID, price,
	)
	return err
}

// GetMarketAverage returns the average price in cents from recent listings for a query.
// Returns 0 and false if not enough samples are available.
func (s *SQLiteStore) GetMarketAverage(query string, categoryID int, minSamples int) (int, bool, error) {
	var avg sql.NullFloat64
	var count int

	err := s.db.QueryRow(`
		SELECT AVG(price), COUNT(*) FROM (
			SELECT price FROM price_history
			WHERE query = ? AND category_id = ?
			AND timestamp > datetime('now', '-7 days')
			ORDER BY timestamp DESC
			LIMIT ?
		)
	`, query, categoryID, minSamples).Scan(&avg, &count)
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
		SELECT item_id, title, price, score, last_seen
		FROM listings
		WHERE query = ? AND item_id LIKE ? AND item_id != ? AND price > 0
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
		var lastSeen string
		if err := rows.Scan(&deal.ItemID, &deal.Title, &deal.Price, &deal.Score, &lastSeen); err != nil {
			return nil, err
		}
		deal.ItemID = unscopedItemID(deal.ItemID)
		deal.MatchReason = strings.TrimSpace(deal.Title)
		if t, err := parseSQLiteTime(lastSeen); err == nil {
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
	var preferredJSON, requiredJSON, niceJSON, queriesJSON, avoidFlagsJSON string
	var active int
	var createdAt, updatedAt string

	if withStats {
		var lastMatchAt string
		err := scanner.Scan(
			&mission.ID, &mission.UserID, &mission.Name, &mission.TargetQuery, &mission.CategoryID,
			&mission.BudgetMax, &mission.BudgetStretch, &preferredJSON, &requiredJSON, &niceJSON,
			&mission.RiskTolerance, &mission.ZipCode, &mission.Distance, &queriesJSON,
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.Category,
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
			&mission.Status, &mission.Urgency, &avoidFlagsJSON, &mission.TravelRadius, &mission.Category,
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
	mission.CreatedAt, _ = parseSQLiteTime(createdAt)
	mission.UpdatedAt, _ = parseSQLiteTime(updatedAt)
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
	var createdAt, updatedAt string
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.Tier, &user.StripeCustomer, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
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
	var checkIntervalSeconds int64
	err := scanner.Scan(
		&spec.ID, &spec.UserID, &spec.ProfileID, &spec.Name, &spec.Query, &spec.MarketplaceID, &spec.CategoryID,
		&spec.MaxPrice, &spec.MinPrice, &conditionJSON, &spec.OfferPercentage, &autoMessage,
		&spec.MessageTemplate, &attributesJSON, &enabled, &checkIntervalSeconds,
	)
	if err != nil {
		return spec, err
	}
	spec.AutoMessage = autoMessage == 1
	spec.Enabled = enabled == 1
	spec.CheckInterval = time.Duration(checkIntervalSeconds) * time.Second
	_ = json.Unmarshal([]byte(conditionJSON), &spec.Condition)
	_ = json.Unmarshal([]byte(attributesJSON), &spec.Attributes)
	return spec, nil
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
