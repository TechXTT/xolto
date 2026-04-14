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
	Score      float64
	FairPrice  int // cents
	OfferPrice int // cents
	Confidence float64
	Reason     string
	RiskFlags  []string
	Feedback   string // "", "approved", "dismissed"
}

type Seller struct {
	ID   string
	Name string
}

type Location struct {
	City     string
	Distance int // meters from configured zip code
}

type ScoredListing struct {
	Listing         Listing
	Score           float64
	OfferPrice      int // cents
	FairPrice       int // cents
	MarketAverage   int // cents
	Confidence      float64
	Reason          string
	ReasoningSource string
	SearchAdvice    string
	ComparableDeals []ComparableDeal
	RiskFlags       []string
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
