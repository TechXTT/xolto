package billing

import "strings"

// Tier definitions here are authoritative. xolto-landing/components/landing/Pricing.tsx
// and xolto-app /settings tier display must mirror these. Update all three when
// changing tiers.
//
// Internal slugs (free, pro, power) are kept stable to avoid a DB migration.
// User-facing display labels are mapped via TierDisplayName below:
//
//	internal slug   user-facing display
//	free            Free
//	pro             Buyer   (mid tier)
//	power           Pro     (top tier)
//
// Auto-messaging is dropped as a tier feature as of W18 (2026-04-25).
// AutoMessagingPerHour is retained on the struct for compatibility but is
// always 0 across every tier; nothing reads it as a non-zero allowance.
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

// TierDisplayName returns the user-facing display label for an internal tier
// slug. Unknown slugs fall back to "Free" (fail-safe). Keep this in sync with
// xolto-landing Pricing.tsx and xolto-app /settings.
func TierDisplayName(tier string) string {
	switch NormalizeTier(tier) {
	case "pro":
		return "Buyer"
	case "power":
		return "Pro"
	case "free":
		return "Free"
	default:
		return "Free"
	}
}

func LimitsFor(tier string) Limits {
	switch NormalizeTier(tier) {
	case "pro":
		return Limits{MaxSearches: 10, MaxMissions: 10, MaxMarketplaces: 2, MinCheckIntervalMins: 5, MaxConcurrentSearches: 2, MaxDispatchPerTick: 3, PlanPriorityWeight: 25, AlertPriorityWeight: 1, AIEnabled: true, AutoMessagingPerHour: 0, MaxShortlistEntries: 100}
	case "power":
		return Limits{MaxSearches: 50, MaxMissions: 0, MaxMarketplaces: 5, MinCheckIntervalMins: 1, MaxConcurrentSearches: 4, MaxDispatchPerTick: 8, PlanPriorityWeight: 50, AlertPriorityWeight: 2, AIEnabled: true, AutoMessagingPerHour: 0, MaxShortlistEntries: 500}
	default:
		return Limits{MaxSearches: 2, MaxMissions: 1, MaxMarketplaces: 1, MinCheckIntervalMins: 30, MaxConcurrentSearches: 1, MaxDispatchPerTick: 1, PlanPriorityWeight: 0, AlertPriorityWeight: 0, AutoMessagingPerHour: 0, MaxShortlistEntries: 10}
	}
}
