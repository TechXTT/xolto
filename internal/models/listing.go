package models

import "time"

type Listing struct {
	MarketplaceID string
	CanonicalID   string
	ItemID        string
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

type ShoppingProfile struct {
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
	Active             bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Recommendation struct {
	Listing        Listing
	Scored         ScoredListing
	Profile        ShoppingProfile
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
	DraftProfile     *ShoppingProfile
	LastAssistantMsg string
	UpdatedAt        time.Time
}

type AssistantReply struct {
	Message         string
	Expecting       bool
	Intent          ConversationIntent
	Profile         *ShoppingProfile
	Recommendations []Recommendation
}

type ShortlistEntry struct {
	ID                  int64
	UserID              string
	ProfileID           int64
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
