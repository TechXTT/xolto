package models

import "time"

type User struct {
	ID             string
	Email          string
	PasswordHash   string
	Name           string
	Tier           string
	IsAdmin        bool
	StripeCustomer string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
