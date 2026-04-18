package scorer

import (
	"context"
	"log/slog"
	"strings"

	"github.com/TechXTT/xolto/internal/models"
)

// MustHaveEvaluator is the interface for semantic LLM-backed must-have evaluation.
// The evaluator is called once per listing with the subset of must-haves that
// came back "unknown" from the tokenizer. It may return "met", "missed", or
// "unknown" for each queried must-have.
//
// The interface uses flat scalar arguments (no imported struct types) so that
// implementations in other packages (e.g. internal/reasoner) can satisfy it
// without creating an import cycle through the scorer → reasoner → scorer path.
//
// Implementations must be safe for concurrent use.
type MustHaveEvaluator interface {
	// Evaluate takes the listing context plus the subset of must-haves that
	// returned "unknown" from the tokenizer, and returns a verdict map:
	//   "met"     — listing text positively confirms the must-have.
	//   "missed"  — listing text explicitly contradicts or denies the must-have.
	//   "unknown" — no signal either way (safe default).
	// Keys in the returned map are must-have text strings. Missing keys are
	// treated as "unknown". On error the caller falls back to the tokenizer result.
	Evaluate(ctx context.Context, listingID string, missionID int64, userID string, title string, description string, unknownMustHaves []string) (map[string]string, error)
}

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

// ScoreMustHavesSemantic extends ScoreMustHaves with an optional semantic LLM
// evaluator that can emit "missed" when a listing explicitly contradicts or
// explicitly lacks a buyer must-have.
//
// Evaluation order:
//  1. Run the tokenizer (ScoreMustHaves) on the full must-have list.
//  2. If every result is "met", return immediately — no LLM call (cost guard).
//  3. Collect the subset of must-haves that returned "unknown".
//  4. Call evaluator.Evaluate once with that subset.
//  5. Merge LLM verdicts into the tokenizer result: LLM may upgrade "unknown"
//     to "met", downgrade to "missed", or leave as "unknown". "met" results
//     from the tokenizer are NEVER re-evaluated.
//  6. On any evaluator error: log one WARN with op=musthave.evaluator.warn,
//     listing_id, mission_id, must-have count, and error. Return tokenizer result.
//
// If evaluator is nil, behaves identically to ScoreMustHaves.
// userID is forwarded to the evaluator for rate-limit and usage tracking.
func ScoreMustHavesSemantic(
	ctx context.Context,
	listing models.Listing,
	mustHaves []string,
	missionID int64,
	evaluator MustHaveEvaluator,
) []models.MustHaveMatch {
	return scoreMustHavesSemanticWithUser(ctx, listing, mustHaves, missionID, "", evaluator)
}

// scoreMustHavesSemanticWithUser is the internal implementation that threads
// userID through to the evaluator for usage tracking. Exported callers that
// have userID available can call ScoreMustHavesSemantic indirectly; the
// /matches handler passes it via the evaluator configuration.
func scoreMustHavesSemanticWithUser(
	ctx context.Context,
	listing models.Listing,
	mustHaves []string,
	missionID int64,
	userID string,
	evaluator MustHaveEvaluator,
) []models.MustHaveMatch {
	// Step 1: run tokenizer.
	tokenizerResult := ScoreMustHaves(listing, mustHaves)

	// Step 2 / short-circuit: no evaluator or every result is already "met".
	if evaluator == nil {
		return tokenizerResult
	}
	allMet := true
	for _, m := range tokenizerResult {
		if m.Status != MustHaveStatusMet {
			allMet = false
			break
		}
	}
	if allMet {
		return tokenizerResult
	}

	// Step 3: collect "unknown" subset in source order.
	unknownTexts := make([]string, 0, len(tokenizerResult))
	for _, m := range tokenizerResult {
		if m.Status == MustHaveStatusUnknown {
			unknownTexts = append(unknownTexts, m.Text)
		}
	}
	if len(unknownTexts) == 0 {
		// All non-"met" entries must be "missed" already (shouldn't happen from
		// the tokenizer, but guard defensively).
		return tokenizerResult
	}

	// Step 4: invoke the evaluator once for the unknown subset.
	verdicts, err := evaluator.Evaluate(ctx, listing.ItemID, missionID, userID, listing.Title, listing.Description, unknownTexts)
	if err != nil {
		slog.WarnContext(ctx, "must-have evaluator error — using tokenizer result",
			"op", "musthave.evaluator.warn",
			"listing_id", listing.ItemID,
			"mission_id", missionID,
			"musthave_count", len(unknownTexts),
			"error", err,
		)
		return tokenizerResult
	}

	// Step 5: merge LLM verdicts into a copy of the tokenizer result. Tokenizer
	// "met" entries are never overwritten. Only "unknown" entries are candidates.
	merged := make([]models.MustHaveMatch, len(tokenizerResult))
	copy(merged, tokenizerResult)
	if len(verdicts) > 0 {
		for i, m := range merged {
			if m.Status != MustHaveStatusUnknown {
				continue
			}
			verdict, ok := verdicts[m.Text]
			if !ok {
				continue
			}
			switch verdict {
			case MustHaveStatusMet, MustHaveStatusMissed, MustHaveStatusUnknown:
				merged[i].Status = verdict
			}
			// Any other value from the LLM is ignored; tokenizer "unknown" is preserved.
		}
	}
	return merged
}
