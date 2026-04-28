package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// OutreachThread mirrors the outreach_threads table columns.
// All time fields are UTC.
type OutreachThread struct {
	ID                    int64
	UserID                string
	ListingID             string
	MarketplaceID         string
	MissionID             *int64
	DraftText             string
	DraftShape            string
	DraftLang             string
	SentAt                time.Time
	RepliedAt             *time.Time
	ReplyText             *string
	State                 string
	LastStateTransitionAt time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ListingKey identifies a listing uniquely within a marketplace.
type ListingKey struct {
	ListingID     string
	MarketplaceID string
}

// OutreachStore is the interface satisfied by both PostgresStore and SQLiteStore.
type OutreachStore interface {
	UpsertThreadOnSent(ctx context.Context, t OutreachThread) (OutreachThread, error)
	MarkReplied(ctx context.Context, userID, listingID, marketplaceID, replyText string) (OutreachThread, error)
	ListThreadsByUser(ctx context.Context, userID string, missionID *int64) ([]OutreachThread, error)
	GetThreadForListing(ctx context.Context, userID, listingID, marketplaceID string) (*OutreachThread, error)
	TransitionStaleThreads(ctx context.Context, cutoff time.Duration) (int64, error)
	ListThreadStatesForListings(ctx context.Context, userID string, listingKeys []ListingKey) (map[ListingKey]OutreachThread, error)
}

// scanOutreachThread scans one row from a query that selects all outreach_threads
// columns in the canonical order used throughout this file.
func scanOutreachThread(row interface {
	Scan(dest ...any) error
}) (OutreachThread, error) {
	var t OutreachThread
	var repliedAt sql.NullTime
	var replyText sql.NullString
	var missionID sql.NullInt64
	err := row.Scan(
		&t.ID,
		&t.UserID,
		&t.ListingID,
		&t.MarketplaceID,
		&missionID,
		&t.DraftText,
		&t.DraftShape,
		&t.DraftLang,
		&t.SentAt,
		&repliedAt,
		&replyText,
		&t.State,
		&t.LastStateTransitionAt,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		return OutreachThread{}, err
	}
	if missionID.Valid {
		t.MissionID = &missionID.Int64
	}
	if repliedAt.Valid {
		t.RepliedAt = &repliedAt.Time
	}
	if replyText.Valid {
		t.ReplyText = &replyText.String
	}
	return t, nil
}

const outreachSelectColumns = `
	id, user_id, listing_id, marketplace_id, mission_id,
	draft_text, draft_shape, draft_lang,
	sent_at, replied_at, reply_text,
	state, last_state_transition_at, created_at, updated_at`

// ---------------------------------------------------------------------------
// PostgresStore implementation
// ---------------------------------------------------------------------------

// UpsertThreadOnSent inserts a new outreach_threads row or updates the draft
// fields on a re-send. If the existing thread is already in state "replied",
// it is returned unchanged — re-sending after a reply does not reset state.
func (s *PostgresStore) UpsertThreadOnSent(ctx context.Context, t OutreachThread) (OutreachThread, error) {
	now := time.Now().UTC()
	var missionID *int64
	if t.MissionID != nil && *t.MissionID > 0 {
		missionID = t.MissionID
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO outreach_threads
			(user_id, listing_id, marketplace_id, mission_id,
			 draft_text, draft_shape, draft_lang,
			 sent_at, state, last_state_transition_at, created_at, updated_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, 'awaiting_reply', $8, $8, $8)
		ON CONFLICT (user_id, listing_id, marketplace_id) DO UPDATE
			SET draft_text              = CASE WHEN outreach_threads.state = 'replied' THEN outreach_threads.draft_text  ELSE EXCLUDED.draft_text  END,
			    draft_shape             = CASE WHEN outreach_threads.state = 'replied' THEN outreach_threads.draft_shape ELSE EXCLUDED.draft_shape END,
			    draft_lang              = CASE WHEN outreach_threads.state = 'replied' THEN outreach_threads.draft_lang  ELSE EXCLUDED.draft_lang  END,
			    sent_at                 = CASE WHEN outreach_threads.state = 'replied' THEN outreach_threads.sent_at     ELSE EXCLUDED.sent_at     END,
			    mission_id              = CASE WHEN outreach_threads.state = 'replied' THEN outreach_threads.mission_id  ELSE EXCLUDED.mission_id  END,
			    updated_at              = EXCLUDED.updated_at
		RETURNING`+" "+outreachSelectColumns,
		t.UserID, t.ListingID, t.MarketplaceID, missionID,
		t.DraftText, t.DraftShape, t.DraftLang,
		now,
	)
	return scanOutreachThread(row)
}

// MarkReplied transitions an outreach thread from awaiting_reply or stale to
// replied, recording the reply text and timestamp. Returns the updated thread.
// The caller must check the current state before calling; this function does
// not enforce the state machine — the handler layer does.
func (s *PostgresStore) MarkReplied(ctx context.Context, userID, listingID, marketplaceID, replyText string) (OutreachThread, error) {
	now := time.Now().UTC()
	row := s.db.QueryRowContext(ctx, `
		UPDATE outreach_threads
		SET state                   = 'replied',
		    replied_at              = $1,
		    reply_text              = $2,
		    last_state_transition_at = $1,
		    updated_at              = $1
		WHERE user_id = $3
		  AND listing_id = $4
		  AND marketplace_id = $5
		RETURNING`+" "+outreachSelectColumns,
		now, replyText, userID, listingID, marketplaceID,
	)
	t, err := scanOutreachThread(row)
	if err == sql.ErrNoRows {
		return OutreachThread{}, ErrOutreachThreadNotFound
	}
	return t, err
}

// ListThreadsByUser returns all outreach threads for a user, optionally
// filtered by mission_id. Results are ordered newest-first by
// last_state_transition_at DESC.
func (s *PostgresStore) ListThreadsByUser(ctx context.Context, userID string, missionID *int64) ([]OutreachThread, error) {
	query := `SELECT` + outreachSelectColumns + `
		FROM outreach_threads
		WHERE user_id = $1`
	args := []any{userID}
	if missionID != nil && *missionID > 0 {
		query += ` AND mission_id = $2`
		args = append(args, *missionID)
	}
	query += ` ORDER BY last_state_transition_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutreachThreadRows(rows)
}

// GetThreadForListing returns the outreach thread for a specific
// (user, listing, marketplace) triple, or nil if none exists.
func (s *PostgresStore) GetThreadForListing(ctx context.Context, userID, listingID, marketplaceID string) (*OutreachThread, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT`+" "+outreachSelectColumns+`
		FROM outreach_threads
		WHERE user_id = $1
		  AND listing_id = $2
		  AND marketplace_id = $3`,
		userID, listingID, marketplaceID,
	)
	t, err := scanOutreachThread(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TransitionStaleThreads sets state = 'stale' on all awaiting_reply threads
// whose last_state_transition_at is older than cutoff ago. Returns the number
// of rows updated.
func (s *PostgresStore) TransitionStaleThreads(ctx context.Context, cutoff time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outreach_threads
		SET state                    = 'stale',
		    last_state_transition_at = NOW(),
		    updated_at               = NOW()
		WHERE state = 'awaiting_reply'
		  AND last_state_transition_at < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int64(cutoff.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListThreadStatesForListings batch-loads thread states for the given listing
// keys for a specific user. The returned map is keyed by ListingKey and
// contains only listings that have an outreach thread — absent keys have no
// thread.
func (s *PostgresStore) ListThreadStatesForListings(ctx context.Context, userID string, listingKeys []ListingKey) (map[ListingKey]OutreachThread, error) {
	if len(listingKeys) == 0 {
		return map[ListingKey]OutreachThread{}, nil
	}

	// Build a VALUES clause for the IN-style join: (listing_id, marketplace_id) IN ...
	// We use unnest with two arrays for clean Postgres parameterisation.
	listingIDs := make([]string, len(listingKeys))
	marketplaceIDs := make([]string, len(listingKeys))
	for i, k := range listingKeys {
		listingIDs[i] = k.ListingID
		marketplaceIDs[i] = k.MarketplaceID
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ot.id, ot.user_id, ot.listing_id, ot.marketplace_id, ot.mission_id,
			ot.draft_text, ot.draft_shape, ot.draft_lang,
			ot.sent_at, ot.replied_at, ot.reply_text,
			ot.state, ot.last_state_transition_at, ot.created_at, ot.updated_at
		FROM outreach_threads ot
		JOIN unnest($2::text[], $3::text[]) AS keys(listing_id, marketplace_id)
		  ON ot.listing_id = keys.listing_id
		 AND ot.marketplace_id = keys.marketplace_id
		WHERE ot.user_id = $1`,
		userID,
		listingIDs,
		marketplaceIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	threads, err := scanOutreachThreadRows(rows)
	if err != nil {
		return nil, err
	}
	result := make(map[ListingKey]OutreachThread, len(threads))
	for _, t := range threads {
		result[ListingKey{ListingID: t.ListingID, MarketplaceID: t.MarketplaceID}] = t
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// SQLiteStore implementation
// ---------------------------------------------------------------------------

// UpsertThreadOnSent — SQLite implementation.
// SQLite does not support RETURNING with ON CONFLICT in older versions, so we
// use an INSERT OR REPLACE pattern followed by a SELECT.
func (s *SQLiteStore) UpsertThreadOnSent(ctx context.Context, t OutreachThread) (OutreachThread, error) {
	now := time.Now().UTC()
	var missionID *int64
	if t.MissionID != nil && *t.MissionID > 0 {
		missionID = t.MissionID
	}

	// Check if a thread already exists.
	existing, err := s.GetThreadForListing(ctx, t.UserID, t.ListingID, t.MarketplaceID)
	if err != nil {
		return OutreachThread{}, err
	}

	if existing != nil && existing.State == "replied" {
		// Re-sending after reply does not reset state — return existing row unchanged.
		return *existing, nil
	}

	if existing != nil {
		// Update draft fields on re-send (non-replied thread).
		_, err = s.db.ExecContext(ctx, `
			UPDATE outreach_threads
			SET draft_text   = ?,
			    draft_shape  = ?,
			    draft_lang   = ?,
			    mission_id   = ?,
			    sent_at      = ?,
			    updated_at   = ?
			WHERE user_id = ?
			  AND listing_id = ?
			  AND marketplace_id = ?`,
			t.DraftText, t.DraftShape, t.DraftLang, missionID,
			now, now,
			t.UserID, t.ListingID, t.MarketplaceID,
		)
		if err != nil {
			return OutreachThread{}, err
		}
	} else {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO outreach_threads
				(user_id, listing_id, marketplace_id, mission_id,
				 draft_text, draft_shape, draft_lang,
				 sent_at, state, last_state_transition_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'awaiting_reply', ?, ?, ?)`,
			t.UserID, t.ListingID, t.MarketplaceID, missionID,
			t.DraftText, t.DraftShape, t.DraftLang,
			now, now, now, now,
		)
		if err != nil {
			return OutreachThread{}, err
		}
	}

	updated, err := s.GetThreadForListing(ctx, t.UserID, t.ListingID, t.MarketplaceID)
	if err != nil {
		return OutreachThread{}, err
	}
	if updated == nil {
		return OutreachThread{}, fmt.Errorf("outreach thread not found after upsert")
	}
	return *updated, nil
}

// MarkReplied — SQLite implementation.
func (s *SQLiteStore) MarkReplied(ctx context.Context, userID, listingID, marketplaceID, replyText string) (OutreachThread, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE outreach_threads
		SET state                    = 'replied',
		    replied_at               = ?,
		    reply_text               = ?,
		    last_state_transition_at = ?,
		    updated_at               = ?
		WHERE user_id = ?
		  AND listing_id = ?
		  AND marketplace_id = ?`,
		now, replyText, now, now,
		userID, listingID, marketplaceID,
	)
	if err != nil {
		return OutreachThread{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return OutreachThread{}, ErrOutreachThreadNotFound
	}
	updated, err := s.GetThreadForListing(ctx, userID, listingID, marketplaceID)
	if err != nil {
		return OutreachThread{}, err
	}
	if updated == nil {
		return OutreachThread{}, ErrOutreachThreadNotFound
	}
	return *updated, nil
}

// ListThreadsByUser — SQLite implementation.
func (s *SQLiteStore) ListThreadsByUser(ctx context.Context, userID string, missionID *int64) ([]OutreachThread, error) {
	query := `SELECT ` + sqliteOutreachSelectColumns + `
		FROM outreach_threads
		WHERE user_id = ?`
	args := []any{userID}
	if missionID != nil && *missionID > 0 {
		query += ` AND mission_id = ?`
		args = append(args, *missionID)
	}
	query += ` ORDER BY last_state_transition_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutreachThreadRowsSQLite(rows)
}

// GetThreadForListing — SQLite implementation.
func (s *SQLiteStore) GetThreadForListing(ctx context.Context, userID, listingID, marketplaceID string) (*OutreachThread, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+sqliteOutreachSelectColumns+`
		FROM outreach_threads
		WHERE user_id = ?
		  AND listing_id = ?
		  AND marketplace_id = ?`,
		userID, listingID, marketplaceID,
	)
	t, err := scanOutreachThreadSQLite(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TransitionStaleThreads — SQLite implementation.
func (s *SQLiteStore) TransitionStaleThreads(ctx context.Context, cutoff time.Duration) (int64, error) {
	threshold := time.Now().UTC().Add(-cutoff)
	result, err := s.db.ExecContext(ctx, `
		UPDATE outreach_threads
		SET state                    = 'stale',
		    last_state_transition_at = CURRENT_TIMESTAMP,
		    updated_at               = CURRENT_TIMESTAMP
		WHERE state = 'awaiting_reply'
		  AND last_state_transition_at < ?`,
		threshold,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListThreadStatesForListings — SQLite implementation.
// SQLite does not have unnest; we build an IN clause manually.
func (s *SQLiteStore) ListThreadStatesForListings(ctx context.Context, userID string, listingKeys []ListingKey) (map[ListingKey]OutreachThread, error) {
	if len(listingKeys) == 0 {
		return map[ListingKey]OutreachThread{}, nil
	}

	// Build: WHERE user_id = ? AND (listing_id, marketplace_id) IN ((?,?),(?,?)...)
	// SQLite does not support row-value IN, so we use OR expansion.
	query := `SELECT ` + sqliteOutreachSelectColumns + `
		FROM outreach_threads
		WHERE user_id = ? AND (`
	args := []any{userID}
	for i, k := range listingKeys {
		if i > 0 {
			query += ` OR `
		}
		query += `(listing_id = ? AND marketplace_id = ?)`
		args = append(args, k.ListingID, k.MarketplaceID)
	}
	query += `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	threads, err := scanOutreachThreadRowsSQLite(rows)
	if err != nil {
		return nil, err
	}
	result := make(map[ListingKey]OutreachThread, len(threads))
	for _, t := range threads {
		result[ListingKey{ListingID: t.ListingID, MarketplaceID: t.MarketplaceID}] = t
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// SQLite scanning helpers (column names identical, no $N placeholders)
// ---------------------------------------------------------------------------

// sqliteOutreachSelectColumns is identical in content to outreachSelectColumns
// but included explicitly for clarity and to avoid any future postgres-only
// syntax leaking into the SQLite path.
const sqliteOutreachSelectColumns = `
	id, user_id, listing_id, marketplace_id, mission_id,
	draft_text, draft_shape, draft_lang,
	sent_at, replied_at, reply_text,
	state, last_state_transition_at, created_at, updated_at`

func scanOutreachThreadSQLite(row interface {
	Scan(dest ...any) error
}) (OutreachThread, error) {
	return scanOutreachThread(row)
}

// scanOutreachThreadRows reads all rows from a Postgres *sql.Rows query.
func scanOutreachThreadRows(rows *sql.Rows) ([]OutreachThread, error) {
	var threads []OutreachThread
	for rows.Next() {
		t, err := scanOutreachThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return threads, nil
}

// scanOutreachThreadRowsSQLite reads all rows from a SQLite *sql.Rows query.
func scanOutreachThreadRowsSQLite(rows *sql.Rows) ([]OutreachThread, error) {
	return scanOutreachThreadRows(rows)
}

// ---------------------------------------------------------------------------
// SQLite schema migration
// ---------------------------------------------------------------------------

// migrateOutreachThreadsSQLite adds the outreach_threads table to the SQLite
// schema. It is called from the existing migrate() function.
func migrateOutreachThreadsSQLite(db *sql.DB) {
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS outreach_threads (
			id                          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id                     TEXT NOT NULL,
			listing_id                  TEXT NOT NULL,
			marketplace_id              TEXT NOT NULL,
			mission_id                  INTEGER,
			draft_text                  TEXT NOT NULL DEFAULT '',
			draft_shape                 TEXT NOT NULL DEFAULT '',
			draft_lang                  TEXT NOT NULL DEFAULT '',
			sent_at                     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			replied_at                  DATETIME,
			reply_text                  TEXT,
			state                       TEXT NOT NULL DEFAULT 'awaiting_reply',
			last_state_transition_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_id, listing_id, marketplace_id)
		)
	`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_outreach_threads_user_state ON outreach_threads (user_id, state)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_outreach_threads_mission ON outreach_threads (mission_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_outreach_threads_last_transition ON outreach_threads (last_state_transition_at)`)
}

// ---------------------------------------------------------------------------
// PostgresStore migration
// ---------------------------------------------------------------------------

// migrateOutreachThreadsPostgres is now a no-op stub.
// W19-27: outreach_threads is created by migration file 000010 via the
// golang-migrate runner (runner.go). The inline CREATE TABLE statements have
// been removed per the Decision Log 2026-04-28 schema source-of-truth call.
//
// W19-26: function signature changed to return error for uniform error propagation.
//
// SQLite path (migrateOutreachThreadsSQLite) is unchanged — dev/test only.
func migrateOutreachThreadsPostgres(ctx context.Context, db *sql.DB) error {
	_ = ctx
	_ = db
	return nil
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrOutreachThreadNotFound is returned when a thread lookup finds no row.
var ErrOutreachThreadNotFound = fmt.Errorf("outreach thread not found")

// ErrOutreachThreadAlreadyReplied is returned when trying to reply to a thread
// that is already in the replied state.
var ErrOutreachThreadAlreadyReplied = fmt.Errorf("thread already replied")
