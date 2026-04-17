package models

import "time"

type Listing struct {
	MarketplaceID string
	CanonicalID   string
	ItemID        string
	ProfileID     int64
	Title         string
	Description   string
	Price         int    // cents
	PriceType     string // "fixed", "negotiable", "bidding", "free", "see-description", "exchange", "reserved", "fast-bid"
	Condition     string
	Seller        Seller
	Location      Location
	Date          time.Time
	URL           string
	ImageURLs     []string
	CategoryID    int
	Attributes    map[string]string // condition, brand, etc.
	// Analysis fields: zero-value when listing comes from a marketplace search;
	// populated when loaded from the store (ListRecentListings).
	Score                   float64
	FairPrice               int // cents
	OfferPrice              int // cents
	Confidence              float64
	Reason                  string
	RiskFlags               []string
	RecommendedAction       string // one of: buy | negotiate | ask_seller | skip
	ComparablesCount         int    // number of comparable deals used by the scorer
	ComparablesMedianAgeDays int    // median age of comparables in days; 0 if none
	Feedback                 string // "", "approved", "dismissed"
	// CurrencyStatus describes how the listing price was normalised from the
	// marketplace's native currency into EUR cents. Values (XOL-33):
	//   "bgn_native"        — offer was quoted in EUR; stored as-is × 100
	//   "converted_from_eur"— offer was quoted in BGN; divided by 1.95583 peg
	//   "unknown"           — currency field missing or unrecognised; BGN fallback used
	//   ""                  — non-OLX marketplace or field not yet populated
	CurrencyStatus string
}

type Seller struct {
	ID   string
	Name string
}

type Location struct {
	City     string
	Distance int // meters from configured zip code
}

// MustHaveMatch records how well a single mission must-have is satisfied by a
// specific listing.
//
// Status enum (exhaustive — only these three values are emitted):
//
//	"met"     — listing demonstrably satisfies the must-have.
//	"missed"  — listing demonstrably violates the must-have.
//	"unknown" — listing description/metadata is silent on the must-have.
//
// When in doubt, "unknown" is the safe default. Over-claiming "met" is the
// primary failure mode (false trust); false "missed" is the secondary failure
// mode (false exclusion). Only emit "met" or "missed" when there is clear
// textual evidence in the listing.
type MustHaveMatch struct {
	Text   string `json:"Text"`
	Status string `json:"Status"` // "met" | "missed" | "unknown"
}

// ScoredListing is the output of the scorer for a single listing.
//
// RecommendedAction is one of the four action-verdict enum values:
//
//	"buy"        — strong signal to purchase at asking price or below
//	"negotiate"  — asking price is above fair but within negotiable range
//	"ask_seller" — evidence is thin or signals are missing; seek clarification
//	"skip"       — clear disqualifier (overpriced, red flags, or condition issue)
//
// RiskFlags is an orthogonal set of stable snake_case trust-signal keys.
// Keys are sourced from computeRiskFlags and may be empty. Dash renders these
// as small chips alongside the action verdict; they do not affect the verdict
// itself.
//
// Valid RiskFlags keys (non-exhaustive; stable contract):
//
//	"anomaly_price"         — asking price is suspiciously low vs fair value
//	"vague_condition"       — listing contains "as-is", "untested", etc.
//	"unclear_bundle"        — bundle/lot listing with unclear item scope
//	"no_model_id"           — electronics listing with no model number in title
//	"missing_key_photos"    — electronics listing with fewer than 3 photos
//	"no_battery_health"     — phone/laptop listing with no battery health signal
//	"refurbished_ambiguity" — refurb listing without grading or warranty signal
//
// MustHaves holds one MustHaveMatch per must-have defined in the associated
// mission, in source order (never alphabetised). An empty must-have mission
// yields an empty slice (never nil) so callers can iterate without nil guards.
type ScoredListing struct {
	Listing           Listing
	Score             float64
	OfferPrice        int // cents
	FairPrice         int // cents
	MarketAverage     int // cents
	Confidence        float64
	Reason            string
	ReasoningSource   string
	SearchAdvice      string
	ComparableDeals          []ComparableDeal
	RiskFlags                []string
	RecommendedAction        string // one of: buy | negotiate | ask_seller | skip
	ComparablesCount         int    // number of comparable deals used by the scorer
	ComparablesMedianAgeDays int    // median age of comparables in days; 0 if none
	MustHaves                []MustHaveMatch
}

type PricePoint struct {
	Query     string
	Price     int // cents
	Timestamp time.Time
}

type ComparableDeal struct {
	ItemID      string
	Title       string
	Price       int
	Score       float64
	Similarity  float64
	LastSeen    time.Time
	MatchReason string
}

type DealAnalysis struct {
	FairPrice       int
	Confidence      float64
	Reason          string
	Source          string
	ComparableDeals []ComparableDeal
	SearchAdvice    string
	Relevant        bool // false means the AI judged this listing unrelated to the search
}

type RecommendationLabel string

const (
	RecommendationBuyNow       RecommendationLabel = "buy_now"
	RecommendationWatch        RecommendationLabel = "worth_watching"
	RecommendationAskQuestions RecommendationLabel = "ask_questions"
	RecommendationSkip         RecommendationLabel = "skip"
)

type ConversationIntent string

const (
	IntentCreateBrief    ConversationIntent = "create_brief"
	IntentRefineBrief    ConversationIntent = "refine_brief"
	IntentShowMatches    ConversationIntent = "show_matches"
	IntentExplainListing ConversationIntent = "explain_listing"
	IntentCompare        ConversationIntent = "compare_listings"
	IntentShortlist      ConversationIntent = "manage_shortlist"
	IntentDraftMessage   ConversationIntent = "draft_message"
)

type Mission struct {
	ID                 int64
	UserID             string
	Name               string
	TargetQuery        string
	CategoryID         int
	BudgetMax          int
	BudgetStretch      int
	PreferredCondition []string
	RequiredFeatures   []string
	NiceToHave         []string
	RiskTolerance      string
	ZipCode            string
	Distance           int
	SearchQueries      []string
	Status             string
	Urgency            string
	AvoidFlags         []string
	TravelRadius       int
	CountryCode        string
	Region             string
	City               string
	PostalCode         string
	CrossBorderEnabled bool
	MarketplaceScope   []string
	Category           string
	MatchCount         int
	LastMatchAt        time.Time
	Active             bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ShoppingProfile is kept as an alias for backward compatibility while
// the product vocabulary transitions to "Mission".
type ShoppingProfile = Mission

type Recommendation struct {
	Listing        Listing
	Scored         ScoredListing
	Mission        Mission
	Label          RecommendationLabel
	FitScore       float64
	Verdict        string
	Concerns       []string
	NextQuestions  []string
	SuggestedOffer int
}

type AssistantSession struct {
	UserID           string
	PendingIntent    ConversationIntent
	PendingQuestion  string
	DraftMission     *Mission
	LastAssistantMsg string
	UpdatedAt        time.Time
}

type AssistantReply struct {
	Message         string
	Expecting       bool
	Intent          ConversationIntent
	Mission         *Mission
	Recommendations []Recommendation
}

type ShortlistEntry struct {
	ID                  int64
	UserID              string
	MissionID           int64
	ItemID              string
	Title               string
	URL                 string
	RecommendationLabel RecommendationLabel
	RecommendationScore float64
	AskPrice            int
	FairPrice           int
	Verdict             string
	Concerns            []string
	SuggestedQuestions  []string
	Status              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ActionDraft struct {
	ID         int64
	UserID     string
	ItemID     string
	ActionType string
	Content    string
	Status     string
	CreatedAt  time.Time
}

type SearchRunLog struct {
	ID              int64
	SearchConfigID  int64
	UserID          string
	MissionID       int64
	Plan            string
	MarketplaceID   string
	CountryCode     string
	StartedAt       time.Time
	FinishedAt      time.Time
	QueueWaitMs     int
	Priority        int
	Status          string
	ResultsFound    int
	NewListings     int
	DealHits        int
	Throttled       bool
	ErrorCode       string
	SearchesAvoided int
}

// MatchesFilter holds the server-side filter and sort parameters for
// GET /matches (Phase 3). All fields are optional — zero values mean
// "no filter / default".
//
// Sort modes:
//
//	"newest"     -> last_seen DESC, item_id ASC  (default, matches Phase 1)
//	"score"      -> score DESC, item_id ASC
//	"price_asc"  -> offer_price ASC  NULLS LAST, item_id ASC
//	"price_desc" -> offer_price DESC NULLS LAST, item_id ASC
//
// Market/Condition must be stored-canonical values (e.g. "marktplaats",
// "vinted_nl", "olxbg", "vinted_dk" for market; "new", "like_new", "good",
// "fair" for condition). The handler normalises the wire vocabulary before
// constructing this struct.
type MatchesFilter struct {
	// Sort is one of "newest", "score", "price_asc", "price_desc".
	// Empty string is equivalent to "newest".
	Sort string
	// Market filters by stored marketplace_id. Empty string means "all".
	Market string
	// Condition filters by stored condition value. Empty string means "all".
	Condition string
	// MinScore, when > 0, excludes listings with score < MinScore.
	// Range: 0..10 inclusive; 0 means no minimum.
	MinScore int
}
