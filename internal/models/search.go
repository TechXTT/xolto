package models

import "time"

type SearchSpec struct {
	ID                   int64
	UserID               string
	ProfileID            int64
	Name                 string
	Query                string
	MarketplaceID        string
	CountryCode          string
	City                 string
	PostalCode           string
	RadiusKm             int
	CategoryID           int
	MaxPrice             int
	MinPrice             int
	Condition            []string
	OfferPercentage      int
	AutoMessage          bool
	MessageTemplate      string
	Attributes           map[string]string
	Enabled              bool
	CheckInterval        time.Duration
	PriorityClass        int
	NextRunAt            time.Time
	LastRunAt            time.Time
	LastSignalAt         time.Time
	LastErrorAt          time.Time
	LastResultCount      int
	ConsecutiveEmptyRuns int
	ConsecutiveFailures  int
}
