package store

// VAL-1a: calibration persistence — scoring_events table.
//
// ScoringEvent is written at worker-time when the scorer processes a listing.
// Write failures are logged and swallowed; scoring is on the critical path
// and calibration persistence is not.
//
// The CalibrationStore interface exposes:
//   - WriteScoringEvent   — insert one row (best-effort)
//   - GetCalibrationSummary — aggregated slices over a time window

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ScoringEvent mirrors one row in the scoring_events table.
//
// AIPath labels the originating path for VAL-1 calibration filtering
// (W19-23). "ai" is the historical default and the value used for real
// LLM calls. "heuristic_fallback" is set by the scorer when the global
// $3/24h AI-spend cap forced the heuristic-only path. The default value
// from the migration is "ai" so legacy rows aggregate normally.
type ScoringEvent struct {
	ID            int64
	ListingID     string
	Marketplace   string
	MissionID     *int64
	Score         float64
	Verdict       string
	Confidence    float64
	Contributions map[string]float64
	ScorerVersion string
	CreatedAt     time.Time
	// AIPath is one of "ai" | "heuristic_fallback". Defaults to "ai" when
	// empty (back-compat). See models.AIPath* constants for the canonical
	// values.
	AIPath string
}

// ScorerVersionV1 is the version tag for the current scoring formula.
// Increment when the scoring algorithm changes materially so historical
// calibration data can be attributed to the correct formula.
const ScorerVersionV1 = "v1"

// CalibrationStore is the interface for scoring_events persistence.
type CalibrationStore interface {
	WriteScoringEvent(ctx context.Context, e ScoringEvent) error
	GetCalibrationSummary(ctx context.Context, q CalibrationQuery) (CalibrationSummary, error)
}

// ---------------------------------------------------------------------------
// Query / Summary types
// ---------------------------------------------------------------------------

// CalibrationQuery carries the filter parameters for GET /internal/calibration/summary.
type CalibrationQuery struct {
	// Window is the lookback duration (e.g. 7*24*time.Hour).
	// Defaults to 7 days when zero.
	Window time.Duration
	// Marketplace filters by marketplace column. Empty string = all.
	Marketplace string
	// Category is reserved for future use; not yet stored on scoring_events.
	// Accepted by the handler but currently ignored in queries.
	Category string
	// IncludeHeuristicFallback controls whether scoring_events rows tagged
	// ai_path = "heuristic_fallback" are included in the aggregation. The
	// default (false) excludes those rows so VAL-0 verdict-correctness
	// metrics (verdict-to-action >= 50%, verdict-changed >= 30%) are not
	// silently shifted by W19-23 cap-fire incidents. Ops debugging
	// (e.g. measuring how much heuristic data was emitted during a
	// cap-fire) can opt back in via include_heuristic_fallback=true on
	// the API.
	IncludeHeuristicFallback bool
}

// CalibrationSummary is the aggregated response body for GET /internal/calibration/summary.
type CalibrationSummary struct {
	WindowDays          int                          `json:"window_days"`
	Marketplace         string                       `json:"marketplace"`
	TotalEvents         int                          `json:"total_events"`
	VerdictCounts       map[string]int               `json:"verdict_counts"`
	ConfidenceHistogram map[string]map[string]int    `json:"confidence_histogram"`
	FairPriceDelta      map[string]map[string]int    `json:"fair_price_delta"`
	OutcomeAttribution  map[string]int               `json:"outcome_attribution"`
}

// ---------------------------------------------------------------------------
// PostgresStore implementation
// ---------------------------------------------------------------------------

// WriteScoringEvent inserts one row into scoring_events. Best-effort: the
// function logs and returns the error but the caller (worker) swallows it.
func (s *PostgresStore) WriteScoringEvent(ctx context.Context, e ScoringEvent) error {
	raw, err := json.Marshal(e.Contributions)
	if err != nil {
		return fmt.Errorf("marshalling contributions: %w", err)
	}

	var missionID *int64
	if e.MissionID != nil && *e.MissionID > 0 {
		missionID = e.MissionID
	}

	aiPath := e.AIPath
	if aiPath == "" {
		aiPath = "ai" // back-compat with rows written before W19-23
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scoring_events
			(listing_id, marketplace, mission_id, score, verdict, confidence,
			 contributions, scorer_version, ai_path, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		e.ListingID,
		e.Marketplace,
		missionID,
		e.Score,
		e.Verdict,
		e.Confidence,
		raw,
		e.ScorerVersion,
		aiPath,
	)
	return err
}

// GetCalibrationSummary returns aggregated slices from scoring_events over the
// requested window. All aggregation happens in-process over a bounded row set
// (window default = 7d); the table is indexed on created_at DESC so the
// DB-side filter is efficient.
//
// fair_price_delta is derived from contributions["comparables"]:
//
//	comparables > 0 means listing.Price < reference_price  → buyer-friendly
//	comparables < 0 means listing.Price > reference_price  → seller-friendly
//	The raw delta in score-units is bucketed as:
//	  "very_underpriced" (comparables >= +2)
//	  "underpriced"      (+1 <= comparables < +2)
//	  "fair"             (-1 < comparables < +1)
//	  "overpriced"       (-2 < comparables <= -1)
//	  "very_overpriced"  (comparables <= -2)
//
// outcome_attribution joins to outreach_threads on listing_id to detect
// whether the listing was acted on; rows with no thread row count as "unknown".
// The outreach state values map to:
//
//	"won"  → listing was purchased
//	"lost" → user gave up
//	"sent" | "replied" → in-progress outreach
//	"none"             → thread exists but no outreach yet
//	"unknown"          → no outreach row for this listing_id
func (s *PostgresStore) GetCalibrationSummary(ctx context.Context, q CalibrationQuery) (CalibrationSummary, error) {
	window := q.Window
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	windowDays := int(window.Hours() / 24)
	if windowDays < 1 {
		windowDays = 1
	}

	since := time.Now().UTC().Add(-window)

	// Build WHERE clause.
	args := []any{since}
	where := "se.created_at >= $1"
	if q.Marketplace != "" {
		args = append(args, q.Marketplace)
		where += fmt.Sprintf(" AND se.marketplace = $%d", len(args))
	}
	// W19-23 VAL-1 contamination guard: by default exclude scoring_events
	// rows produced by the global AI-spend cap heuristic fallback, so
	// VAL-0 verdict-correctness metrics are not silently shifted by
	// cap-fire incidents. Ops debugging can opt back in.
	if !q.IncludeHeuristicFallback {
		where += " AND se.ai_path = 'ai'"
	}

	// Query: fetch scoring_events left-joined to outreach_threads for outcome.
	// We pull the columns needed for in-process aggregation.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			se.verdict,
			se.confidence,
			se.contributions,
			COALESCE(ot.state, 'unknown') AS outreach_state
		FROM scoring_events se
		LEFT JOIN outreach_threads ot ON ot.listing_id = se.listing_id
		WHERE `+where+`
		ORDER BY se.created_at DESC`,
		args...,
	)
	if err != nil {
		return CalibrationSummary{}, fmt.Errorf("querying scoring_events: %w", err)
	}
	defer rows.Close()

	summary := CalibrationSummary{
		WindowDays:          windowDays,
		Marketplace:         q.Marketplace,
		VerdictCounts:       make(map[string]int),
		ConfidenceHistogram: make(map[string]map[string]int),
		OutcomeAttribution:  make(map[string]int),
		FairPriceDelta:      make(map[string]map[string]int),
	}

	for rows.Next() {
		var verdict, outreachState string
		var confidence float64
		var contribsRaw []byte

		if err := rows.Scan(&verdict, &confidence, &contribsRaw, &outreachState); err != nil {
			return CalibrationSummary{}, fmt.Errorf("scanning scoring_events row: %w", err)
		}

		summary.TotalEvents++

		// Verdict counts.
		summary.VerdictCounts[verdict]++

		// Confidence histogram: bucket into 0.1 bands, keyed by verdict.
		confBucket := confidenceBucket(confidence)
		if summary.ConfidenceHistogram[verdict] == nil {
			summary.ConfidenceHistogram[verdict] = make(map[string]int)
		}
		summary.ConfidenceHistogram[verdict][confBucket]++

		// Fair-price delta: derived from contributions["comparables"].
		var contribs map[string]float64
		if len(contribsRaw) > 0 {
			if jsonErr := json.Unmarshal(contribsRaw, &contribs); jsonErr != nil {
				slog.Warn("calibration: failed to unmarshal contributions", "error", jsonErr)
			}
		}
		if contribs != nil {
			comp := contribs["comparables"]
			bucket := fairPriceDeltaBucket(comp)
			if summary.FairPriceDelta[verdict] == nil {
				summary.FairPriceDelta[verdict] = make(map[string]int)
			}
			summary.FairPriceDelta[verdict][bucket]++
		}

		// Outcome attribution: map outreach state.
		summary.OutcomeAttribution[outreachState]++
	}
	if err := rows.Err(); err != nil {
		return CalibrationSummary{}, fmt.Errorf("iterating scoring_events: %w", err)
	}

	return summary, nil
}

// ---------------------------------------------------------------------------
// SQLiteStore implementation
// ---------------------------------------------------------------------------

// WriteScoringEvent — SQLite implementation (dev / test path).
func (s *SQLiteStore) WriteScoringEvent(ctx context.Context, e ScoringEvent) error {
	raw, err := json.Marshal(e.Contributions)
	if err != nil {
		return fmt.Errorf("marshalling contributions: %w", err)
	}

	var missionID *int64
	if e.MissionID != nil && *e.MissionID > 0 {
		missionID = e.MissionID
	}

	aiPath := e.AIPath
	if aiPath == "" {
		aiPath = "ai"
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scoring_events
			(listing_id, marketplace, mission_id, score, verdict, confidence,
			 contributions, scorer_version, ai_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		e.ListingID,
		e.Marketplace,
		missionID,
		e.Score,
		e.Verdict,
		e.Confidence,
		raw,
		e.ScorerVersion,
		aiPath,
	)
	return err
}

// GetCalibrationSummary — SQLite implementation (dev / test path).
// Functionally equivalent to the Postgres version.
func (s *SQLiteStore) GetCalibrationSummary(ctx context.Context, q CalibrationQuery) (CalibrationSummary, error) {
	window := q.Window
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	windowDays := int(window.Hours() / 24)
	if windowDays < 1 {
		windowDays = 1
	}

	since := time.Now().UTC().Add(-window).Format("2006-01-02T15:04:05Z")

	args := []any{since}
	where := "se.created_at >= ?"
	if q.Marketplace != "" {
		args = append(args, q.Marketplace)
		where += " AND se.marketplace = ?"
	}
	// W19-23 VAL-1 contamination guard. See PostgresStore docs.
	if !q.IncludeHeuristicFallback {
		where += " AND se.ai_path = 'ai'"
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			se.verdict,
			se.confidence,
			se.contributions,
			COALESCE(ot.state, 'unknown') AS outreach_state
		FROM scoring_events se
		LEFT JOIN outreach_threads ot ON ot.listing_id = se.listing_id
		WHERE `+where+`
		ORDER BY se.created_at DESC`,
		args...,
	)
	if err != nil {
		return CalibrationSummary{}, fmt.Errorf("querying scoring_events: %w", err)
	}
	defer rows.Close()

	summary := CalibrationSummary{
		WindowDays:          windowDays,
		Marketplace:         q.Marketplace,
		VerdictCounts:       make(map[string]int),
		ConfidenceHistogram: make(map[string]map[string]int),
		OutcomeAttribution:  make(map[string]int),
		FairPriceDelta:      make(map[string]map[string]int),
	}

	for rows.Next() {
		var verdict, outreachState string
		var confidence float64
		var contribsRaw []byte

		if err := rows.Scan(&verdict, &confidence, &contribsRaw, &outreachState); err != nil {
			return CalibrationSummary{}, fmt.Errorf("scanning scoring_events row: %w", err)
		}

		summary.TotalEvents++
		summary.VerdictCounts[verdict]++

		confBucket := confidenceBucket(confidence)
		if summary.ConfidenceHistogram[verdict] == nil {
			summary.ConfidenceHistogram[verdict] = make(map[string]int)
		}
		summary.ConfidenceHistogram[verdict][confBucket]++

		var contribs map[string]float64
		if len(contribsRaw) > 0 {
			if jsonErr := json.Unmarshal(contribsRaw, &contribs); jsonErr != nil {
				slog.Warn("calibration: failed to unmarshal contributions", "error", jsonErr)
			}
		}
		if contribs != nil {
			comp := contribs["comparables"]
			bucket := fairPriceDeltaBucket(comp)
			if summary.FairPriceDelta[verdict] == nil {
				summary.FairPriceDelta[verdict] = make(map[string]int)
			}
			summary.FairPriceDelta[verdict][bucket]++
		}

		summary.OutcomeAttribution[outreachState]++
	}
	if err := rows.Err(); err != nil {
		return CalibrationSummary{}, fmt.Errorf("iterating scoring_events: %w", err)
	}

	return summary, nil
}

// ---------------------------------------------------------------------------
// Migration helpers
// ---------------------------------------------------------------------------

// migratePostgresCalibration adds the scoring_events table to Postgres.
// Called from migratePostgres().
func migratePostgresCalibration(ctx context.Context, db *sql.DB) {
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scoring_events (
			id               BIGSERIAL PRIMARY KEY,
			listing_id       TEXT             NOT NULL,
			marketplace      TEXT             NOT NULL DEFAULT '',
			mission_id       BIGINT,
			score            DOUBLE PRECISION NOT NULL DEFAULT 0,
			verdict          TEXT             NOT NULL DEFAULT '',
			confidence       DOUBLE PRECISION NOT NULL DEFAULT 0,
			contributions    JSONB            NOT NULL DEFAULT '{}'::jsonb,
			scorer_version   TEXT             NOT NULL DEFAULT 'v1',
			ai_path          TEXT             NOT NULL DEFAULT 'ai',
			created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW()
		)`)
	// Idempotent column add for existing deployments — the dedicated
	// migration file (000015) runs once per env; this ALTER is a safety
	// net for embedded schema-bootstraps that don't go through the
	// migrations directory.
	_, _ = db.ExecContext(ctx, `ALTER TABLE scoring_events ADD COLUMN IF NOT EXISTS ai_path TEXT NOT NULL DEFAULT 'ai'`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_scoring_events_created_at ON scoring_events (created_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_scoring_events_marketplace ON scoring_events (marketplace, created_at DESC)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_scoring_events_listing_id ON scoring_events (listing_id)`)

	// W19-23: ai_budget_overrides audit log.
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_budget_overrides (
			id              BIGSERIAL PRIMARY KEY,
			set_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			new_cap_usd     DOUBLE PRECISION NOT NULL,
			reason          TEXT        NOT NULL DEFAULT '',
			set_by_user_id  TEXT        NOT NULL DEFAULT ''
		)`)
	_, _ = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_ai_budget_overrides_set_at ON ai_budget_overrides (set_at DESC)`)
}

// migrateCalibrationSQLite adds the scoring_events table to the SQLite schema.
func migrateCalibrationSQLite(db *sql.DB) {
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS scoring_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			listing_id      TEXT    NOT NULL,
			marketplace     TEXT    NOT NULL DEFAULT '',
			mission_id      INTEGER,
			score           REAL    NOT NULL DEFAULT 0,
			verdict         TEXT    NOT NULL DEFAULT '',
			confidence      REAL    NOT NULL DEFAULT 0,
			contributions   TEXT    NOT NULL DEFAULT '{}',
			scorer_version  TEXT    NOT NULL DEFAULT 'v1',
			ai_path         TEXT    NOT NULL DEFAULT 'ai',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	// SQLite doesn't support IF NOT EXISTS on ADD COLUMN, but the column
	// add is idempotent at the table-create level above; existing dev
	// databases need a manual ALTER (or recreate). The schemaHasColumn
	// helper protects against duplicate-column errors.
	if !sqliteHasColumn(db, "scoring_events", "ai_path") {
		_, _ = db.Exec(`ALTER TABLE scoring_events ADD COLUMN ai_path TEXT NOT NULL DEFAULT 'ai'`)
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_scoring_events_created_at ON scoring_events (created_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_scoring_events_marketplace ON scoring_events (marketplace, created_at)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_scoring_events_listing_id ON scoring_events (listing_id)`)

	// W19-23: ai_budget_overrides audit log.
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_budget_overrides (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			set_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			new_cap_usd     REAL NOT NULL,
			reason          TEXT NOT NULL DEFAULT '',
			set_by_user_id  TEXT NOT NULL DEFAULT ''
		)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_budget_overrides_set_at ON ai_budget_overrides (set_at DESC)`)
}

// sqliteHasColumn returns true when the column exists on the table.
// SQLite ALTER TABLE ADD COLUMN does NOT support IF NOT EXISTS, so we must
// query the schema first to make schema bootstrapping idempotent on dev
// databases that already had the table created without ai_path.
func sqliteHasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Bucketing helpers (shared between Postgres and SQLite paths)
// ---------------------------------------------------------------------------

// confidenceBucket returns a string key for a 0.1-wide confidence band.
// E.g. 0.72 → "0.7", 0.35 → "0.3". Values outside [0,1] are clamped.
//
// The bucket boundary is the lower bound of the band (open on right), so a
// confidence of exactly 0.70 falls into bucket "0.7", and 0.80 into "0.8".
func confidenceBucket(c float64) string {
	if c < 0 {
		c = 0
	}
	if c > 1 {
		c = 1
	}
	// Multiply by 10, truncate, divide back.
	band := int(c * 10)
	if band > 9 {
		band = 9 // 1.0 → "0.9" bucket
	}
	return fmt.Sprintf("%.1f", float64(band)*0.1)
}

// fairPriceDeltaBucket maps the "comparables" contribution delta into a
// descriptive bucket. The "comparables" component absorbs all
// score-unit shifts relative to the reference price:
//
//	very_underpriced : comparables >= +2.0  (price is well below fair value)
//	underpriced      : +1.0 <= comp < +2.0
//	fair             : -1.0 < comp < +1.0
//	overpriced       : -2.0 <= comp <= -1.0
//	very_overpriced  : comp < -2.0
//	unknown          : comparables key absent or NaN
//
// These thresholds map to the scoring formula where each 10% price deviation
// shifts the base score by 1.0 unit. A +2.0 bucket ≈ 20% below fair value.
func fairPriceDeltaBucket(comp float64) string {
	switch {
	case comp >= 2.0:
		return "very_underpriced"
	case comp >= 1.0:
		return "underpriced"
	case comp > -1.0:
		return "fair"
	case comp > -2.0:
		return "overpriced"
	default:
		return "very_overpriced"
	}
}
