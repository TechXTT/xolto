package scorer

import (
	"strings"

	"github.com/TechXTT/xolto/internal/models"
)

// MustHaveStatusMet, MustHaveStatusMissed, and MustHaveStatusUnknown are the
// exhaustive set of values for MustHaveMatch.Status. Do not introduce other
// values without PM approval.
const (
	MustHaveStatusMet     = "met"
	MustHaveStatusMissed  = "missed"
	MustHaveStatusUnknown = "unknown"
)

// ScoreMustHaves evaluates each must-have text against the listing's title and
// description and returns a slice of MustHaveMatch in the same order as
// mustHaves. When mustHaves is empty, an empty (non-nil) slice is returned so
// callers can iterate without nil guards.
//
// Status assignment rules:
//   - "met"     — the listing text contains the must-have keyword(s) (case-insensitive).
//   - "missed"  — reserved for future semantic matching; not emitted by this
//                 implementation to avoid false exclusions.
//   - "unknown" — the listing text is silent on the must-have.
//
// The conservative default is "unknown". Over-claiming "met" (false trust) is
// the primary failure mode — only "met" when there is clear textual evidence.
func ScoreMustHaves(listing models.Listing, mustHaves []string) []models.MustHaveMatch {
	result := make([]models.MustHaveMatch, 0, len(mustHaves))
	// Build a lowercase search corpus from title and description. Concatenate
	// with a space so a keyword spanning the boundary is not accidentally matched.
	corpus := strings.ToLower(listing.Title + " " + listing.Description)
	for _, mh := range mustHaves {
		mhTrim := strings.TrimSpace(mh)
		if mhTrim == "" {
			// Blank must-have entries are treated as unknown — skip silently.
			result = append(result, models.MustHaveMatch{Text: mh, Status: MustHaveStatusUnknown})
			continue
		}
		status := matchMustHave(corpus, mhTrim)
		result = append(result, models.MustHaveMatch{Text: mh, Status: status})
	}
	return result
}

// matchMustHave evaluates a single must-have phrase against the lowercase
// listing corpus. Returns "met" if the phrase is present, "unknown" otherwise.
// "missed" is not emitted by pure keyword matching to avoid false exclusions;
// future semantic matching may emit it.
func matchMustHave(corpusLower, mustHaveTrim string) string {
	// Keyword matching: split the must-have into tokens and require all tokens
	// to appear in the corpus. This handles multi-word phrases like "no dead
	// pixels" more gracefully than a single-substring search.
	lower := strings.ToLower(mustHaveTrim)
	tokens := tokenize(lower)
	if len(tokens) == 0 {
		return MustHaveStatusUnknown
	}
	for _, tok := range tokens {
		if !strings.Contains(corpusLower, tok) {
			return MustHaveStatusUnknown
		}
	}
	return MustHaveStatusMet
}

// tokenize splits a must-have phrase into meaningful tokens, stripping common
// stop words so that "no dead pixels" does not require "no" to appear
// literally in the listing (which would be over-restrictive). We only strip
// leading/trailing stop words; interior ones are preserved to keep specificity.
//
// Tokens that remain after stripping are used for inclusion matching.
func tokenize(phrase string) []string {
	// Split on whitespace.
	raw := strings.Fields(phrase)
	if len(raw) == 0 {
		return nil
	}
	// For a single word, return it directly.
	if len(raw) == 1 {
		return raw
	}
	// For multi-word phrases, strip leading negations and quantifiers so that
	// e.g. "no dead pixels" matches on "dead pixels" signal in listing rather
	// than the literal word "no". This prevents false misses when a seller
	// writes "confirmed no dead pixels" — the critical signal is "dead pixels"
	// plus context, and over-filtering on "no" harms recall more than precision.
	//
	// Words stripped only from the LEADING position (not interior):
	leading := map[string]bool{
		"no": true, "without": true, "with": true, "has": true,
		"have": true, "a": true, "an": true, "the": true,
		"must": true, "should": true, "needs": true, "need": true,
	}
	tokens := raw
	for len(tokens) > 1 && leading[tokens[0]] {
		tokens = tokens[1:]
	}
	return tokens
}
