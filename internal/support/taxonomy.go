// Package support provides the taxonomy types and classification logic
// for xolto's Claude-managed support platform (SUP-3 / XOL-54).
//
// This module is deterministic: it validates LLM output against typed
// enums and runs an incident-keyword severity override before returning
// a Classification to the caller.
//
// The actual LLM call is SUP-4's responsibility; this package only
// consumes the LLM's JSON-unmarshaled result.
package support

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Typed enums
// ---------------------------------------------------------------------------

// Category represents the support ticket category.
type Category string

const (
	CategoryPricing      Category = "pricing"
	CategoryListingWrong Category = "listing_wrong"
	CategoryVerdict      Category = "verdict"
	CategoryMarketplace  Category = "marketplace"
	CategoryLogin        Category = "login"
	CategoryBilling      Category = "billing"
	CategoryBug          Category = "bug"
	CategoryFeature      Category = "feature"
	CategoryGeneral      Category = "general"
)

// AllCategories is the exhaustive set of Category values.
var AllCategories = []Category{
	CategoryPricing,
	CategoryListingWrong,
	CategoryVerdict,
	CategoryMarketplace,
	CategoryLogin,
	CategoryBilling,
	CategoryBug,
	CategoryFeature,
	CategoryGeneral,
}

// Market represents the marketplace the support event relates to.
type Market string

const (
	MarketOLXBG       Market = "olx_bg"
	MarketMarktplaats Market = "marktplaats"
	MarketVintedNL    Market = "vinted_nl"
	MarketVintedDK    Market = "vinted_dk"
	MarketUnknown     Market = "unknown"
)

// AllMarkets is the exhaustive set of Market values.
var AllMarkets = []Market{
	MarketOLXBG,
	MarketMarktplaats,
	MarketVintedNL,
	MarketVintedDK,
	MarketUnknown,
}

// ProductCat represents the product category the support event relates to.
type ProductCat string

const (
	ProductCatCamera    ProductCat = "camera"
	ProductCatLaptop    ProductCat = "laptop"
	ProductCatPhone     ProductCat = "phone"
	ProductCatAudio     ProductCat = "audio"
	ProductCatGaming    ProductCat = "gaming"
	ProductCatTablet    ProductCat = "tablet"
	ProductCatAppliance ProductCat = "appliance"
	ProductCatOther     ProductCat = "other"
)

// AllProductCats is the exhaustive set of ProductCat values.
var AllProductCats = []ProductCat{
	ProductCatCamera,
	ProductCatLaptop,
	ProductCatPhone,
	ProductCatAudio,
	ProductCatGaming,
	ProductCatTablet,
	ProductCatAppliance,
	ProductCatOther,
}

// Severity represents how urgently the support event needs attention.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityIncident Severity = "incident"
)

// AllSeverities is the exhaustive set of Severity values.
var AllSeverities = []Severity{
	SeverityLow,
	SeverityMedium,
	SeverityHigh,
	SeverityIncident,
}

// ActionNeeded represents the action required to resolve the support event.
type ActionNeeded string

const (
	ActionReplyOnly            ActionNeeded = "reply_only"
	ActionBackendFix           ActionNeeded = "backend_fix"
	ActionDashFix              ActionNeeded = "dash_fix"
	ActionScorerFix            ActionNeeded = "scorer_fix"
	ActionScraperFix           ActionNeeded = "scraper_fix"
	ActionBillingAuthFix       ActionNeeded = "billing_auth_fix"
	ActionProductClarification ActionNeeded = "product_clarification"
	ActionRoadmapCandidate     ActionNeeded = "roadmap_candidate"
)

// AllActionsNeeded is the exhaustive set of ActionNeeded values.
var AllActionsNeeded = []ActionNeeded{
	ActionReplyOnly,
	ActionBackendFix,
	ActionDashFix,
	ActionScorerFix,
	ActionScraperFix,
	ActionBillingAuthFix,
	ActionProductClarification,
	ActionRoadmapCandidate,
}

// ---------------------------------------------------------------------------
// Routing map
// ---------------------------------------------------------------------------

// ActionToLinearProject maps each ActionNeeded value to the Linear project
// that should receive the resulting issue. ActionReplyOnly maps to "" (no
// Linear issue is created).
var ActionToLinearProject = map[ActionNeeded]string{
	ActionBackendFix:           "OLX BG trust",
	ActionScraperFix:           "OLX BG trust",
	ActionDashFix:              "/matches decisional pillar",
	ActionScorerFix:            "/matches decisional pillar",
	ActionBillingAuthFix:       "auth-billing",
	ActionProductClarification: "xolto-roadmap",
	ActionRoadmapCandidate:     "xolto-roadmap",
	ActionReplyOnly:            "",
}

// ---------------------------------------------------------------------------
// Incident keyword override
// ---------------------------------------------------------------------------

// IncidentKeywords is the list of terms whose presence in a support thread
// body forces severity = incident, regardless of LLM output.
//
// The list is multilingual: English, Bulgarian Cyrillic, and Dutch.
var IncidentKeywords = []string{
	// English
	"down", "outage", "can't log in", "cant log in", "can't login", "cannot login",
	"locked out", "hacked", "breach", "unauthorized charge", "billed twice",
	"refund", "gdpr", "dmca", "legal", "lawyer", "lawsuit",
	// Bulgarian Cyrillic
	"паднал", "не работи", "не мога да вляза", "хакнат", "измама",
	"двойно плащане", "възстановяване на сума",
	// Dutch
	"kan niet inloggen", "terugbetaling", "gehackt",
}

// tokenRe matches Unicode letters and digits (Cyrillic-safe; avoids \b
// which is ASCII-only in Go's RE2 engine).
var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// buildKeywordPattern compiles a single regexp that matches any of the
// given keywords in a Unicode-safe, case-insensitive, word-boundary-aware
// way. Each keyword may be a single word or a multi-word phrase.
//
// Strategy for word-boundary safety without \b:
//   - Single token: assert that no Unicode letter/digit precedes or follows
//     by using negative look-around — Go RE2 does not support look-arounds,
//     so instead we pre-tokenize the text and match against token windows.
//
// Because RE2 has no look-arounds, the boundary check is implemented in
// matchesIncidentKeyword by tokenizing the lowercased input and sliding a
// window over the tokens.
func buildKeywordPattern(keywords []string) *regexp.Regexp {
	// Build a simple (?i) alternation for fast substring pre-screening.
	// The authoritative boundary check is done in matchesIncidentKeyword.
	parts := make([]string, len(keywords))
	for i, kw := range keywords {
		parts[i] = regexp.QuoteMeta(strings.ToLower(kw))
	}
	return regexp.MustCompile(`(?i)` + strings.Join(parts, "|"))
}

var incidentPattern = buildKeywordPattern(IncidentKeywords)

// HasIncidentKeyword reports whether body contains any incident keyword,
// enforcing Unicode-safe word-boundary matching.
//
// Multi-word keywords (e.g. "can't log in") are matched as a contiguous
// token sequence. Single-word keywords are matched only when surrounded by
// non-letter/non-digit characters (or at string boundaries).
func HasIncidentKeyword(body string) bool {
	lower := strings.ToLower(body)

	// Fast path: no substring match at all.
	if !incidentPattern.MatchString(lower) {
		return false
	}

	// Slow path: authoritative boundary-aware check.
	// Tokenize by splitting on runs of non-letter/non-digit characters,
	// preserving the original casing-lowered text for phrase matching.
	return matchesIncidentKeyword(lower)
}

// matchesIncidentKeyword performs the authoritative boundary check against
// the lowercased body.
func matchesIncidentKeyword(lower string) bool {
	// Extract token positions so we can do phrase (multi-token) matching.
	locs := tokenRe.FindAllStringIndex(lower, -1)
	tokens := make([]string, len(locs))
	for i, loc := range locs {
		tokens[i] = lower[loc[0]:loc[1]]
	}

	for _, kw := range IncidentKeywords {
		kwLower := strings.ToLower(kw)
		kwTokens := tokenRe.FindAllString(kwLower, -1)

		if len(kwTokens) == 0 {
			continue
		}

		if len(kwTokens) == 1 {
			// Single-token keyword: match against the token list directly.
			for _, tok := range tokens {
				if tok == kwTokens[0] {
					return true
				}
			}
		} else {
			// Multi-token keyword (phrase): slide a window over the token list.
			n := len(kwTokens)
			for i := 0; i <= len(tokens)-n; i++ {
				match := true
				for j := 0; j < n; j++ {
					if tokens[i+j] != kwTokens[j] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}

	return false
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// Sentinel errors allow callers to use errors.Is for precise error handling.
var (
	ErrInvalidCategory   = errors.New("support/taxonomy: invalid category value")
	ErrInvalidMarket     = errors.New("support/taxonomy: invalid market value")
	ErrInvalidProductCat = errors.New("support/taxonomy: invalid product_cat value")
	ErrInvalidSeverity   = errors.New("support/taxonomy: invalid severity value")
	ErrInvalidAction     = errors.New("support/taxonomy: invalid action_needed value")
)

// ---------------------------------------------------------------------------
// LLMClassification — raw strings from LLM JSON output
// ---------------------------------------------------------------------------

// LLMClassification holds the raw string fields unmarshaled from the LLM's
// JSON response. Fields are strings (not typed enums) so that JSON
// unmarshaling succeeds even when the LLM emits an unrecognized value;
// Classify() performs the enum validation step.
type LLMClassification struct {
	Category   string `json:"category"`
	Market     string `json:"market"`
	ProductCat string `json:"product_cat"`
	Severity   string `json:"severity"`
	Action     string `json:"action_needed"`
}

// ---------------------------------------------------------------------------
// Classification — validated, typed result
// ---------------------------------------------------------------------------

// Classification is the fully validated, typed result returned by Classify.
type Classification struct {
	Category   Category
	Market     Market
	ProductCat ProductCat
	Severity   Severity
	Action     ActionNeeded

	// LinearProject is the routing target derived from ActionToLinearProject.
	// It is empty when Action == ActionReplyOnly.
	LinearProject string

	// IncidentOverride is true when the severity was forced to SeverityIncident
	// by the keyword matcher, overriding the LLM's output.
	IncidentOverride bool
}

// ---------------------------------------------------------------------------
// Classify
// ---------------------------------------------------------------------------

// Classify validates llmResult, applies the incident-keyword override, and
// returns a fully typed Classification.
//
// The incident-keyword check runs first. If the combined body+subject text
// contains a keyword, severity is forced to SeverityIncident regardless of
// llmResult.Severity.
//
// If any LLM field contains a value not in the corresponding enum, Classify
// returns a non-nil error using one of the sentinel errors defined above.
// The caller should treat such events as requiring human-only triage.
func Classify(body, subject string, llmResult LLMClassification) (Classification, error) {
	// Validate Category.
	cat, err := parseCategory(llmResult.Category)
	if err != nil {
		return Classification{}, fmt.Errorf("%w: %q", ErrInvalidCategory, llmResult.Category)
	}

	// Validate Market.
	mkt, err := parseMarket(llmResult.Market)
	if err != nil {
		return Classification{}, fmt.Errorf("%w: %q", ErrInvalidMarket, llmResult.Market)
	}

	// Validate ProductCat.
	pc, err := parseProductCat(llmResult.ProductCat)
	if err != nil {
		return Classification{}, fmt.Errorf("%w: %q", ErrInvalidProductCat, llmResult.ProductCat)
	}

	// Validate Severity.
	sev, err := parseSeverity(llmResult.Severity)
	if err != nil {
		return Classification{}, fmt.Errorf("%w: %q", ErrInvalidSeverity, llmResult.Severity)
	}

	// Validate ActionNeeded.
	action, err := parseAction(llmResult.Action)
	if err != nil {
		return Classification{}, fmt.Errorf("%w: %q", ErrInvalidAction, llmResult.Action)
	}

	// Incident-keyword override: check combined body + subject text.
	combined := body + " " + subject
	incidentOverride := HasIncidentKeyword(combined)
	if incidentOverride {
		sev = SeverityIncident
	}

	return Classification{
		Category:         cat,
		Market:           mkt,
		ProductCat:       pc,
		Severity:         sev,
		Action:           action,
		LinearProject:    ActionToLinearProject[action],
		IncidentOverride: incidentOverride,
	}, nil
}

// ---------------------------------------------------------------------------
// Parse helpers
// ---------------------------------------------------------------------------

func parseCategory(s string) (Category, error) {
	for _, v := range AllCategories {
		if string(v) == s {
			return v, nil
		}
	}
	return "", ErrInvalidCategory
}

func parseMarket(s string) (Market, error) {
	for _, v := range AllMarkets {
		if string(v) == s {
			return v, nil
		}
	}
	return "", ErrInvalidMarket
}

func parseProductCat(s string) (ProductCat, error) {
	for _, v := range AllProductCats {
		if string(v) == s {
			return v, nil
		}
	}
	return "", ErrInvalidProductCat
}

func parseSeverity(s string) (Severity, error) {
	for _, v := range AllSeverities {
		if string(v) == s {
			return v, nil
		}
	}
	return "", ErrInvalidSeverity
}

func parseAction(s string) (ActionNeeded, error) {
	for _, v := range AllActionsNeeded {
		if string(v) == s {
			return v, nil
		}
	}
	return "", ErrInvalidAction
}
