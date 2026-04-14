package store

import (
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

type Reader interface {
	GetMarketAverage(query string, categoryID int, minSamples int) (int, bool, error)
	GetComparableDeals(userID, query, excludeItemID string, limit int) ([]models.ComparableDeal, error)
	GetApprovedComparables(userID string, missionID int64, limit int) ([]models.ComparableDeal, error)
	GetListingScoringState(userID, itemID string) (price int, reasoningSource string, found bool, err error)
	GetAIScoreCache(cacheKey string, promptVersion int) (score float64, reasoning string, found bool, err error)
	GetActiveMission(userID string) (*models.Mission, error)
	GetMission(id int64) (*models.Mission, error)
	ListMissions(userID string) ([]models.Mission, error)
	GetShortlist(userID string) ([]models.ShortlistEntry, error)
	GetShortlistEntry(userID, itemID string) (*models.ShortlistEntry, error)
	GetAssistantSession(userID string) (*models.AssistantSession, error)
	IsNew(userID, itemID string) (bool, error)
	GetListingScore(userID, itemID string) (float64, bool, error)
	WasOffered(userID, itemID string) (bool, error)
	GetUserByEmail(email string) (*models.User, error)
	GetUserByID(id string) (*models.User, error)
	GetUserByAuthIdentity(provider, subject string) (*models.User, error)
	ListUserAuthMethods(userID string) ([]string, error)
	GetSearchConfigs(userID string) ([]models.SearchSpec, error)
	GetSearchConfigByID(id int64) (*models.SearchSpec, error)
	GetAllEnabledSearchConfigs() ([]models.SearchSpec, error)
	CountSearchConfigs(userID string) (int, error)
	CountActiveMissions(userID string) (int, error)
	ListRecentListings(userID string, limit int, missionID int64) ([]models.Listing, error)
	ListActionDrafts(userID string) ([]models.ActionDraft, error)
	// Admin
	ListAllUsers() ([]models.AdminUserSummary, error)
	GetAIUsageStats(days int) (models.AIUsageStats, error)
	GetAIUsageTimeline(days int) ([]models.AIUsageEntry, error)
	GetUserAIUsageStats(userID string, days int) (models.AIUsageStats, error)
	GetSearchOpsStats(days int) (models.SearchOpsStats, error)
	ListSearchRuns(filter models.AdminSearchRunFilter) ([]models.AdminSearchRun, error)
	ListAdminAuditLog(limit int) ([]models.AdminAuditLogEntry, error)
	// Business
	GetBusinessOverview(days int) (models.BusinessOverview, error)
	ListBusinessSubscriptions(filter models.BusinessSubscriptionFilter) ([]models.BusinessSubscriptionRow, error)
	GetBusinessRevenue(days int) ([]models.BusinessRevenuePoint, error)
	GetBusinessFunnel(days int) (models.BusinessFunnel, error)
	GetBusinessCohorts(months int) ([]models.BusinessCohortRow, error)
	GetBusinessAlerts(days int) ([]models.BusinessAlert, error)
	GetStripeSubscriptionSnapshot(subscriptionID string) (*models.StripeSubscriptionSnapshot, error)
	ListUsersWithStripeCustomerIDs() ([]models.User, error)
	GetLatestBusinessReconcileRun() (*models.BillingReconcileRun, error)
}

type Writer interface {
	UpsertMission(mission models.Mission) (int64, error)
	UpdateMissionStatus(id int64, status string) error
	DeleteMission(id int64, userID string) error
	SaveShortlistEntry(entry models.ShortlistEntry) error
	SaveConversationArtifact(userID string, intent models.ConversationIntent, input, output string) error
	SaveActionDraft(draft models.ActionDraft) error
	SaveAssistantSession(session models.AssistantSession) error
	ClearAssistantSession(userID string) error
	SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error
	TouchListing(userID, itemID string) error
	SetAIScoreCache(cacheKey string, score float64, reasoning string, promptVersion int) error
	SetListingFeedback(userID, itemID, feedback string) error
	RecordPrice(query string, categoryID int, price int) error
	MarkOffered(userID, itemID string) error
	CreateUser(email, hash, name string) (string, error)
	UpdateUserProfile(user models.User) error
	UpsertUserAuthIdentity(identity models.AuthIdentity) error
	CreateSearchConfig(spec models.SearchSpec) (int64, error)
	UpdateSearchConfig(spec models.SearchSpec) error
	UpdateSearchRuntime(spec models.SearchSpec) error
	SetSearchEnabled(id int64, enabled bool) error
	SetSearchNextRunAt(id int64, nextRunAt time.Time) error
	DeleteSearchConfig(id int64, userID string) error
	UpdateUserTier(userID, tier string) error
	UpdateUserRole(userID, role string) error
	UpdateStripeCustomer(userID, customerID string) error
	UpdateUserTierByStripeCustomer(customerID, tier string) error
	RecordStripeEvent(eventID string) error
	UpsertStripeWebhookEvent(entry models.StripeWebhookEventLog) error
	UpsertStripeSubscriptionSnapshot(snapshot models.StripeSubscriptionSnapshot) error
	AppendStripeSubscriptionHistory(entry models.StripeSubscriptionHistoryEntry) error
	UpsertStripeInvoiceSummary(invoice models.StripeInvoiceSummary) error
	RecordStripeMutation(entry models.StripeMutationLog) error
	StartBillingReconcileRun(run models.BillingReconcileRun) (int64, error)
	FinishBillingReconcileRun(id int64, status, summaryJSON, errorJSON string) error
	RecordSearchRun(entry models.SearchRunLog) error
	// Admin
	RecordAIUsage(entry models.AIUsageEntry) error
	SetUserAdmin(userID string, isAdmin bool) error
	RecordAdminAuditLog(entry models.AdminAuditLogEntry) error
}

type Store interface {
	Reader
	Writer
}
