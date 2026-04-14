package models

import (
	"strings"
	"time"
)

type UserRole string

const (
	UserRoleUser     UserRole = "user"
	UserRoleOwner    UserRole = "owner"
	UserRoleOperator UserRole = "operator"
	UserRoleAdmin    UserRole = "admin"
)

type User struct {
	ID                 string
	Email              string
	PasswordHash       string
	Name               string
	Tier               string
	Role               string
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
	MissionID        int64
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
	SuccessfulRuns              int            `json:"successful_runs"`
	FailedRuns                  int            `json:"failed_runs"`
	FailureRatePct              float64        `json:"failure_rate_pct"`
	TotalResultsFound           int            `json:"total_results_found"`
	TotalNewListings            int            `json:"total_new_listings"`
	TotalDealHits               int            `json:"total_deal_hits"`
	TotalThrottled              int            `json:"total_throttled"`
	SearchesAvoidedByScoping    int            `json:"searches_avoided_by_scoping"`
	AverageQueueWaitMs          int            `json:"average_queue_wait_ms"`
	AverageMissionFreshnessMins int            `json:"average_mission_freshness_mins"`
	ByStatus                    map[string]int `json:"by_status"`
	ByPlan                      map[string]int `json:"by_plan"`
	ByCountry                   map[string]int `json:"by_country"`
	ByMarketplace               map[string]int `json:"by_marketplace"`
}

type AdminSearchRunFilter struct {
	Days          int
	Status        string
	MarketplaceID string
	CountryCode   string
	UserID        string
	Limit         int
}

type AdminSearchRun struct {
	ID              int64     `json:"id"`
	SearchConfigID  int64     `json:"search_config_id"`
	SearchName      string    `json:"search_name"`
	UserID          string    `json:"user_id"`
	UserEmail       string    `json:"user_email"`
	MissionID       int64     `json:"mission_id"`
	MissionName     string    `json:"mission_name"`
	Plan            string    `json:"plan"`
	MarketplaceID   string    `json:"marketplace_id"`
	CountryCode     string    `json:"country_code"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	QueueWaitMs     int       `json:"queue_wait_ms"`
	Priority        int       `json:"priority"`
	Status          string    `json:"status"`
	ResultsFound    int       `json:"results_found"`
	NewListings     int       `json:"new_listings"`
	DealHits        int       `json:"deal_hits"`
	Throttled       bool      `json:"throttled"`
	ErrorCode       string    `json:"error_code"`
	SearchesAvoided int       `json:"searches_avoided"`
}

type AdminAuditLogEntry struct {
	ID          int64     `json:"id"`
	ActorUserID string    `json:"actor_user_id"`
	ActorRole   string    `json:"actor_role"`
	RequestID   string    `json:"request_id"`
	Action      string    `json:"action"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	BeforeJSON  string    `json:"before_json"`
	AfterJSON   string    `json:"after_json"`
	CreatedAt   time.Time `json:"created_at"`
}

func NormalizeUserRole(role string) string {
	switch UserRole(strings.ToLower(strings.TrimSpace(role))) {
	case UserRoleUser:
		return string(UserRoleUser)
	case UserRoleOwner:
		return string(UserRoleOwner)
	case UserRoleOperator:
		return string(UserRoleOperator)
	case UserRoleAdmin:
		return string(UserRoleAdmin)
	default:
		return ""
	}
}

func EffectiveUserRole(user User) string {
	role := NormalizeUserRole(user.Role)
	if role != "" {
		return role
	}
	if user.IsAdmin {
		return string(UserRoleAdmin)
	}
	return string(UserRoleUser)
}

func IsTeamRole(role string) bool {
	switch NormalizeUserRole(role) {
	case string(UserRoleOwner), string(UserRoleOperator), string(UserRoleAdmin):
		return true
	default:
		return false
	}
}

func HasOperatorAccess(user User) bool {
	switch EffectiveUserRole(user) {
	case string(UserRoleOwner), string(UserRoleOperator), string(UserRoleAdmin):
		return true
	default:
		return false
	}
}

func HasOwnerAccess(user User) bool {
	return EffectiveUserRole(user) == string(UserRoleOwner)
}
