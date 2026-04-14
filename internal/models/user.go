package models

import "time"

type User struct {
	ID                 string
	Email              string
	PasswordHash       string
	Name               string
	Tier               string
	IsAdmin            bool
	StripeCustomer     string
	CountryCode        string
	Region             string
	City               string
	PostalCode         string
	PreferredRadiusKm  int
	CrossBorderEnabled bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AuthIdentity struct {
	ID              int64
	UserID          string
	Provider        string
	ProviderSubject string
	Email           string
	EmailVerified   bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AIUsageEntry records a single LLM API call for cost/usage tracking.
type AIUsageEntry struct {
	ID               int64
	UserID           string
	CallType         string // "reasoner", "generator", "brief_parser", "compare", "draft"
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMs        int
	Success          bool
	ErrorMsg         string
	CreatedAt        time.Time
}

// AIUsageStats holds aggregated usage data for admin dashboards.
type AIUsageStats struct {
	TotalCalls       int
	TotalTokens      int
	TotalPrompt      int
	TotalCompletion  int
	FailedCalls      int
	EstimatedCostUSD float64
}

// AdminUserSummary is a user record augmented with usage stats for the admin view.
type AdminUserSummary struct {
	User
	MissionCount int
	SearchCount  int
	AICallCount  int
	AITokens     int
}

type SearchOpsStats struct {
	TotalRuns                   int            `json:"total_runs"`
	TotalResultsFound           int            `json:"total_results_found"`
	TotalNewListings            int            `json:"total_new_listings"`
	TotalDealHits               int            `json:"total_deal_hits"`
	TotalThrottled              int            `json:"total_throttled"`
	SearchesAvoidedByScoping    int            `json:"searches_avoided_by_scoping"`
	AverageQueueWaitMs          int            `json:"average_queue_wait_ms"`
	AverageMissionFreshnessMins int            `json:"average_mission_freshness_mins"`
	ByPlan                      map[string]int `json:"by_plan"`
	ByCountry                   map[string]int `json:"by_country"`
	ByMarketplace               map[string]int `json:"by_marketplace"`
}
