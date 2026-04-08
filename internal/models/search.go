package models

import "time"

type SearchSpec struct {
	ID              int64
	UserID          string
	ProfileID       int64
	Name            string
	Query           string
	MarketplaceID   string
	CategoryID      int
	MaxPrice        int
	MinPrice        int
	Condition       []string
	OfferPercentage int
	AutoMessage     bool
	MessageTemplate string
	Attributes      map[string]string
	Enabled         bool
	CheckInterval   time.Duration
}
