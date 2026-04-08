package store

import "github.com/TechXTT/marktbot/internal/models"

type Reader interface {
	GetMarketAverage(query string, categoryID int, minSamples int) (int, bool, error)
	GetComparableDeals(userID, query, excludeItemID string, limit int) ([]models.ComparableDeal, error)
	GetActiveShoppingProfile(userID string) (*models.ShoppingProfile, error)
	GetShortlist(userID string) ([]models.ShortlistEntry, error)
	GetShortlistEntry(userID, itemID string) (*models.ShortlistEntry, error)
	GetAssistantSession(userID string) (*models.AssistantSession, error)
	IsNew(userID, itemID string) (bool, error)
	GetListingScore(userID, itemID string) (float64, bool, error)
	WasOffered(userID, itemID string) (bool, error)
	GetUserByEmail(email string) (*models.User, error)
	GetUserByID(id string) (*models.User, error)
	GetSearchConfigs(userID string) ([]models.SearchSpec, error)
	GetAllEnabledSearchConfigs() ([]models.SearchSpec, error)
	CountSearchConfigs(userID string) (int, error)
	ListRecentListings(userID string, limit int) ([]models.Listing, error)
	ListActionDrafts(userID string) ([]models.ActionDraft, error)
}

type Writer interface {
	UpsertShoppingProfile(profile models.ShoppingProfile) (int64, error)
	SaveShortlistEntry(entry models.ShortlistEntry) error
	SaveConversationArtifact(userID string, intent models.ConversationIntent, input, output string) error
	SaveActionDraft(draft models.ActionDraft) error
	SaveAssistantSession(session models.AssistantSession) error
	ClearAssistantSession(userID string) error
	SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error
	RecordPrice(query string, categoryID int, price int) error
	MarkOffered(userID, itemID string) error
	CreateUser(email, hash, name string) (string, error)
	CreateSearchConfig(spec models.SearchSpec) (int64, error)
	UpdateSearchConfig(spec models.SearchSpec) error
	DeleteSearchConfig(id int64, userID string) error
	UpdateUserTier(userID, tier string) error
	UpdateStripeCustomer(userID, customerID string) error
	UpdateUserTierByStripeCustomer(customerID, tier string) error
	RecordStripeEvent(eventID string) error
}

type Store interface {
	Reader
	Writer
}
