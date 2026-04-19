// Package replycopilot interprets a seller reply via LLM and generates a
// rule-based draft follow-up message. Stateless — no persistence, no new DB
// tables. The LLM is used only for classification; all draft generation is
// Go-side rule-based (XOL-73).
package replycopilot

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/TechXTT/xolto/internal/draftnote"
	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/models"
)

// Interpretation classifies the seller reply.
type Interpretation string

// Action is the recommended buyer action after interpreting the reply.
type Action string

// Confidence represents how certain the LLM classification is.
type Confidence string

const (
	InterpNegotiable Interpretation = "negotiable"
	InterpFirm       Interpretation = "firm"
	InterpLowSignal  Interpretation = "low_signal"
	InterpRisky      Interpretation = "risky"
)

const (
	ActionCounter   Action = "counter"
	ActionAskSeller Action = "ask_seller"
	ActionAccept    Action = "accept"
	ActionSkip      Action = "skip"
)

const (
	ConfHigh   Confidence = "high"
	ConfMedium Confidence = "medium"
	ConfLow    Confidence = "low"
)

// riskySignals is the exhaustive set of signal strings that force InterpRisky.
var riskySignals = map[string]bool{
	"urgency_pressure":      true,
	"other_buyer_claimed":   true,
	"scam_indicators":       true,
	"asks_for_wire_transfer": true,
}

// ReplyContext carries the buyer-side context when submitting a seller reply
// for interpretation.
type ReplyContext struct {
	SellerReply     string
	OurOfferPrice   int    // EUR cents; 0 = no prior offer
	OriginalVerdict string // "buy"|"negotiate"|"ask_seller"|"skip"|""
}

// MissionContext mirrors draftnote.MissionContext for package isolation.
type MissionContext struct {
	MustHaves   []string
	CountryCode string
}

// Result is the structured output of Interpret.
type Result struct {
	Interpretation   Interpretation `json:"interpretation"`
	RecommendedAction Action        `json:"recommended_action"`
	DraftNextMessage  string        `json:"draft_next_message"`
	Confidence        Confidence    `json:"confidence"`
	Signals           []string      `json:"signals"`
	Lang              draftnote.Lang `json:"lang"`
	OfferPrice        int           `json:"offer_price,omitempty"`
}

// Classifier is the LLM interface used by Interpret. Implement this with a
// real HTTP client or a test stub.
type Classifier interface {
	Classify(ctx context.Context, prompt string) (ClassifyResult, error)
}

// ClassifyResult is the parsed output from the LLM classifier.
type ClassifyResult struct {
	Interpretation Interpretation `json:"interpretation"`
	Confidence     Confidence     `json:"confidence"`
	Signals        []string       `json:"signals"`
}

// Interpret is the main entry point. It classifies the seller reply via the
// Classifier, applies Go-side post-LLM validation (non-negotiable), and
// generates a rule-based draft follow-up message.
func Interpret(ctx context.Context, rc ReplyContext, listing models.Listing, mission MissionContext, classifier Classifier) (Result, error) {
	lang := detectLang(rc.SellerReply, listing)

	// Step 1: Short-circuit for replies that are too short to classify.
	if len(strings.TrimSpace(rc.SellerReply)) < 5 {
		draft := buildDraft(ActionAskSeller, listing, 0, lang)
		return Result{
			Interpretation:    InterpLowSignal,
			RecommendedAction: ActionAskSeller,
			DraftNextMessage:  draft,
			Confidence:        ConfLow,
			Signals:           []string{"too_short"},
			Lang:              lang,
		}, nil
	}

	// Step 2: LLM classify. On error, use a safe fallback.
	var classified ClassifyResult
	if classifier != nil {
		var err error
		classified, err = classifier.Classify(ctx, buildPrompt(rc, listing, mission))
		if err != nil {
			classified = ClassifyResult{Interpretation: InterpLowSignal, Confidence: ConfLow}
		}
	} else {
		classified = ClassifyResult{Interpretation: InterpLowSignal, Confidence: ConfLow}
	}
	if classified.Signals == nil {
		classified.Signals = []string{}
	}

	// Step 3: Post-LLM Go-side validation — non-negotiable.

	// 3a. Unknown interpretation → force low_signal.
	switch classified.Interpretation {
	case InterpNegotiable, InterpFirm, InterpLowSignal, InterpRisky:
		// valid
	default:
		classified.Interpretation = InterpLowSignal
	}

	// 3b. Confidence low → force low_signal.
	if classified.Confidence == ConfLow {
		classified.Interpretation = InterpLowSignal
	}

	// 3c. Risky signals take priority over all other interpretations.
	for _, sig := range classified.Signals {
		if riskySignals[sig] {
			classified.Interpretation = InterpRisky
			break
		}
	}

	// 3d. Determine recommended action.
	action := resolveAction(classified, listing)

	// Step 4: Offer price — only for counter actions.
	offerPrice := 0
	if action == ActionCounter {
		offerPrice = computeOfferPrice(rc.OurOfferPrice, listing.FairPrice)
	}

	// Step 5: Rule-based draft generation (never LLM).
	draft := buildDraft(action, listing, offerPrice, lang)

	return Result{
		Interpretation:    classified.Interpretation,
		RecommendedAction: action,
		DraftNextMessage:  draft,
		Confidence:        classified.Confidence,
		Signals:           classified.Signals,
		Lang:              lang,
		OfferPrice:        offerPrice,
	}, nil
}

// resolveAction maps interpretation + confidence + listing price to the
// recommended buyer action. This logic is Go-side and cannot be overridden
// by the LLM.
func resolveAction(r ClassifyResult, listing models.Listing) Action {
	switch r.Interpretation {
	case InterpRisky:
		return ActionSkip
	case InterpLowSignal:
		return ActionAskSeller
	case InterpFirm:
		if listing.FairPrice > 0 && listing.Price > int(float64(listing.FairPrice)*1.05) {
			return ActionSkip
		}
		if r.Confidence == ConfHigh && listing.FairPrice > 0 && listing.Price <= int(float64(listing.FairPrice)*1.05) {
			return ActionAccept
		}
		return ActionAskSeller
	case InterpNegotiable:
		// Seller willing to negotiate → always counter; never accept without pushing first.
		if r.Confidence != ConfLow {
			return ActionCounter
		}
		return ActionAskSeller
	default:
		return ActionAskSeller
	}
}

// computeOfferPrice calculates the counter offer price in EUR cents.
func computeOfferPrice(ourOffer, fairPrice int) int {
	if fairPrice <= 0 {
		return 0
	}
	if ourOffer > 0 {
		mid := (ourOffer + fairPrice) / 2
		if mid > fairPrice {
			mid = fairPrice
		}
		return mid
	}
	return int(float64(fairPrice) * 0.90)
}

// buildDraft generates a rule-based draft message. Never calls the LLM.
func buildDraft(action Action, listing models.Listing, offerPrice int, lang draftnote.Lang) string {
	switch action {
	case ActionCounter:
		priceFmt := formatPrice(offerPrice, lang)
		switch lang {
		case draftnote.LangBG:
			return fmt.Sprintf("Благодаря за отговора. Бихте ли се съгласили на %s? Това е моята финална оферта.", priceFmt)
		case draftnote.LangNL:
			return fmt.Sprintf("Bedankt voor je reactie. Zou je akkoord gaan met %s? Dit is mijn laatste bod.", priceFmt)
		default:
			return fmt.Sprintf("Thanks for your reply. Would you consider %s? That's my final offer.", priceFmt)
		}
	case ActionAskSeller:
		switch lang {
		case draftnote.LangBG:
			return "Благодаря! Можете ли да изясните ситуацията малко повече?"
		case draftnote.LangNL:
			return "Bedankt! Kun je dit iets verder toelichten?"
		default:
			return "Thanks! Could you clarify a bit more?"
		}
	case ActionAccept:
		switch lang {
		case draftnote.LangBG:
			return "Чудесно! Кога мога да взема артикула?"
		case draftnote.LangNL:
			return "Top! Wanneer kan ik het ophalen?"
		default:
			return "Great! When can I pick it up?"
		}
	case ActionSkip:
		switch lang {
		case draftnote.LangBG:
			return "Разбирам. Благодаря за отговора. Ще следя обявата."
		case draftnote.LangNL:
			return "Begrepen. Bedankt voor je reactie. Ik houd de advertentie in de gaten."
		default:
			return "Understood. Thanks for your time."
		}
	default:
		return "Thanks! Could you clarify a bit more?"
	}
}

// formatPrice formats a price in EUR cents for display.
func formatPrice(cents int, lang draftnote.Lang) string {
	if lang == draftnote.LangBG {
		return format.BGN(cents)
	}
	return format.Euro(cents)
}

// buildPrompt constructs the LLM classification prompt.
func buildPrompt(rc ReplyContext, listing models.Listing, mission MissionContext) string {
	priceStr := fmt.Sprintf("EUR %.2f", float64(listing.Price)/100)
	fairStr := "unknown"
	if listing.FairPrice > 0 {
		fairStr = fmt.Sprintf("EUR %.2f", float64(listing.FairPrice)/100)
	}
	offerStr := "no offer sent"
	if rc.OurOfferPrice > 0 {
		offerStr = fmt.Sprintf("EUR %.2f", float64(rc.OurOfferPrice)/100)
	}

	return strings.Join([]string{
		"You are a used-electronics buying assistant helping interpret a seller reply.",
		"",
		"Listing: " + listing.Title,
		"Asking price: " + priceStr,
		"Fair value estimate: " + fairStr + " (or \"unknown\" if 0)",
		"Our offer: " + offerStr + " (or \"no offer sent\" if 0)",
		"Seller reply: \"" + rc.SellerReply + "\"",
		"",
		"Classify the seller reply. Reply with strict JSON only — no markdown, no explanation:",
		"{\"interpretation\":\"negotiable|firm|low_signal|risky\",\"confidence\":\"high|medium|low\",\"signals\":[\"...\"]}",
		"",
		"Interpretation rules:",
		"- \"negotiable\": seller explicitly opens negotiation, offers discount, or counter-proposes a price",
		"- \"firm\": seller states a price floor, says no negotiation, or explicitly refuses",
		"- \"low_signal\": reply is too short, ambiguous, off-topic, automated, or missing meaningful content",
		"- \"risky\": reply contains urgency pressure, claims of other buyers, suspicious payment requests, or scam patterns",
		"",
		"Confidence rules:",
		"- \"high\": unambiguous — explicit price AND clear stance stated",
		"- \"medium\": one clear signal, one inferred",
		"- \"low\": short, no price, mixed or unclear signals",
		"",
		"If confidence is \"low\", interpretation MUST be \"low_signal\".",
		"\"risky\" takes priority over all other interpretations.",
		"Never invent price data not present in the reply.",
		"Reply with JSON only.",
	}, "\n")
}

// detectLang detects the language from the seller reply text first, then falls
// back to the listing title. Detection order: BG (Cyrillic >30% of letter
// runes) → NL (Dutch stop-words) → EN (default).
//
// The seller reply is checked first because that's the most proximate signal —
// the reply may be in a different language than the listing itself.
func detectLang(sellerReply string, listing models.Listing) draftnote.Lang {
	// Try seller reply first.
	if sellerReply != "" {
		if isCyrillicDominant(sellerReply) {
			return draftnote.LangBG
		}
		if containsDutchStopWord(sellerReply) {
			return draftnote.LangNL
		}
	}

	// Fall back to listing title detection.
	titleSample := strings.ToLower(listing.Title)
	desc := listing.Description
	if len(desc) > 100 {
		desc = desc[:100]
	}
	titleSample += " " + strings.ToLower(desc)

	tokens := strings.FieldsFunc(titleSample, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == '/' ||
			r == '\n' || r == '\r' || r == '\t' || r == '(' || r == ')'
	})
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = struct{}{}
	}

	for _, w := range bgStopWords {
		if _, ok := tokenSet[w]; ok {
			return draftnote.LangBG
		}
	}
	for _, w := range dutchStopWords {
		if _, ok := tokenSet[w]; ok {
			return draftnote.LangNL
		}
	}

	return draftnote.LangEN
}

// isCyrillicDominant returns true when more than 30% of the letter runes in s
// are Cyrillic. This mirrors the detection intent in internal/draftnote.
func isCyrillicDominant(s string) bool {
	var letters, cyrillic int
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if r >= '\u0400' && r <= '\u04FF' {
				cyrillic++
			}
		}
	}
	if letters == 0 {
		return false
	}
	return float64(cyrillic)/float64(letters) > 0.30
}

// containsDutchStopWord returns true when s contains any Dutch stop word as a
// whole token.
func containsDutchStopWord(s string) bool {
	lower := strings.ToLower(s)
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == '/' ||
			r == '\n' || r == '\r' || r == '\t' || r == '(' || r == ')'
	})
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = struct{}{}
	}
	for _, w := range dutchStopWords {
		if _, ok := tokenSet[w]; ok {
			return true
		}
	}
	return false
}

// dutchStopWords is a minimal set used for Dutch language detection.
var dutchStopWords = []string{
	"de", "het", "een", "en", "van", "te", "in", "op", "dat", "is",
	"met", "voor", "zijn", "als", "er", "maar", "om", "bij", "ook",
	"naar", "dan", "niet", "dit", "ze", "aan", "heeft", "worden",
}

// bgStopWords is a minimal Cyrillic set for Bulgarian language detection.
var bgStopWords = []string{
	"на", "от", "за", "се", "ще", "или", "при", "без", "но", "по",
	"до", "те", "го", "ги", "им", "ни", "да", "не",
	"добро", "ново", "добра", "нова", "продавам", "купувам",
	"работи", "перфектно", "состояние", "включва", "батерия",
	"употребяван", "употребявана", "използван", "използвана",
}
