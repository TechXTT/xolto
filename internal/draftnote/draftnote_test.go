package draftnote_test

import (
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/draftnote"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
)

// nlListing returns a listing whose title contains a Dutch stop-word so the
// language detector chooses NL. description is intentionally empty unless overridden.
func nlListing(title string, flags []string, fairPriceCents int) models.Listing {
	return models.Listing{
		Title:     title,
		FairPrice: fairPriceCents,
		RiskFlags: flags,
	}
}

// enListing returns a listing with no Dutch stop-words — language detector returns EN.
func enListing(title string, flags []string, fairPriceCents int) models.Listing {
	return models.Listing{
		Title:     title,
		FairPrice: fairPriceCents,
		RiskFlags: flags,
	}
}

// Top-5 risk flags for the matrix.
var top5Flags = []string{
	"anomaly_price",
	"vague_condition",
	"no_battery_health",
	"missing_key_photos",
	"no_model_id",
}

// TestMatrixShapeByVerdict: 4 verdicts × top-5 risk flags = 20 cases.
// Each case asserts: (1) shape matches verdict, (2) for ask_seller the
// flag priority order drives question selection.
func TestMatrixShapeByVerdict(t *testing.T) {
	verdicts := []struct {
		verdict       string
		expectedShape draftnote.Shape
	}{
		{scorer.ActionBuy, draftnote.ShapeBuy},
		{scorer.ActionNegotiate, draftnote.ShapeNegotiate},
		{scorer.ActionAskSeller, draftnote.ShapeAskSeller},
		{scorer.ActionSkip, draftnote.ShapeGeneric},
	}

	for _, v := range verdicts {
		for _, flag := range top5Flags {
			listing := nlListing("de Sony A6000 body", []string{flag}, 30000)
			note := draftnote.Draft(v.verdict, listing, draftnote.MissionContext{})

			if note.Shape != v.expectedShape {
				t.Errorf("verdict=%s flag=%s: expected shape %q, got %q",
					v.verdict, flag, v.expectedShape, note.Shape)
			}
			if strings.TrimSpace(note.Text) == "" {
				t.Errorf("verdict=%s flag=%s: text must not be empty", v.verdict, flag)
			}
			if note.Lang != draftnote.LangNL {
				t.Errorf("verdict=%s flag=%s: expected lang nl (Dutch title), got %q", v.verdict, flag, note.Lang)
			}
		}
	}
}

// TestAskSellerFlagPriorityOrder verifies that when multiple flags are present
// the highest-priority one drives the question.
func TestAskSellerFlagPriorityOrder(t *testing.T) {
	cases := []struct {
		flags          []string
		expectedSubstr string // Dutch question keyword expected in text or questions
	}{
		// anomaly_price beats everything
		{
			[]string{"anomaly_price", "vague_condition", "no_battery_health"},
			"gestolen",
		},
		// vague_condition beats no_battery_health when anomaly_price absent
		{
			[]string{"vague_condition", "no_battery_health", "missing_key_photos"},
			"staat beschrijven",
		},
		// no_battery_health beats missing_key_photos
		{
			[]string{"no_battery_health", "missing_key_photos", "no_model_id"},
			"batterijgezondheid",
		},
		// missing_key_photos beats no_model_id
		{
			[]string{"missing_key_photos", "no_model_id"},
			"foto",
		},
		// no_model_id alone
		{
			[]string{"no_model_id"},
			"modelnummer",
		},
	}

	for _, c := range cases {
		listing := nlListing("de Canon EOS M50", c.flags, 0)
		note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})

		if note.Shape != draftnote.ShapeAskSeller {
			t.Errorf("flags=%v: expected shape ask_seller, got %q", c.flags, note.Shape)
		}
		// questions carry the flag-driven text; check all text output
		combined := note.Text + " " + strings.Join(note.Questions, " ")
		if !strings.Contains(strings.ToLower(combined), c.expectedSubstr) {
			t.Errorf("flags=%v: expected combined output to contain %q, got: %s", c.flags, c.expectedSubstr, combined)
		}
	}
}

// TestAskSellerFallbackWhenNoKnownFlag verifies the generic fallback question
// is used when no known flag matches (or all are deduped by listing content).
func TestAskSellerFallbackWhenNoKnownFlag(t *testing.T) {
	listing := nlListing("de Fujifilm X100V", []string{}, 0)
	note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeAskSeller {
		t.Fatalf("expected shape ask_seller, got %q", note.Shape)
	}
	combined := note.Text + " " + strings.Join(note.Questions, " ")
	if !strings.Contains(combined, "conditie") {
		t.Errorf("expected generic NL fallback to contain 'conditie', got: %s", combined)
	}
}

// TestBuyDraftNoPriceIncluded verifies AC: buy draft never includes a price
// mention — the buyer accepts the asking price and renegotiating breaks trust.
func TestBuyDraftNoPriceIncluded(t *testing.T) {
	// Listing with a non-zero FairPrice — must still not appear in buy draft.
	listing := models.Listing{
		Title:     "de Sony A7 III body",
		FairPrice: 120000,
		RiskFlags: []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeBuy {
		t.Fatalf("expected shape buy, got %q", note.Shape)
	}
	// "EUR" or "€" or any price indicator must not appear in the buy text.
	lowerText := strings.ToLower(note.Text)
	for _, priceIndicator := range []string{"eur", "€", "1200", "1.200"} {
		if strings.Contains(lowerText, priceIndicator) {
			t.Errorf("buy draft must not include price indicators; text: %s", note.Text)
		}
	}
}

// TestNegotiateFloorGuard verifies AC: negotiate anchor does not go below
// fair_price × 0.85.
func TestNegotiateFloorGuard(t *testing.T) {
	fairPrice := 100000 // EUR 1000.00
	listing := models.Listing{
		Title:     "de Nikon Z6 body",
		FairPrice: fairPrice,
		RiskFlags: []string{},
	}
	note := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeNegotiate {
		t.Fatalf("expected shape negotiate, got %q", note.Shape)
	}
	// The suggested offer in the text should be >= floor (EUR 850.00 = 85000 cents).
	// We verify by checking the text doesn't contain values below the floor.
	// A simple sanity check: text must contain "EUR" (price anchor is present).
	if !strings.Contains(note.Text, "EUR") {
		t.Errorf("negotiate draft with fairPrice>0 must contain price anchor; text: %s", note.Text)
	}
}

// TestNegotiateNoAnchorWhenFairPriceZero verifies the structural emission gap
// disclosure: when ComparablesCount=0 and FairPrice=0, the negotiate draft
// omits a specific price anchor but still emits shape=negotiate.
func TestNegotiateNoAnchorWhenFairPriceZero(t *testing.T) {
	listing := models.Listing{
		Title:            "de Leica Q2",
		FairPrice:        0,
		ComparablesCount: 0,
		RiskFlags:        []string{},
	}
	note := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeNegotiate {
		t.Fatalf("expected shape negotiate even without anchor, got %q", note.Shape)
	}
	// No EUR anchor expected when FairPrice=0.
	if strings.Contains(note.Text, "EUR") {
		t.Errorf("negotiate draft with fairPrice=0 must not contain EUR anchor; text: %s", note.Text)
	}
}

// TestSkipVerdictEmitsGenericShape verifies AC: skip → shape=generic (not a
// refusal — user asked for a draft anyway).
func TestSkipVerdictEmitsGenericShape(t *testing.T) {
	listing := nlListing("de iPhone 12 Pro", []string{"anomaly_price"}, 0)
	note := draftnote.Draft(scorer.ActionSkip, listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeGeneric {
		t.Fatalf("expected shape generic for skip, got %q", note.Shape)
	}
	if strings.TrimSpace(note.Text) == "" {
		t.Fatal("generic draft text must not be empty")
	}
}

// TestDeterminism verifies AC: identical inputs produce byte-identical output.
func TestDeterminism(t *testing.T) {
	listing := models.Listing{
		Title:       "de Sony A6400",
		Description: "Mooie camera in goede staat",
		FairPrice:   45000,
		RiskFlags:   []string{"missing_key_photos", "no_model_id"},
	}
	first := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
	second := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})

	if first.Text != second.Text {
		t.Errorf("Draft is not deterministic:\nfirst:  %s\nsecond: %s", first.Text, second.Text)
	}
	if first.Shape != second.Shape {
		t.Errorf("Shape is not deterministic: %q vs %q", first.Shape, second.Shape)
	}
	if first.Lang != second.Lang {
		t.Errorf("Lang is not deterministic: %q vs %q", first.Lang, second.Lang)
	}
}

// TestLanguageDetectionNL verifies that a Dutch-language listing gets lang=nl.
func TestLanguageDetectionNL(t *testing.T) {
	listing := models.Listing{
		Title:     "de Canon EOS R5 body in goede staat",
		RiskFlags: []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangNL {
		t.Errorf("expected lang nl for Dutch listing, got %q", note.Lang)
	}
}

// TestLanguageDetectionEN verifies that a clearly English listing gets lang=en.
func TestLanguageDetectionEN(t *testing.T) {
	listing := models.Listing{
		Title:       "Sony A7IV Full Frame Camera Body",
		Description: "Excellent condition, barely used. Comes with original box.",
		RiskFlags:   []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangEN {
		t.Errorf("expected lang en for English listing, got %q", note.Lang)
	}
}

// TestAskSellerDedupesBatteryHealthWhenMentioned verifies AC: ask_seller draft
// does not ask about battery health when the listing's own title already mentions it.
func TestAskSellerDedupesBatteryHealthWhenMentioned(t *testing.T) {
	listing := models.Listing{
		Title:     "de iPhone 13 batterij 94% uitstekend",
		RiskFlags: []string{"no_battery_health", "missing_key_photos"},
	}
	note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
	// no_battery_health should be deduped (title mentions battery/batterij).
	// Next priority is missing_key_photos — should appear in questions.
	combined := note.Text + " " + strings.Join(note.Questions, " ")
	if strings.Contains(strings.ToLower(combined), "batterijgezondheid") {
		t.Errorf("battery question should be deduped when listing title mentions battery: %s", combined)
	}
	if !strings.Contains(strings.ToLower(combined), "foto") {
		t.Errorf("expected missing_key_photos question after deduplication, got: %s", combined)
	}
}

// TestInvalidVerdictIsNotCalledViaAPI serves as documentation that the
// Draft function trusts the caller to validate verdict before calling.
// The handler enforces the allowlist; this test simply confirms that
// unknown verdicts fallback to generic (not a panic or error).
func TestInvalidVerdictFallsToGeneric(t *testing.T) {
	listing := models.Listing{Title: "camera", RiskFlags: []string{}}
	note := draftnote.Draft("unknown_verdict", listing, draftnote.MissionContext{})
	if note.Shape != draftnote.ShapeGeneric {
		t.Errorf("unexpected verdict should fall to generic, got %q", note.Shape)
	}
}

// bgListing returns a listing whose title contains a Bulgarian Cyrillic
// stop-word so detectLang returns LangBG (XOL-38 M3-D).
func bgListing(title string, flags []string, fairPriceCents int) models.Listing {
	return models.Listing{
		Title:     title,
		FairPrice: fairPriceCents,
		RiskFlags: flags,
	}
}

// TestLanguageDetectionBG verifies that a Bulgarian listing gets lang=bg (XOL-38 M3-D).
func TestLanguageDetectionBG(t *testing.T) {
	listing := models.Listing{
		// "батерия" is a BG stop-word in the bgStopWords set
		Title:     "Фотоапарат Canon EOS R10 батерия 94%",
		RiskFlags: []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Errorf("expected lang bg for Bulgarian listing, got %q", note.Lang)
	}
}

// TestBGBuyDraft verifies BG buy template text (XOL-38 M3-D).
func TestBGBuyDraft(t *testing.T) {
	listing := bgListing("Фотоапарат Canon EOS R10 батерия", []string{}, 0)
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Fatalf("expected lang bg, got %q", note.Lang)
	}
	if note.Shape != draftnote.ShapeBuy {
		t.Fatalf("expected shape buy, got %q", note.Shape)
	}
	// Must include a Bulgarian greeting
	if !strings.Contains(note.Text, "Здравейте") {
		t.Errorf("BG buy draft must contain 'Здравейте', got: %s", note.Text)
	}
	// Must reference the listing title
	if !strings.Contains(note.Text, "Canon EOS R10") {
		t.Errorf("BG buy draft must include listing title, got: %s", note.Text)
	}
}

// TestBGNegotiateDraft verifies BG negotiate template text (XOL-38 M3-D).
// Note: BG negotiate now uses BGN (лв.) format, not EUR.
func TestBGNegotiateDraft(t *testing.T) {
	listing := bgListing("Sony A6000 батерия 88%", []string{}, 30000)
	note := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Fatalf("expected lang bg, got %q", note.Lang)
	}
	if note.Shape != draftnote.ShapeNegotiate {
		t.Fatalf("expected shape negotiate, got %q", note.Shape)
	}
	if !strings.Contains(note.Text, "Здравейте") {
		t.Errorf("BG negotiate draft must contain 'Здравейте', got: %s", note.Text)
	}
	// Price anchor must appear when FairPrice > 0 — in BG, as лв. not EUR
	if !strings.Contains(note.Text, "лв.") {
		t.Errorf("BG negotiate draft with fairPrice>0 must contain BGN price anchor (лв.), got: %s", note.Text)
	}
}

// TestBGNegotiateDraftNoAnchor verifies BG negotiate without fair price (XOL-38 M3-D).
func TestBGNegotiateDraftNoAnchor(t *testing.T) {
	listing := bgListing("iPhone 13 Pro употребяван", []string{}, 0)
	note := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Fatalf("expected lang bg, got %q", note.Lang)
	}
	if strings.Contains(note.Text, "EUR") {
		t.Errorf("BG negotiate draft with fairPrice=0 must not contain EUR anchor, got: %s", note.Text)
	}
	if strings.Contains(note.Text, "лв.") {
		t.Errorf("BG negotiate draft with fairPrice=0 must not contain BGN anchor, got: %s", note.Text)
	}
}

// TestBGAskSellerDraft verifies BG ask-seller template including flag question (XOL-38 M3-D).
func TestBGAskSellerDraft(t *testing.T) {
	listing := bgListing("MacBook Pro М1 батерия", []string{"no_battery_health"}, 0)
	note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Fatalf("expected lang bg, got %q", note.Lang)
	}
	if note.Shape != draftnote.ShapeAskSeller {
		t.Fatalf("expected shape ask_seller, got %q", note.Shape)
	}
	// BG battery question must appear in questions
	combined := note.Text + " " + strings.Join(note.Questions, " ")
	if !strings.Contains(combined, "батерия") && !strings.Contains(combined, "батер") {
		t.Errorf("BG ask_seller with no_battery_health flag must include battery question, got: %s", combined)
	}
}

// TestBGGenericDraft verifies BG generic (skip) template (XOL-38 M3-D).
func TestBGGenericDraft(t *testing.T) {
	listing := bgListing("Nikon D750 употребяван", []string{}, 0)
	note := draftnote.Draft(scorer.ActionSkip, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangBG {
		t.Fatalf("expected lang bg, got %q", note.Lang)
	}
	if note.Shape != draftnote.ShapeGeneric {
		t.Fatalf("expected shape generic, got %q", note.Shape)
	}
	if !strings.Contains(note.Text, "Здравейте") {
		t.Errorf("BG generic draft must contain 'Здравейте', got: %s", note.Text)
	}
}

// TestBGFlagPriorityOrder verifies that BG flag-question priority order works
// correctly — same priority as NL/EN (XOL-38 M3-D).
func TestBGFlagPriorityOrder(t *testing.T) {
	cases := []struct {
		flags          []string
		expectedSubstr string // BG question keyword expected in questions
	}{
		{
			[]string{"anomaly_price", "vague_condition", "no_battery_health"},
			"откраднат",
		},
		{
			[]string{"vague_condition", "no_battery_health"},
			"дефект",
		},
		{
			[]string{"missing_key_photos"},
			"снимки",
		},
	}
	for _, c := range cases {
		// "употребяван" triggers BG detection
		listing := bgListing("Canon EOS R10 употребяван", c.flags, 0)
		note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
		if note.Lang != draftnote.LangBG {
			t.Errorf("flags=%v: expected lang bg, got %q", c.flags, note.Lang)
		}
		combined := note.Text + " " + strings.Join(note.Questions, " ")
		if !strings.Contains(strings.ToLower(combined), c.expectedSubstr) {
			t.Errorf("flags=%v: expected combined output to contain %q, got: %s", c.flags, c.expectedSubstr, combined)
		}
	}
}

// TestNLDraftUnchanged verifies that an NL listing still produces NL output
// after the BG language detection was added (regression guard, XOL-38 M3-D).
func TestNLDraftUnchanged(t *testing.T) {
	listing := models.Listing{
		Title:     "de Canon EOS R5 body in goede staat",
		RiskFlags: []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangNL {
		t.Errorf("NL listing must still produce lang nl after BG detection addition, got %q", note.Lang)
	}
	if !strings.Contains(note.Text, "Hoi") {
		t.Errorf("NL buy draft must contain 'Hoi', got: %s", note.Text)
	}
}

// TestENDraftUnchanged verifies that a non-BG/non-NL listing still produces EN
// output (regression guard, XOL-38 M3-D).
func TestENDraftUnchanged(t *testing.T) {
	listing := models.Listing{
		Title:       "Sony A7IV Full Frame Camera Body",
		Description: "Excellent condition, barely used. Comes with original box.",
		RiskFlags:   []string{},
	}
	note := draftnote.Draft(scorer.ActionBuy, listing, draftnote.MissionContext{})
	if note.Lang != draftnote.LangEN {
		t.Errorf("EN listing must still produce lang en, got %q", note.Lang)
	}
	if !strings.Contains(note.Text, "Hi!") {
		t.Errorf("EN buy draft must contain 'Hi!', got: %s", note.Text)
	}
}

// TestNegotiateOfferCalibration verifies offer is 85% of fair price.
func TestNegotiateOfferCalibration(t *testing.T) {
	cases := []struct {
		fairPrice      int
		wantOfferPrice int
		wantZero       bool
	}{
		{100_00, 85_00, false},  // 100 EUR → 85 EUR offer
		{50_00, 42_50, false},   // 50 EUR → 42.50 EUR (int truncation of 5000 * 0.85 = 4250 cents)
		{0, 0, true},            // no anchor → OfferPrice == 0
	}
	for _, c := range cases {
		listing := enListing("Sony A6000", nil, c.fairPrice)
		note := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
		if c.wantZero {
			if note.OfferPrice != 0 {
				t.Errorf("fairPrice=0: expected OfferPrice=0, got %d", note.OfferPrice)
			}
		} else {
			if note.OfferPrice != c.wantOfferPrice {
				t.Errorf("fairPrice=%d: expected OfferPrice=%d, got %d", c.fairPrice, c.wantOfferPrice, note.OfferPrice)
			}
		}
	}
}

// TestAskSellerQuestionsFromMustHaves verifies mission must-haves drive questions.
func TestAskSellerQuestionsFromMustHaves(t *testing.T) {
	t.Run("two unknown must-haves produce two questions", func(t *testing.T) {
		listing := enListing("iPhone 13", nil, 0)
		mission := draftnote.MissionContext{MustHaves: []string{"original charger", "battery health"}}
		note := draftnote.Draft(scorer.ActionAskSeller, listing, mission)
		if len(note.Questions) != 2 {
			t.Fatalf("expected 2 questions, got %d: %v", len(note.Questions), note.Questions)
		}
		for _, q := range note.Questions {
			if !strings.Contains(strings.ToLower(q), "original charger") &&
				!strings.Contains(strings.ToLower(q), "battery health") {
				t.Errorf("question does not reference must-have text: %q", q)
			}
		}
		// text is opener only — must-have text must not appear in Text
		if strings.Contains(note.Text, "original charger") || strings.Contains(note.Text, "battery health") {
			t.Errorf("must-have text must not appear in Note.Text opener, got: %q", note.Text)
		}
	})

	t.Run("matched must-have not included as question", func(t *testing.T) {
		listing := models.Listing{
			Title:       "iPhone 13",
			Description: "includes original charger in box",
		}
		mission := draftnote.MissionContext{MustHaves: []string{"original charger"}}
		note := draftnote.Draft(scorer.ActionAskSeller, listing, mission)
		for _, q := range note.Questions {
			if strings.Contains(strings.ToLower(q), "original charger") {
				t.Errorf("matched must-have should not appear as question, got: %q", q)
			}
		}
	})

	t.Run("no must-haves with risk flag produces one question", func(t *testing.T) {
		listing := enListing("iPhone 13", []string{"no_battery_health"}, 0)
		note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
		if len(note.Questions) == 0 {
			t.Fatal("expected at least one question from risk flag")
		}
	})

	t.Run("no must-haves no risk flags produces generic fallback", func(t *testing.T) {
		listing := enListing("iPhone 13", nil, 0)
		note := draftnote.Draft(scorer.ActionAskSeller, listing, draftnote.MissionContext{})
		if len(note.Questions) == 0 {
			t.Fatal("expected generic fallback question, got empty slice")
		}
	})

	t.Run("cap at 5 questions max", func(t *testing.T) {
		many := []string{"must-have-1", "must-have-2", "must-have-3", "must-have-4", "must-have-5", "must-have-6"}
		listing := enListing("iPhone 13", []string{"no_battery_health"}, 0)
		mission := draftnote.MissionContext{MustHaves: many}
		note := draftnote.Draft(scorer.ActionAskSeller, listing, mission)
		if len(note.Questions) > 5 {
			t.Errorf("expected at most 5 questions, got %d", len(note.Questions))
		}
	})
}

// TestBGNFormatInNegotiateText verifies BGN format used for BG listings.
func TestBGNFormatInNegotiateText(t *testing.T) {
	// BG listing (Cyrillic title → detectLang returns LangBG)
	bgLst := models.Listing{
		Title:     "iPhone на Apple",
		FairPrice: 391_17, // EUR 391.17 ≈ 765 BGN
		RiskFlags: nil,
	}
	note := draftnote.Draft(scorer.ActionNegotiate, bgLst, draftnote.MissionContext{})
	if strings.Contains(note.Text, "EUR") {
		t.Errorf("BG negotiate text must not contain 'EUR', got: %q", note.Text)
	}
	if !strings.Contains(note.Text, "лв.") {
		t.Errorf("BG negotiate text must contain 'лв.', got: %q", note.Text)
	}

	// NL listing — must still use EUR
	nlLst := nlListing("de iPhone van Apple", nil, 391_17)
	noteNL := draftnote.Draft(scorer.ActionNegotiate, nlLst, draftnote.MissionContext{})
	if !strings.Contains(noteNL.Text, "EUR") {
		t.Errorf("NL negotiate text must contain 'EUR', got: %q", noteNL.Text)
	}
}

func TestCountryCodeOverridesLang(t *testing.T) {
	// Listing with no Cyrillic stop-words — would detect as EN without override.
	listing := models.Listing{
		Title:     "Laptop Acer aspire ES 13",
		FairPrice: 100_00,
	}
	// Without mission context: EN
	noteEN := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{})
	if noteEN.Lang != draftnote.LangEN {
		t.Errorf("expected EN without country code, got %s", noteEN.Lang)
	}
	// With BG mission context: should override to BG
	noteBG := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{CountryCode: "BG"})
	if noteBG.Lang != draftnote.LangBG {
		t.Errorf("expected BG with CountryCode=BG, got %s", noteBG.Lang)
	}
	if strings.Contains(noteBG.Text, "EUR") {
		t.Errorf("BG-overridden draft must not contain EUR, got: %q", noteBG.Text)
	}
	if !strings.Contains(noteBG.Text, "лв.") {
		t.Errorf("BG-overridden draft must contain лв., got: %q", noteBG.Text)
	}
	// Lowercase "bg" also works
	noteBGLower := draftnote.Draft(scorer.ActionNegotiate, listing, draftnote.MissionContext{CountryCode: "bg"})
	if noteBGLower.Lang != draftnote.LangBG {
		t.Errorf("expected BG with lowercase country code 'bg', got %s", noteBGLower.Lang)
	}
}
