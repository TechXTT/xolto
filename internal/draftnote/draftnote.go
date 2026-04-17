// Package draftnote generates verdict-shaped seller note drafts for the
// outreach flow. Output is plain text (no markdown), NL-first with EN
// fallback. No external calls, no persistence — callers own both.
package draftnote

import (
	"fmt"
	"strings"

	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
)

// Shape is the structural template used for the drafted text.
// Exactly four values are emitted — one per verdict plus generic for skip.
type Shape string

const (
	ShapeBuy       Shape = "buy"
	ShapeNegotiate Shape = "negotiate"
	ShapeAskSeller Shape = "ask_seller"
	ShapeGeneric   Shape = "generic"
)

// Lang is the language of the drafted text.
// Exactly two values are emitted — nl (default) or en (fallback).
type Lang string

const (
	LangNL Lang = "nl"
	LangEN Lang = "en"
)

// Note is the structured output of Draft.
type Note struct {
	Text  string `json:"text"`
	Shape Shape  `json:"shape"`
	Lang  Lang   `json:"lang"`
}

// flagToQuestionNL maps the priority-ordered risk flags to a Dutch clarifying
// question. Priority order (highest to lowest) mirrors the dash UI flag-chip
// ordering: anomaly_price, vague_condition, no_battery_health,
// missing_key_photos, no_model_id, unclear_bundle, refurbished_ambiguity.
var flagToQuestionNL = map[string]string{
	"anomaly_price":         "De prijs lijkt erg laag — kun je bevestigen dat het product origineel en niet gestolen is?",
	"vague_condition":       "De advertentie vermeldt 'zo is het' of vergelijkbare bewoording — kun je de exacte staat beschrijven en melden of er gebreken zijn?",
	"no_battery_health":     "Kun je de batterijgezondheid delen (percentage via instellingen)?",
	"missing_key_photos":    "Kun je extra foto's sturen van alle kanten, inclusief scherm en achterkant?",
	"no_model_id":           "Kun je het exacte modelnummer bevestigen zodat ik zeker weet dat het om het juiste apparaat gaat?",
	"unclear_bundle":        "Kun je precies vermelden wat er allemaal bij wordt geleverd (oplader, kabels, originele verpakking)?",
	"refurbished_ambiguity": "Is het apparaat gereviseerd door de fabrikant of door een derde partij, en is er garantie bij?",
}

var flagToQuestionEN = map[string]string{
	"anomaly_price":         "The price seems unusually low — can you confirm the item is genuine and not stolen?",
	"vague_condition":       "The listing says 'as is' or similar — can you describe the exact condition and any defects?",
	"no_battery_health":     "Could you share the battery health percentage (from settings)?",
	"missing_key_photos":    "Could you send additional photos of all sides, including the screen and back?",
	"no_model_id":           "Could you confirm the exact model number so I can be sure it's the right device?",
	"unclear_bundle":        "Could you clarify exactly what's included (charger, cables, original box)?",
	"refurbished_ambiguity": "Was this refurbished by the manufacturer or a third party, and is there a warranty?",
}

// flagPriority defines the tiebreak order when multiple risk flags are
// present. Lower index = higher priority.
var flagPriority = []string{
	"anomaly_price",
	"vague_condition",
	"no_battery_health",
	"missing_key_photos",
	"no_model_id",
	"unclear_bundle",
	"refurbished_ambiguity",
}

// dutchStopWords is a minimal set used to detect Dutch-language listings.
// A title or description that contains at least one of these is treated as NL.
var dutchStopWords = []string{
	"de", "het", "een", "en", "van", "te", "in", "op", "dat", "is",
	"met", "voor", "zijn", "als", "er", "maar", "om", "bij", "ook",
	"naar", "dan", "niet", "dit", "ze", "aan", "heeft", "worden",
}

// detectLang returns LangNL when the listing title or first 100 chars of
// description contain at least one Dutch stop-word, otherwise LangEN.
func detectLang(l models.Listing) Lang {
	sample := strings.ToLower(l.Title)
	desc := l.Description
	if len(desc) > 100 {
		desc = desc[:100]
	}
	sample += " " + strings.ToLower(desc)

	// Tokenise on whitespace and punctuation for accurate whole-word matching.
	tokens := strings.FieldsFunc(sample, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == '/' ||
			r == '\n' || r == '\r' || r == '\t' || r == '(' || r == ')'
	})
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = struct{}{}
	}
	for _, w := range dutchStopWords {
		if _, ok := tokenSet[w]; ok {
			return LangNL
		}
	}
	return LangEN
}

// topPriorityFlag returns the highest-priority risk flag from flags that is
// not already described by the listing's own title or description, and for
// which a question template exists. Returns "" when no match is found.
func topPriorityFlag(flags []string, listing models.Listing) string {
	flagSet := make(map[string]struct{}, len(flags))
	for _, f := range flags {
		flagSet[f] = struct{}{}
	}
	lowerTitle := strings.ToLower(listing.Title)
	lowerDesc := strings.ToLower(listing.Description)

	for _, candidate := range flagPriority {
		if _, ok := flagSet[candidate]; !ok {
			continue
		}
		// Deduplicate: skip if the listing already addresses this concern.
		if candidate == "no_battery_health" {
			if strings.Contains(lowerTitle, "battery") || strings.Contains(lowerTitle, "batterij") ||
				strings.Contains(lowerDesc, "battery health") || strings.Contains(lowerDesc, "batterijgezondheid") ||
				strings.Contains(lowerDesc, "%") {
				continue
			}
		}
		if candidate == "no_model_id" {
			// Skip if the title already contains a model identifier pattern (letters+digits).
			if hasModelPattern(lowerTitle) {
				continue
			}
		}
		return candidate
	}
	return ""
}

// hasModelPattern is a simple heuristic: true when the string contains a
// word that mixes letters and digits (typical model identifiers like "a6000",
// "x100v", "t14s").
func hasModelPattern(s string) bool {
	hasLetter := false
	hasDigit := false
	inWord := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			if inWord {
				hasLetter = true
			}
			inWord = true
		case r >= '0' && r <= '9':
			if inWord {
				hasDigit = true
			}
			inWord = true
		default:
			if inWord && hasLetter && hasDigit {
				return true
			}
			inWord = false
			hasLetter = false
			hasDigit = false
		}
	}
	return inWord && hasLetter && hasDigit
}

// Draft generates a verdict-shaped seller note for the given listing and
// verdict string. verdict must be one of the four canonical action values;
// the caller is responsible for validating and rejecting unknown verdicts
// before calling Draft.
//
// The negotiate shape uses the listing's FairPrice to anchor the price
// suggestion. When FairPrice is zero (no comparables data), the shape still
// emits negotiate text but without a specific price anchor — the caller's
// report must surface this gap (see structural emission gap disclosure).
func Draft(verdict string, listing models.Listing) Note {
	lang := detectLang(listing)
	shape := verdictToShape(verdict)
	text := buildText(shape, lang, listing)
	return Note{Text: text, Shape: shape, Lang: lang}
}

func verdictToShape(verdict string) Shape {
	switch verdict {
	case scorer.ActionBuy:
		return ShapeBuy
	case scorer.ActionNegotiate:
		return ShapeNegotiate
	case scorer.ActionAskSeller:
		return ShapeAskSeller
	case scorer.ActionSkip:
		return ShapeGeneric
	default:
		// Unreachable: caller validates the verdict enum before calling Draft.
		return ShapeGeneric
	}
}

func buildText(shape Shape, lang Lang, listing models.Listing) string {
	switch shape {
	case ShapeBuy:
		return buildBuyText(lang, listing)
	case ShapeNegotiate:
		return buildNegotiateText(lang, listing)
	case ShapeAskSeller:
		return buildAskSellerText(lang, listing)
	default:
		return buildGenericText(lang, listing)
	}
}

func buildBuyText(lang Lang, listing models.Listing) string {
	// Buy draft: confirm intent + pickup logistics. No price — buyer accepts asking price.
	switch lang {
	case LangNL:
		return strings.TrimSpace(fmt.Sprintf(
			"Hoi! Ik ben geïnteresseerd in je %s en wil hem graag kopen. Kun je bevestigen dat hij nog beschikbaar is en wanneer ik hem kan ophalen?",
			listing.Title,
		))
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Hi! I'm interested in your %s and would like to buy it. Could you confirm it's still available and when I could pick it up?",
			listing.Title,
		))
	}
}

func buildNegotiateText(lang Lang, listing models.Listing) string {
	// Negotiate draft: comparables-anchored price suggestion.
	// Guard: anchor must not go below fair_price × 0.85.
	fairPrice := listing.FairPrice
	if fairPrice <= 0 {
		// No anchor available — emit intent-only negotiate text without a price.
		// Structural emission gap: when FairPrice=0 the draft omits the price
		// anchor. Callers can detect this via listing.FairPrice == 0.
		switch lang {
		case LangNL:
			return strings.TrimSpace(fmt.Sprintf(
				"Hoi! Ik heb interesse in je %s. Op basis van vergelijkbare aanbiedingen vraag ik me af of er iets mogelijk is aan de prijs. Kun je me meer vertellen?",
				listing.Title,
			))
		default:
			return strings.TrimSpace(fmt.Sprintf(
				"Hi! I'm interested in your %s. Based on comparable listings I was wondering if there's any flexibility on the price. Could you tell me more?",
				listing.Title,
			))
		}
	}

	// Suggested offer = fair price (market value). Floor at 85% of fair price.
	suggestedOffer := fairPrice
	floor := int(float64(fairPrice) * 0.85)
	if suggestedOffer < floor {
		suggestedOffer = floor
	}

	switch lang {
	case LangNL:
		return strings.TrimSpace(fmt.Sprintf(
			"Hoi! Ik heb interesse in je %s. Op basis van vergelijkbare advertenties zie ik een marktprijs rond %s. Zou je %s overwegen? Als alles in orde is, kan ik snel ophalen.",
			listing.Title,
			format.Euro(fairPrice),
			format.Euro(suggestedOffer),
		))
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Hi! I'm interested in your %s. Based on comparable listings the market price is around %s. Would you consider %s? If everything checks out I can pick it up quickly.",
			listing.Title,
			format.Euro(fairPrice),
			format.Euro(suggestedOffer),
		))
	}
}

func buildAskSellerText(lang Lang, listing models.Listing) string {
	// Ask seller draft: clarifying question from top-priority risk flag.
	flag := topPriorityFlag(listing.RiskFlags, listing)

	var question string
	if flag != "" {
		if lang == LangNL {
			question = flagToQuestionNL[flag]
		} else {
			question = flagToQuestionEN[flag]
		}
	}

	if question == "" {
		switch lang {
		case LangNL:
			question = "Kun je meer vertellen over de conditie en meegeleverde accessoires?"
		default:
			question = "Could you tell me more about the condition and what accessories are included?"
		}
	}

	switch lang {
	case LangNL:
		return strings.TrimSpace(fmt.Sprintf(
			"Hoi! Ik heb interesse in je %s. %s",
			listing.Title,
			question,
		))
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Hi! I'm interested in your %s. %s",
			listing.Title,
			question,
		))
	}
}

func buildGenericText(lang Lang, listing models.Listing) string {
	// Generic / skip: polite no-commitment opener — user asked for a draft
	// despite a skip verdict, so emit a soft enquiry rather than a refusal.
	switch lang {
	case LangNL:
		return strings.TrimSpace(fmt.Sprintf(
			"Hoi! Ik zag je advertentie voor de %s. Kun je me iets meer vertellen over de staat en of er nog onderhandelingsruimte is?",
			listing.Title,
		))
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Hi! I saw your listing for the %s. Could you tell me a bit more about the condition and whether there's any room on the price?",
			listing.Title,
		))
	}
}
