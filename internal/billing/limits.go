package billing

import "strings"

type Limits struct {
	MaxSearches           int
	MaxMissions           int
	MaxMarketplaces       int
	MinCheckIntervalMins  int
	MaxConcurrentSearches int
	MaxDispatchPerTick    int
	PlanPriorityWeight    int
	AlertPriorityWeight   int
	AIEnabled             bool
	AutoMessagingPerHour  int
	MaxShortlistEntries   int
}

func NormalizeTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "team":
		return "power"
	default:
		return strings.ToLower(strings.TrimSpace(tier))
	}
}

func LimitsFor(tier string) Limits {
	switch NormalizeTier(tier) {
	case "pro":
		return Limits{MaxSearches: 10, MaxMissions: 10, MaxMarketplaces: 2, MinCheckIntervalMins: 5, MaxConcurrentSearches: 2, MaxDispatchPerTick: 3, PlanPriorityWeight: 25, AlertPriorityWeight: 1, AIEnabled: true, AutoMessagingPerHour: 5, MaxShortlistEntries: 100}
	case "power":
		return Limits{MaxSearches: 50, MaxMissions: 0, MaxMarketplaces: 5, MinCheckIntervalMins: 1, MaxConcurrentSearches: 4, MaxDispatchPerTick: 8, PlanPriorityWeight: 50, AlertPriorityWeight: 2, AIEnabled: true, AutoMessagingPerHour: 20, MaxShortlistEntries: 500}
	default:
		return Limits{MaxSearches: 2, MaxMissions: 1, MaxMarketplaces: 1, MinCheckIntervalMins: 30, MaxConcurrentSearches: 1, MaxDispatchPerTick: 1, PlanPriorityWeight: 0, AlertPriorityWeight: 0, MaxShortlistEntries: 10}
	}
}
