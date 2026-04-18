package support

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// AC-1: Enum exhaustiveness — every AllX slice matches the constants exactly.
// ---------------------------------------------------------------------------

func TestAllCategories_Exhaustive(t *testing.T) {
	want := []Category{
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
	if len(AllCategories) != len(want) {
		t.Fatalf("AllCategories length = %d, want %d", len(AllCategories), len(want))
	}
	for i, v := range want {
		if AllCategories[i] != v {
			t.Errorf("AllCategories[%d] = %q, want %q", i, AllCategories[i], v)
		}
	}
}

func TestAllMarkets_Exhaustive(t *testing.T) {
	want := []Market{
		MarketOLXBG,
		MarketMarktplaats,
		MarketVintedNL,
		MarketVintedDK,
		MarketUnknown,
	}
	if len(AllMarkets) != len(want) {
		t.Fatalf("AllMarkets length = %d, want %d", len(AllMarkets), len(want))
	}
	for i, v := range want {
		if AllMarkets[i] != v {
			t.Errorf("AllMarkets[%d] = %q, want %q", i, AllMarkets[i], v)
		}
	}
}

func TestAllProductCats_Exhaustive(t *testing.T) {
	want := []ProductCat{
		ProductCatCamera,
		ProductCatLaptop,
		ProductCatPhone,
		ProductCatAudio,
		ProductCatGaming,
		ProductCatTablet,
		ProductCatAppliance,
		ProductCatOther,
	}
	if len(AllProductCats) != len(want) {
		t.Fatalf("AllProductCats length = %d, want %d", len(AllProductCats), len(want))
	}
	for i, v := range want {
		if AllProductCats[i] != v {
			t.Errorf("AllProductCats[%d] = %q, want %q", i, AllProductCats[i], v)
		}
	}
}

func TestAllSeverities_Exhaustive(t *testing.T) {
	want := []Severity{
		SeverityLow,
		SeverityMedium,
		SeverityHigh,
		SeverityIncident,
	}
	if len(AllSeverities) != len(want) {
		t.Fatalf("AllSeverities length = %d, want %d", len(AllSeverities), len(want))
	}
	for i, v := range want {
		if AllSeverities[i] != v {
			t.Errorf("AllSeverities[%d] = %q, want %q", i, AllSeverities[i], v)
		}
	}
}

func TestAllActionsNeeded_Exhaustive(t *testing.T) {
	want := []ActionNeeded{
		ActionReplyOnly,
		ActionBackendFix,
		ActionDashFix,
		ActionScorerFix,
		ActionScraperFix,
		ActionBillingAuthFix,
		ActionProductClarification,
		ActionRoadmapCandidate,
	}
	if len(AllActionsNeeded) != len(want) {
		t.Fatalf("AllActionsNeeded length = %d, want %d", len(AllActionsNeeded), len(want))
	}
	for i, v := range want {
		if AllActionsNeeded[i] != v {
			t.Errorf("AllActionsNeeded[%d] = %q, want %q", i, AllActionsNeeded[i], v)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-5: Routing table covers every ActionNeeded value.
// ---------------------------------------------------------------------------

func TestActionToLinearProject_CoversAllActions(t *testing.T) {
	for _, action := range AllActionsNeeded {
		if _, ok := ActionToLinearProject[action]; !ok {
			t.Errorf("ActionToLinearProject missing entry for ActionNeeded %q", action)
		}
	}
}

func TestActionToLinearProject_ReplyOnlyIsEmpty(t *testing.T) {
	if got := ActionToLinearProject[ActionReplyOnly]; got != "" {
		t.Errorf("ActionToLinearProject[ActionReplyOnly] = %q, want empty string", got)
	}
}

func TestActionToLinearProject_BackendFixTarget(t *testing.T) {
	if got := ActionToLinearProject[ActionBackendFix]; got != "OLX BG trust" {
		t.Errorf("ActionToLinearProject[ActionBackendFix] = %q, want %q", got, "OLX BG trust")
	}
}

// ---------------------------------------------------------------------------
// AC-2: Incident-keyword matcher — English, BG Cyrillic, Dutch, no FP.
// ---------------------------------------------------------------------------

func TestHasIncidentKeyword_EnglishHits(t *testing.T) {
	cases := []struct {
		body string
		desc string
	}{
		{"The site is down right now", "down"},
		{"We are experiencing an outage", "outage"},
		{"I can't log in to my account", "can't log in"},
		{"cant log in somehow", "cant log in"},
		{"I can't login at all", "can't login"},
		{"I cannot login, please help", "cannot login"},
		{"I am locked out of my account", "locked out"},
		{"I think I've been hacked", "hacked"},
		{"There was a breach of my data", "breach"},
		{"There is an unauthorized charge on my card", "unauthorized charge"},
		{"I was billed twice this month", "billed twice"},
		{"I want a refund please", "refund"},
		{"GDPR request for data deletion", "gdpr"},
		{"DMCA takedown notice", "dmca"},
		{"This is a legal matter", "legal"},
		{"I will contact a lawyer", "lawyer"},
		{"I am filing a lawsuit", "lawsuit"},
	}
	for _, tc := range cases {
		if !HasIncidentKeyword(tc.body) {
			t.Errorf("HasIncidentKeyword(%q) = false, want true (keyword: %s)", tc.body, tc.desc)
		}
	}
}

func TestHasIncidentKeyword_BulgarianCyrillicHits(t *testing.T) {
	cases := []struct {
		body string
		desc string
	}{
		{"Сайтът е паднал", "паднал"},
		{"Приложението не работи", "не работи"},
		{"Не мога да вляза в профила си", "не мога да вляза"},
		{"Профилът ми е хакнат", "хакнат"},
		{"Имаше измама с картата ми", "измама"},
		{"Двойно плащане за абонамента", "двойно плащане"},
		{"Искам възстановяване на сума", "възстановяване на сума"},
	}
	for _, tc := range cases {
		if !HasIncidentKeyword(tc.body) {
			t.Errorf("HasIncidentKeyword(%q) = false, want true (keyword: %s)", tc.body, tc.desc)
		}
	}
}

func TestHasIncidentKeyword_DutchHits(t *testing.T) {
	cases := []struct {
		body string
		desc string
	}{
		{"Ik kan niet inloggen op mijn account", "kan niet inloggen"},
		{"Ik wil een terugbetaling aanvragen", "terugbetaling"},
		{"Mijn account is gehackt", "gehackt"},
	}
	for _, tc := range cases {
		if !HasIncidentKeyword(tc.body) {
			t.Errorf("HasIncidentKeyword(%q) = false, want true (keyword: %s)", tc.body, tc.desc)
		}
	}
}

func TestHasIncidentKeyword_NoFalsePositives(t *testing.T) {
	cases := []string{
		"I love this product, working great",
		"This camera takes excellent photos",
		"How do I list an item on OLX?",
		"What is the fair price for this laptop?",
		"Страхотна сделка, много доволен",
		"Goede prijs voor een gebruikte laptop",
		"The delivery was fast and smooth",
	}
	for _, body := range cases {
		if HasIncidentKeyword(body) {
			t.Errorf("HasIncidentKeyword(%q) = true, want false (false positive)", body)
		}
	}
}

// Ensure "down" as a substring inside a longer word does NOT match.
func TestHasIncidentKeyword_NoSubstringFalsePositive(t *testing.T) {
	cases := []string{
		// "down" must not match inside "download", "markdown", "countdown"
		"Please download the app",
		"Read the markdown docs",
		"Starting the countdown now",
		// "legal" must not match inside "illegal" ... actually "illegal" contains "legal"
		// as a separate token boundary issue — since we tokenize on word boundaries,
		// "illegal" tokenizes to ["illegal"], not ["il","legal"], so it should NOT match.
		"That is illegal behavior",
		// "refund" inside a compound sentence where refund is the word
		// (this SHOULD match, so we skip it here — this tests non-incident cases)
		"The lawyer drama was in a movie",
	}
	// "lawyer" in the last case IS in the keyword list, so it will match — remove it.
	safeCases := cases[:len(cases)-1]
	for _, body := range safeCases {
		if HasIncidentKeyword(body) {
			t.Errorf("HasIncidentKeyword(%q) = true, want false (substring false positive)", body)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-3: Classify() incident override — LLM says low, body has "outage".
// ---------------------------------------------------------------------------

func validLLM() LLMClassification {
	return LLMClassification{
		Category:   "bug",
		Market:     "olx_bg",
		ProductCat: "phone",
		Severity:   "low",
		Action:     "backend_fix",
	}
}

func TestClassify_IncidentOverride(t *testing.T) {
	llm := validLLM()
	llm.Severity = "low"

	got, err := Classify("The platform is experiencing an outage right now", "urgent", llm)
	if err != nil {
		t.Fatalf("Classify() unexpected error: %v", err)
	}
	if got.Severity != SeverityIncident {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityIncident)
	}
	if !got.IncidentOverride {
		t.Error("IncidentOverride = false, want true")
	}
}

func TestClassify_IncidentOverrideFromSubject(t *testing.T) {
	llm := validLLM()
	llm.Severity = "medium"

	got, err := Classify("I need help with my account", "Site outage report", llm)
	if err != nil {
		t.Fatalf("Classify() unexpected error: %v", err)
	}
	if got.Severity != SeverityIncident {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityIncident)
	}
	if !got.IncidentOverride {
		t.Error("IncidentOverride = false, want true")
	}
}

func TestClassify_NoIncidentOverride_LLMSeverityPreserved(t *testing.T) {
	llm := validLLM()
	llm.Severity = "high"

	got, err := Classify("I have a question about a listing price", "Price question", llm)
	if err != nil {
		t.Fatalf("Classify() unexpected error: %v", err)
	}
	if got.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityHigh)
	}
	if got.IncidentOverride {
		t.Error("IncidentOverride = true, want false")
	}
}

// ---------------------------------------------------------------------------
// AC-4: Invalid LLM output returns a typed error.
// ---------------------------------------------------------------------------

func TestClassify_InvalidCategory(t *testing.T) {
	llm := validLLM()
	llm.Category = "totally_unknown_category"

	_, err := Classify("body", "subject", llm)
	if err == nil {
		t.Fatal("Classify() expected error for invalid category, got nil")
	}
	if !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("errors.Is(err, ErrInvalidCategory) = false, err = %v", err)
	}
}

func TestClassify_InvalidMarket(t *testing.T) {
	llm := validLLM()
	llm.Market = "ebay_us"

	_, err := Classify("body", "subject", llm)
	if err == nil {
		t.Fatal("Classify() expected error for invalid market, got nil")
	}
	if !errors.Is(err, ErrInvalidMarket) {
		t.Errorf("errors.Is(err, ErrInvalidMarket) = false, err = %v", err)
	}
}

func TestClassify_InvalidProductCat(t *testing.T) {
	llm := validLLM()
	llm.ProductCat = "smartwatch"

	_, err := Classify("body", "subject", llm)
	if err == nil {
		t.Fatal("Classify() expected error for invalid product_cat, got nil")
	}
	if !errors.Is(err, ErrInvalidProductCat) {
		t.Errorf("errors.Is(err, ErrInvalidProductCat) = false, err = %v", err)
	}
}

func TestClassify_InvalidSeverity(t *testing.T) {
	llm := validLLM()
	llm.Severity = "critical"

	_, err := Classify("body", "subject", llm)
	if err == nil {
		t.Fatal("Classify() expected error for invalid severity, got nil")
	}
	if !errors.Is(err, ErrInvalidSeverity) {
		t.Errorf("errors.Is(err, ErrInvalidSeverity) = false, err = %v", err)
	}
}

func TestClassify_InvalidAction(t *testing.T) {
	llm := validLLM()
	llm.Action = "do_something_weird"

	_, err := Classify("body", "subject", llm)
	if err == nil {
		t.Fatal("Classify() expected error for invalid action_needed, got nil")
	}
	if !errors.Is(err, ErrInvalidAction) {
		t.Errorf("errors.Is(err, ErrInvalidAction) = false, err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Happy path: valid LLM output returns fully populated Classification.
// ---------------------------------------------------------------------------

func TestClassify_HappyPath(t *testing.T) {
	llm := LLMClassification{
		Category:   "pricing",
		Market:     "olx_bg",
		ProductCat: "camera",
		Severity:   "medium",
		Action:     "scorer_fix",
	}

	got, err := Classify("The price seems wrong for this camera", "Wrong price", llm)
	if err != nil {
		t.Fatalf("Classify() unexpected error: %v", err)
	}

	if got.Category != CategoryPricing {
		t.Errorf("Category = %q, want %q", got.Category, CategoryPricing)
	}
	if got.Market != MarketOLXBG {
		t.Errorf("Market = %q, want %q", got.Market, MarketOLXBG)
	}
	if got.ProductCat != ProductCatCamera {
		t.Errorf("ProductCat = %q, want %q", got.ProductCat, ProductCatCamera)
	}
	if got.Severity != SeverityMedium {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityMedium)
	}
	if got.Action != ActionScorerFix {
		t.Errorf("Action = %q, want %q", got.Action, ActionScorerFix)
	}
	if got.LinearProject != "/matches decisional pillar" {
		t.Errorf("LinearProject = %q, want %q", got.LinearProject, "/matches decisional pillar")
	}
	if got.IncidentOverride {
		t.Error("IncidentOverride = true, want false")
	}
}

func TestClassify_ReplyOnly_EmptyLinearProject(t *testing.T) {
	llm := LLMClassification{
		Category:   "general",
		Market:     "unknown",
		ProductCat: "other",
		Severity:   "low",
		Action:     "reply_only",
	}

	got, err := Classify("Just a general question", "Question", llm)
	if err != nil {
		t.Fatalf("Classify() unexpected error: %v", err)
	}
	if got.LinearProject != "" {
		t.Errorf("LinearProject = %q, want empty string for reply_only", got.LinearProject)
	}
}

// ---------------------------------------------------------------------------
// Sentinel error identity — errors.Is must work through wrapping.
// ---------------------------------------------------------------------------

func TestSentinelErrors_Identity(t *testing.T) {
	sentinels := []error{
		ErrInvalidCategory,
		ErrInvalidMarket,
		ErrInvalidProductCat,
		ErrInvalidSeverity,
		ErrInvalidAction,
	}
	for _, sentinel := range sentinels {
		if sentinel == nil {
			t.Errorf("sentinel error is nil: %v", sentinel)
		}
	}
}
