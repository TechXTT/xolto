package billing

type Limits struct {
	MaxSearches          int
	MaxMarketplaces      int
	MinCheckIntervalMins int
	AIEnabled            bool
	AutoMessagingPerHour int
	MaxShortlistEntries  int
}

func LimitsFor(tier string) Limits {
	switch tier {
	case "pro":
		return Limits{MaxSearches: 10, MaxMarketplaces: 2, MinCheckIntervalMins: 5, AIEnabled: true, AutoMessagingPerHour: 5, MaxShortlistEntries: 100}
	case "team":
		return Limits{MaxSearches: 50, MaxMarketplaces: 5, MinCheckIntervalMins: 1, AIEnabled: true, AutoMessagingPerHour: 20, MaxShortlistEntries: 500}
	default:
		return Limits{MaxSearches: 2, MaxMarketplaces: 1, MinCheckIntervalMins: 30, MaxShortlistEntries: 10}
	}
}
