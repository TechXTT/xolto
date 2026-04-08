# MarktBot — Product Coherence Refactor (Implementation-Ready)

This plan is written for direct handoff to an AI coding agent. Every change is specified with exact file paths, exact code to find and replace, and precise insertion points. Execute the steps in order — later steps depend on earlier ones.

---

## Context

MarktBot has five pages (Brief, Radar, Hunts, Saved, Settings) that don't form one workflow. Three concrete problems:

1. **Score data is lost on reload.** `SaveListing` only saves a `float64` score. Fair price, offer, confidence, and reason are never written to the DB. `ListRecentListings` returns bare listings with no analysis. The `ListingCard` in the feed shows score only from SSE events — and even the SSE handler discards the scored fields, passing only the nested `Listing` to state.

2. **Brief doesn't start monitoring.** `startBriefConversation` saves a profile, asks "want me to show matches?", and waits. The user must manually navigate to Hunts and create searches.

3. **Draft offer is fully built but unreachable.** `DraftSellerMessage` in `assistant.go` is complete. There is no API route to call it and no UI button.

Secondary improvements: nav rename (Brief → Deals → Saved → Settings), electronics-focused prompts, risk heuristics, email alerts.

---

## Step 1 — DB Migration: Add Analysis Columns to `listings` Table

### File: `internal/store/store.go`

In the `migrate()` function, after line 187:
```go
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN image_urls TEXT NOT NULL DEFAULT '[]'`)
```

Add these lines immediately after:
```go
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN url TEXT NOT NULL DEFAULT ''`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN condition TEXT NOT NULL DEFAULT ''`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN marketplace_id TEXT NOT NULL DEFAULT 'marktplaats'`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN fair_price INTEGER NOT NULL DEFAULT 0`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN offer_price INTEGER NOT NULL DEFAULT 0`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN confidence REAL NOT NULL DEFAULT 0`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''`)
_, _ = db.Exec(`ALTER TABLE listings ADD COLUMN risk_flags TEXT NOT NULL DEFAULT '[]'`)
```

### File: `internal/store/postgres.go`

In `migratePostgres()`, after line 179:
```go
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS image_urls TEXT NOT NULL DEFAULT '[]'`)
```

Add immediately after:
```go
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS url TEXT NOT NULL DEFAULT ''`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS condition TEXT NOT NULL DEFAULT ''`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS marketplace_id TEXT NOT NULL DEFAULT 'marktplaats'`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS fair_price INTEGER NOT NULL DEFAULT 0`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS offer_price INTEGER NOT NULL DEFAULT 0`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS confidence DOUBLE PRECISION NOT NULL DEFAULT 0`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS reasoning TEXT NOT NULL DEFAULT ''`)
_, _ = db.ExecContext(ctx, `ALTER TABLE listings ADD COLUMN IF NOT EXISTS risk_flags TEXT NOT NULL DEFAULT '[]'`)
```

---

## Step 2 — Go Models: Add Fields to `Listing` and `ScoredListing`

### File: `internal/models/listing.go`

**Change 1:** In the `Listing` struct (currently ends at `Attributes map[string]string`), add these fields after the `Attributes` line:
```go
// Analysis fields — zero-value when listing comes from a marketplace search;
// populated when loaded from the store (ListRecentListings).
Score      float64
FairPrice  int      // cents
OfferPrice int      // cents
Confidence float64
Reason     string
RiskFlags  []string
```

**Change 2:** In the `ScoredListing` struct (currently ends at `ComparableDeals []ComparableDeal`), add one field after `ComparableDeals`:
```go
RiskFlags []string
```

---

## Step 3 — Store Interface: Update `SaveListing` Signature

### File: `internal/store/iface.go`

Find line 31:
```go
SaveListing(userID string, l models.Listing, query string, score float64) error
```

Replace with:
```go
SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error
```

---

## Step 4 — SQLite Store: Update `SaveListing` and `ListRecentListings`

### File: `internal/store/store.go`

**Change 1:** Replace the entire `SaveListing` function (lines 725–737):

```go
// SaveListing stores or updates a listing and its scored analysis.
func (s *SQLiteStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, risk_flags,
			first_seen, last_seen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(item_id) DO UPDATE SET
			price         = excluded.price,
			score         = excluded.score,
			image_urls    = excluded.image_urls,
			url           = excluded.url,
			condition     = excluded.condition,
			marketplace_id = excluded.marketplace_id,
			fair_price    = excluded.fair_price,
			offer_price   = excluded.offer_price,
			confidence    = excluded.confidence,
			reasoning     = excluded.reasoning,
			risk_flags    = excluded.risk_flags,
			last_seen     = CURRENT_TIMESTAMP
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, string(riskFlagsJSON),
	)
	return err
}
```

**Change 2:** Replace the entire `ListRecentListings` function (lines 605–636):

```go
func (s *SQLiteStore) ListRecentListings(userID string, limit int) ([]models.Listing, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT item_id, title, price, price_type, image_urls,
		       url, condition, marketplace_id,
		       score, fair_price, offer_price, confidence, reasoning, risk_flags,
		       last_seen
		FROM listings
		WHERE item_id LIKE ?
		ORDER BY last_seen DESC
		LIMIT ?
	`, scopedItemPrefix(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var listings []models.Listing
	for rows.Next() {
		var listing models.Listing
		var imageURLsJSON, riskFlagsJSON, lastSeen string
		if err := rows.Scan(
			&listing.ItemID, &listing.Title, &listing.Price, &listing.PriceType, &imageURLsJSON,
			&listing.URL, &listing.Condition, &listing.MarketplaceID,
			&listing.Score, &listing.FairPrice, &listing.OfferPrice, &listing.Confidence,
			&listing.Reason, &riskFlagsJSON, &lastSeen,
		); err != nil {
			return nil, err
		}
		listing.ItemID = unscopedItemID(listing.ItemID)
		listing.CanonicalID = listing.MarketplaceID + ":" + listing.ItemID
		listing.Date, _ = parseSQLiteTime(lastSeen)
		_ = json.Unmarshal([]byte(imageURLsJSON), &listing.ImageURLs)
		_ = json.Unmarshal([]byte(riskFlagsJSON), &listing.RiskFlags)
		listings = append(listings, listing)
	}
	return listings, rows.Err()
}
```

---

## Step 5 — Postgres Store: Update `SaveListing` and `ListRecentListings`

### File: `internal/store/postgres.go`

**Change 1:** Replace the `SaveListing` function (lines 612–624):

```go
func (s *PostgresStore) SaveListing(userID string, l models.Listing, query string, scored models.ScoredListing) error {
	imageURLsJSON, _ := json.Marshal(l.ImageURLs)
	riskFlagsJSON, _ := json.Marshal(scored.RiskFlags)
	_, err := s.db.Exec(`
		INSERT INTO listings (
			item_id, title, price, price_type, score, query, image_urls,
			url, condition, marketplace_id,
			fair_price, offer_price, confidence, reasoning, risk_flags,
			first_seen, last_seen
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW(),NOW())
		ON CONFLICT(item_id) DO UPDATE SET
			price          = EXCLUDED.price,
			score          = EXCLUDED.score,
			image_urls     = EXCLUDED.image_urls,
			url            = EXCLUDED.url,
			condition      = EXCLUDED.condition,
			marketplace_id = EXCLUDED.marketplace_id,
			fair_price     = EXCLUDED.fair_price,
			offer_price    = EXCLUDED.offer_price,
			confidence     = EXCLUDED.confidence,
			reasoning      = EXCLUDED.reasoning,
			risk_flags     = EXCLUDED.risk_flags,
			last_seen      = NOW()
	`,
		scopedItemID(userID, l.ItemID), l.Title, l.Price, l.PriceType, scored.Score, query, string(imageURLsJSON),
		l.URL, l.Condition, l.MarketplaceID,
		scored.FairPrice, scored.OfferPrice, scored.Confidence, scored.Reason, string(riskFlagsJSON),
	)
	return err
}
```

**Change 2:** Find `ListRecentListings` in `postgres.go` and replace it with the same logic as the SQLite version above, substituting `$1` / `$2` for `?` / `?` and `NOW()` instead of `CURRENT_TIMESTAMP` where applicable. The SELECT, column list, and Scan call are identical to the SQLite version.

---

## Step 6 — Scorer: Add `computeRiskFlags` and Assign to Result

### File: `internal/scorer/scorer.go`

**Change 1:** Add the following function at the bottom of the file, before the closing of the package (after `hasActionablePrice`):

```go
// computeRiskFlags returns a slice of trust-signal flags for a listing.
// Called after fair price is known so anomaly detection can compare.
func computeRiskFlags(listing models.Listing, fairPrice int) []string {
	var flags []string
	lower := strings.ToLower(listing.Title + " " + listing.Description)

	// Price anomaly: asking price is less than half the estimated fair price.
	if fairPrice > 0 && listing.Price > 0 && listing.Price < fairPrice/2 {
		flags = append(flags, "anomaly_price")
	}

	// Vague or risky condition language.
	vagueTerms := []string{"as is", "as-is", "untested", "for parts", "sold as seen", "no returns", "working condition", "not working"}
	for _, term := range vagueTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "vague_condition")
			break
		}
	}

	// Unclear bundle — grouped items without clear individual specs.
	bundleTerms := []string{"bundle", " lot ", "complete set", "collection"}
	for _, term := range bundleTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "unclear_bundle")
			break
		}
	}

	// Electronics listing with no model identifier (no digits in title).
	if isElectronicsListing(listing.Title) {
		hasDigit := false
		for _, c := range listing.Title {
			if c >= '0' && c <= '9' {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			flags = append(flags, "no_model_id")
		}
	}

	return flags
}

func isElectronicsListing(title string) bool {
	lower := strings.ToLower(title)
	keywords := []string{
		"camera", "lens", "laptop", "macbook", "iphone", "ipad", "samsung", "pixel",
		"sony", "nikon", "canon", "fuji", "fujifilm", "gpu", "cpu", "graphics card",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
```

**Change 2:** In the `Score` function, after line 137 (`score = clamp(score, 1, 10)`), add:
```go
riskFlags := computeRiskFlags(listing, analysis.FairPrice)
```

**Change 3:** In the `return models.ScoredListing{...}` at lines 145–156, add the `RiskFlags` field:
```go
return models.ScoredListing{
	Listing:         listing,
	Score:           score,
	OfferPrice:      offerPrice,
	FairPrice:       analysis.FairPrice,
	MarketAverage:   marketAvg,
	Confidence:      analysis.Confidence,
	Reason:          reason.String(),
	ReasoningSource: analysis.Source,
	SearchAdvice:    analysis.SearchAdvice,
	ComparableDeals: analysis.ComparableDeals,
	RiskFlags:       riskFlags,
}
```

---

## Step 7 — Worker: Pass `scored` to `SaveListing`

### File: `internal/worker/worker.go`

**Change 1:** On line 47, replace:
```go
_ = w.db.SaveListing(spec.UserID, listing, spec.Query, scored.Score)
```
With:
```go
_ = w.db.SaveListing(spec.UserID, listing, spec.Query, scored)
```

**Change 2:** In the `UserWorker` struct (lines 16–23), add one field after `notifier`:
```go
emailNotifier *notify.EmailNotifier
```

The updated struct:
```go
type UserWorker struct {
	specs         []models.SearchSpec
	db            store.Store
	registry      *marketplace.Registry
	scorer        *scorer.Scorer
	notifier      notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore      float64
}
```

**Change 3:** In `RunCycle`, after the `w.notifier.Publish(...)` block (lines 56–63), add:
```go
if w.emailNotifier != nil && w.emailNotifier.Enabled() {
	if user, err := w.db.GetUserByID(spec.UserID); err == nil && user != nil && user.Email != "" {
		_ = w.emailNotifier.SendDealAlert(user.Email, listing, scored.Score)
	}
}
```

---

## Step 8 — Worker Pool: Propagate `emailNotifier`

### File: `internal/worker/pool.go`

**Change 1:** In the `Pool` struct (lines 17–27), add one field after `notifier`:
```go
emailNotifier *notify.EmailNotifier
```

**Change 2:** Add a setter method (insert anywhere in pool.go outside other functions):
```go
// SetEmailNotifier configures an optional email notifier for deal alerts.
func (p *Pool) SetEmailNotifier(e *notify.EmailNotifier) {
	p.emailNotifier = e
}
```

**Change 3:** In `RunAllNow` (lines 70–77), update the `UserWorker` literal to include `emailNotifier`:
```go
w := &UserWorker{
	specs:         due,
	db:            p.db,
	registry:      p.registry,
	scorer:        p.scorer,
	notifier:      p.notifier,
	emailNotifier: p.emailNotifier,
	minScore:      p.minScore,
}
```

**Change 4:** In `RunUserNow` (lines 96–103), make the same `emailNotifier` addition to the `UserWorker` literal.

---

## Step 9 — Email Notifier: New File

### File: `internal/notify/email.go` (CREATE)

```go
package notify

import (
	"fmt"
	"net/smtp"

	"github.com/TechXTT/marktbot/internal/models"
)

// EmailNotifier sends deal alert emails via SMTP.
// All fields are optional; call Enabled() before use.
type EmailNotifier struct {
	host, port, user, pass, from string
}

// NewEmail returns an EmailNotifier. If host or user is empty, Enabled() returns false.
func NewEmail(host, port, user, pass, from string) *EmailNotifier {
	return &EmailNotifier{host: host, port: port, user: user, pass: pass, from: from}
}

// Enabled returns true when the notifier is configured and ready to send.
func (n *EmailNotifier) Enabled() bool {
	return n.host != "" && n.user != ""
}

// SendDealAlert sends a deal alert email to the given address.
func (n *EmailNotifier) SendDealAlert(to string, listing models.Listing, score float64) error {
	auth := smtp.PlainAuth("", n.user, n.pass, n.host)
	subject := "Strong deal found: " + listing.Title
	body := buildDealEmailHTML(listing, score)
	msg := "From: MarktBot <" + n.from + ">\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		body
	return smtp.SendMail(n.host+":"+n.port, auth, n.from, []string{to}, []byte(msg))
}

func buildDealEmailHTML(listing models.Listing, score float64) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family:Inter,sans-serif;background:#edf2ef;padding:32px">
<div style="max-width:560px;margin:0 auto;background:#fff;border-radius:16px;overflow:hidden">
  <div style="background:#0a1410;padding:24px 28px">
    <h1 style="color:#68e2b8;margin:0;font-size:1.1rem">MarktBot found a strong deal</h1>
  </div>
  <div style="padding:28px">
    <p style="font-size:1.1rem;font-weight:600;color:#081510;margin:0 0 8px">%s</p>
    <p style="font-size:1.4rem;font-weight:700;color:#0f8f67;margin:0 0 16px">Score: %.1f / 10</p>
    <a href="%s" style="display:inline-block;background:#0f8f67;color:#fff;padding:12px 24px;border-radius:12px;text-decoration:none;font-weight:600">View listing →</a>
  </div>
</div>
</body></html>`, listing.Title, score, listing.URL)
}
```

---

## Step 10 — Config: Add SMTP Fields

### File: `internal/config/env.go`

**Change 1:** Add these fields to `ServerConfig`:
```go
SMTPHost   string
SMTPPort   string
SMTPUser   string
SMTPPass   string
SMTPFrom   string
AlertScore float64
```

**Change 2:** Add these lines to `LoadServerConfigFromEnv` inside `cfg := ServerConfig{...}`:
```go
SMTPHost:   os.Getenv("SMTP_HOST"),
SMTPPort:   getenvDefault("SMTP_PORT", "587"),
SMTPUser:   os.Getenv("SMTP_USER"),
SMTPPass:   os.Getenv("SMTP_PASS"),
SMTPFrom:   getenvDefault("SMTP_FROM", "alerts@marktbot.app"),
AlertScore: parseFloatDefault(os.Getenv("ALERT_SCORE"), 8.0),
```

**Change 3:** Add this helper function at the bottom of the file:
```go
func parseFloatDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return def
	}
	return f
}
```

Add `"fmt"` to the import block.

---

## Step 11 — Main Server: Init and Wire Email Notifier

### File: `cmd/server/main.go`

Find where `worker.NewPool(...)` is called. After that call (on the returned `pool` value), add:
```go
emailNotifier := notify.NewEmail(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
pool.SetEmailNotifier(emailNotifier)
```

Add `"github.com/TechXTT/marktbot/internal/notify"` to the import block if not already present.

---

## Step 12 — Assistant: Auto-deploy Hunts from Brief

### File: `internal/assistant/assistant.go`

**Change 1:** Add these two helper functions anywhere in the file (before or after `searchConfigsForProfile`):

```go
// autoDeployHunts creates SearchSpec records for the user's brief profile.
// It skips any query+marketplace combination that already exists.
// Returns the count of newly created searches.
func (a *Assistant) autoDeployHunts(ctx context.Context, userID string, profile models.ShoppingProfile) (int, error) {
	user, err := a.store.GetUserByID(userID)
	if err != nil || user == nil {
		return 0, err
	}

	existing, _ := a.store.GetSearchConfigs(userID)
	existingKeys := make(map[string]bool, len(existing))
	for _, s := range existing {
		existingKeys[strings.ToLower(s.Query)+"|"+s.MarketplaceID] = true
	}

	queries := profile.SearchQueries
	if len(queries) == 0 && profile.TargetQuery != "" {
		queries = []string{profile.TargetQuery}
	}
	if len(queries) == 0 {
		queries = []string{profile.Name}
	}

	interval := intervalForTier(user.Tier)
	marketplaces := marketplacesForTier(user.Tier)

	count := 0
	for _, query := range queries {
		for _, mp := range marketplaces {
			key := strings.ToLower(query) + "|" + mp
			if existingKeys[key] {
				continue
			}
			spec := models.SearchSpec{
				UserID:          userID,
				Name:            profile.Name,
				Query:           query,
				MarketplaceID:   mp,
				MaxPrice:        profile.BudgetMax * 100, // BudgetMax is in whole euros
				Condition:       profile.PreferredCondition,
				CheckInterval:   interval,
				OfferPercentage: 72,
				Enabled:         true,
			}
			if _, err := a.store.CreateSearchConfig(spec); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func intervalForTier(tier string) time.Duration {
	switch tier {
	case "team":
		return time.Minute
	case "pro":
		return 5 * time.Minute
	default:
		return 30 * time.Minute
	}
}

func marketplacesForTier(tier string) []string {
	switch tier {
	case "team":
		return []string{"marktplaats", "vinted", "olxbg"}
	case "pro":
		return []string{"marktplaats", "vinted"}
	default:
		return []string{"marktplaats"}
	}
}
```

**Change 2:** Replace the second half of `startBriefConversation` — starting from `id, err := a.store.UpsertShoppingProfile(*profile)` (line 341) through the end of the function (line 358).

Replace this entire block:
```go
id, err := a.store.UpsertShoppingProfile(*profile)
if err != nil {
    return nil, err
}
profile.ID = id
reply := fmt.Sprintf("Great — I've got your brief for %s. Want me to pull up what's available right now?", profile.Name)
_ = a.store.SaveAssistantSession(models.AssistantSession{
    UserID:           userID,
    PendingIntent:    models.IntentShowMatches,
    LastAssistantMsg: reply,
})
_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, reply)
return &models.AssistantReply{
    Message:   reply,
    Expecting: true,
    Intent:    models.IntentShowMatches,
    Profile:   profile,
}, nil
```

With:
```go
id, err := a.store.UpsertShoppingProfile(*profile)
if err != nil {
    return nil, err
}
profile.ID = id

huntCount, _ := a.autoDeployHunts(ctx, userID, *profile)
recs, _, _ := a.FindMatches(ctx, userID, defaultMatchLimit)

var huntMsg string
switch {
case huntCount == 1:
    huntMsg = "I've activated 1 monitor — it'll scan every few minutes."
case huntCount > 1:
    huntMsg = fmt.Sprintf("I've activated %d monitors across the market.", huntCount)
default:
    huntMsg = "Your existing monitors will pick this up automatically."
}

reply := fmt.Sprintf("Brief saved for **%s**. %s\n\nHere's what's available right now:", profile.Name, huntMsg)
_ = a.store.ClearAssistantSession(userID)
_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, reply)
return &models.AssistantReply{
    Message:         reply,
    Expecting:       false,
    Intent:          models.IntentShowMatches,
    Profile:         profile,
    Recommendations: recs,
}, nil
```

**Change 3:** In `collectConcerns` (around line 529), find:
```go
if strings.Contains(text, "defect") || strings.Contains(text, "gaat niet aan") || strings.Contains(text, "kapot") {
    concerns = append(concerns, "listing may be defective or incomplete")
}
```

Replace with:
```go
if strings.Contains(text, "defect") || strings.Contains(text, "not working") ||
    strings.Contains(text, "broken") || strings.Contains(text, "fault") ||
    strings.Contains(text, "gaat niet aan") || strings.Contains(text, "kapot") {
    concerns = append(concerns, "listing may be defective or incomplete")
}
```

---

## Step 13 — Generator: Remove Dutch References from Prompts

### File: `internal/generator/generator.go`

**Change 1:** Replace the system prompt (lines 80–84):
```go
Content: "You generate marketplace search presets for Marktplaats.nl. Return strict JSON only. " +
    "Optimize for bargain hunting of used electronics. Use euro budgets as whole euros. " +
    "For camera bodies use category_id 487, for lenses use 495, and only use 356 for gaming-related items. " +
    "Use Dutch conditions and keep auto_message false.",
```

With:
```go
Content: "You generate search presets for European second-hand marketplaces (Marktplaats, Vinted, OLX). Return strict JSON only. " +
    "Optimize for bargain hunting of used electronics: cameras, lenses, laptops, phones, gaming gear, audio equipment. Use euro budgets as whole euros. " +
    "For camera bodies use category_id 487, for lenses use 495, and only use 356 for gaming-related items. " +
    "Use canonical English conditions (new, like_new, good, fair) and keep auto_message false.",
```

**Change 2:** In `buildPrompt`, replace the example JSON string (line 142) to use English conditions and a simple English message template:

Find the long backtick string containing `"condition":["Gebruikt","Zo goed als nieuw"]` and replace the condition arrays with `["good","like_new"]`. Replace the Dutch `message_template` value (`"Hoi! Ik ben geïnteresseerd..."`) with `"Hi, I'm interested in {{.Title}}. Would you accept €{{.OfferPrice}}?"`.

**Change 3:** On line 147, replace:
```go
"- Use Dutch condition values.",
```
With:
```go
"- Use canonical English condition values: new, like_new, good, fair.",
```

---

## Step 14 — API Server: Add Draft Offer Sub-Route

### File: `internal/api/server.go`

In `handleShortlistItem`, the first line of the function body (line 483) is:
```go
itemID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/shortlist/"), "/")
```

Replace just that first line and the empty-check with this expanded header:
```go
rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/shortlist/"), "/")

// Handle /shortlist/{itemID}/draft as a POST sub-resource.
if strings.HasSuffix(rawPath, "/draft") && strings.Count(rawPath, "/") == 1 {
    itemID := strings.TrimSuffix(rawPath, "/draft")
    if r.Method != http.MethodPost {
        writeMethodNotAllowed(w, http.MethodPost)
        return
    }
    draft, err := s.assistant.DraftSellerMessage(r.Context(), user.ID, itemID)
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, draft)
    return
}

itemID := rawPath
if itemID == "" {
    writeError(w, http.StatusBadRequest, "missing shortlist item id")
    return
}
```

Remove the original `if itemID == "" { ... }` block that follows — it's now included above.

---

## Step 15 — TypeScript API Client: Add Scored Fields and Draft Offer

### File: `web/lib/api.ts`

**Change 1:** Replace the `Listing` type (lines 30–39):
```ts
export type Listing = {
  ItemID: string;
  Title: string;
  Price: number;
  PriceType?: string;
  Condition?: string;
  URL?: string;
  ImageURLs?: string[];
  MarketplaceID?: string;
};
```

With:
```ts
export type Listing = {
  ItemID: string;
  Title: string;
  Price: number;
  PriceType?: string;
  Condition?: string;
  URL?: string;
  ImageURLs?: string[];
  MarketplaceID?: string;
  // Analysis fields — present when loaded from DB or SSE scored deal:
  Score?: number;
  FairPrice?: number;
  OfferPrice?: number;
  Confidence?: number;
  Reason?: string;
  RiskFlags?: string[];
};
```

**Change 2:** In the `api` object, inside the `shortlist` section (lines 274–278), add a `draftOffer` method:
```ts
shortlist: {
  get: async () => apiFetch<{ shortlist: ShortlistEntry[] }>("/shortlist"),
  add: async (itemID: string) => apiFetch<ShortlistEntry>(`/shortlist/${itemID}`, { method: "POST" }),
  remove: async (itemID: string) => apiFetch<{ ok: boolean }>(`/shortlist/${itemID}`, { method: "DELETE" }),
  draftOffer: async (itemID: string) =>
    apiFetch<{ Content: string; ItemID: string }>(`/shortlist/${encodeURIComponent(itemID)}/draft`, { method: "POST" }),
},
```

---

## Step 16 — ListingCard: Use Flat `Listing` Fields

### File: `web/components/ListingCard.tsx`

**Change 1:** Delete the local `ScoredListing` interface (lines 9–15) and the `isScoredListing` type guard function (lines 23–25).

**Change 2:** Replace the `Props` interface:
```ts
interface Props {
  listing: Listing | ScoredListing;
  onShortlist?: (itemID: string) => Promise<void>;
  isSaved?: boolean;
}
```
With:
```ts
interface Props {
  listing: Listing;
  onShortlist?: (itemID: string) => Promise<void>;
  isSaved?: boolean;
}
```

**Change 3:** Replace the four derived variables at the top of the component function:
```ts
const item = isScoredListing(listing) ? listing.Listing : listing;
const score = isScoredListing(listing) ? listing.Score : undefined;
const offerPrice = isScoredListing(listing) ? listing.OfferPrice : undefined;
const fairPrice = isScoredListing(listing) ? listing.FairPrice : undefined;
const reason = isScoredListing(listing) ? listing.Reason : undefined;
```
With:
```ts
const item = listing;
const score = (listing.Score ?? 0) > 0 ? listing.Score : undefined;
const offerPrice = (listing.OfferPrice ?? 0) > 0 ? listing.OfferPrice : undefined;
const fairPrice = (listing.FairPrice ?? 0) > 0 ? listing.FairPrice : undefined;
const reason = listing.Reason || undefined;
```

**Change 4:** Add a risk flag labels map and badges after the `MARKETPLACE_LABELS` constant:
```ts
const RISK_FLAG_LABELS: Record<string, string> = {
  anomaly_price:   "⚠ Anomaly price",
  vague_condition: "⚠ Vague condition",
  unclear_bundle:  "⚠ Unclear bundle",
  no_model_id:     "⚠ No model identified",
};
```

**Change 5:** Inside the returned JSX, after the `{reason && <p className="listing-reason">{reason}</p>}` line, add:
```tsx
{(item.RiskFlags?.length ?? 0) > 0 && (
  <div className="risk-flags">
    {item.RiskFlags!.map((flag) => (
      <span key={flag} className="risk-flag">
        {RISK_FLAG_LABELS[flag] ?? flag}
      </span>
    ))}
  </div>
)}
```

---

## Step 17 — Feed Page: Fix SSE Handler and Empty State

### File: `web/app/(dashboard)/feed/page.tsx`

**Change 1:** Replace the SSE event handler block (lines 23–31):
```ts
disconnect = connectDealStream((payload) => {
  if (!payload || typeof payload !== "object") return;
  const event = payload as { type?: string; deal?: { Listing?: Listing }; Listing?: Listing };
  const listing = event.deal?.Listing ?? event.Listing;
  if (event.type === "deal_found" && listing?.ItemID) {
    setListings((prev) => [listing, ...prev.filter((item) => item.ItemID !== listing.ItemID)]);
    setNewCount((count) => count + 1);
  }
});
```

With:
```ts
disconnect = connectDealStream((payload) => {
  if (!payload || typeof payload !== "object") return;
  const event = payload as {
    type?: string;
    deal?: {
      Listing?: Listing;
      Score?: number;
      OfferPrice?: number;
      FairPrice?: number;
      Confidence?: number;
      Reason?: string;
      RiskFlags?: string[];
    };
  };
  if (event.type === "deal_found" && event.deal?.Listing?.ItemID) {
    const listing: Listing = {
      ...event.deal.Listing,
      Score:      event.deal.Score      ?? 0,
      OfferPrice: event.deal.OfferPrice ?? 0,
      FairPrice:  event.deal.FairPrice  ?? 0,
      Confidence: event.deal.Confidence ?? 0,
      Reason:     event.deal.Reason     ?? "",
      RiskFlags:  event.deal.RiskFlags  ?? [],
    };
    setListings((prev) => [listing, ...prev.filter((item) => item.ItemID !== listing.ItemID)]);
    setNewCount((count) => count + 1);
  }
});
```

**Change 2:** Replace the empty state (lines 70–82):
```tsx
<div className="surface-panel empty-state">
  <div className="empty-icon">...</div>
  <h3>Radar is ready — no hunts active yet</h3>
  <p>
    Set up at least one hunt and MarktBot will start streaming scored deals here in real time.
  </p>
</div>
```

With:
```tsx
<div className="surface-panel empty-state">
  <div className="empty-icon">
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" style={{ color: "var(--brand-600)" }}>
      <circle cx="8" cy="8" r="4" />
      <path d="M8 4a4 4 0 0 1 4 4" opacity="0.5" />
      <path d="M8 1a7 7 0 0 1 7 7" opacity="0.3" />
    </svg>
  </div>
  <h3>No deals yet — tell the AI what you want</h3>
  <p>
    Describe what you're after in the Brief and MarktBot will activate monitors and stream scored deals here.
  </p>
  <Link href="/assistant" className="btn-primary" style={{ marginTop: 12 }}>
    Set up your brief
  </Link>
</div>
```

Add `import Link from "next/link";` at the top of the file if not already present.

---

## Step 18 — AssistantChat: Electronics Prompts, Link Fix, Post-Brief CTA

### File: `web/components/AssistantChat.tsx`

**Change 1:** Replace the `PROMPTS` array (lines 16–21):
```ts
const PROMPTS = [
  "I want a Sony A6700, good condition, under €900",
  "Looking for a MacBook Pro M3 — open to refurbs",
  "Vintage Levi's denim jacket, max €60",
  "Electric road bike, budget €1 200",
];
```

With:
```ts
const PROMPTS = [
  "I want a Sony A6700, good condition, under €900",
  "Looking for a MacBook Pro 14 M3, like new, max €1 600",
  "Canon RF 50mm f/1.8 lens, any condition, under €220",
  "Gaming laptop RTX 4060, good condition, budget €750",
];
```

**Change 2:** Replace the "View hunts" link (line 188):
```tsx
<Link href="/searches" className="btn-ghost" style={{ fontSize: "0.84rem", minHeight: 36 }}>
  View hunts
</Link>
```

With:
```tsx
<Link href="/feed" className="btn-ghost" style={{ fontSize: "0.84rem", minHeight: 36 }}>
  View deals
</Link>
```

**Change 3:** After the `</div>` that closes the `rec-list` (after line 248), add a post-brief CTA. Find this block:
```tsx
{/* Inline recommendation cards */}
{item.role === "assistant" && item.recommendations && item.recommendations.length > 0 && (
  <div className="rec-list">
    {item.recommendations.map((rec) => (
      <RecCard key={rec.Listing.ItemID} rec={rec} />
    ))}
  </div>
)}
```

Replace with:
```tsx
{/* Inline recommendation cards */}
{item.role === "assistant" && item.recommendations && item.recommendations.length > 0 && (
  <>
    <div className="rec-list">
      {item.recommendations.map((rec) => (
        <RecCard key={rec.Listing.ItemID} rec={rec} />
      ))}
    </div>
    <div className="chat-feed-cta">
      <p>Your monitors are scanning. New deals appear in real time.</p>
      <Link href="/feed" className="btn-primary" style={{ fontSize: "0.84rem" }}>
        Open Deals →
      </Link>
    </div>
  </>
)}
```

---

## Step 19 — Navigation: Rename and Remove Hunts

### File: `web/app/(dashboard)/layout.tsx`

**Change 1:** Delete the `IconHunts` function (lines 32–40).

**Change 2:** Replace the `NAV` array (lines 60–66):
```ts
const NAV = [
  { href: "/assistant", label: "AI Brief", description: "Tell the AI what you want", Icon: IconAI },
  { href: "/feed", label: "Radar", description: "Live AI-surfaced deals", Icon: IconRadar },
  { href: "/searches", label: "Hunts", description: "Active market monitors", Icon: IconHunts },
  { href: "/shortlist", label: "Saved", description: "Deals worth acting on", Icon: IconSaved },
  { href: "/settings", label: "Settings", description: "Account and billing", Icon: IconSettings },
];
```

With:
```ts
const NAV = [
  { href: "/assistant", label: "Brief",    description: "Tell the AI what you want",   Icon: IconAI },
  { href: "/feed",      label: "Deals",    description: "AI-surfaced matches",          Icon: IconRadar },
  { href: "/shortlist", label: "Saved",    description: "Deals you're considering",     Icon: IconSaved },
  { href: "/settings",  label: "Settings", description: "Account and billing",          Icon: IconSettings },
];
```

---

## Step 20 — Shortlist Page: Draft Offer UI

### File: `web/app/(dashboard)/shortlist/page.tsx`

**Change 1:** Add a `draftStates` state variable alongside the existing state declarations:
```ts
const [draftStates, setDraftStates] = useState<Record<string, { loading: boolean; text: string | null }>>({});
```

**Change 2:** Add a `draftOffer` async function alongside the other handler functions:
```ts
async function draftOffer(itemID: string) {
  setDraftStates((prev) => ({ ...prev, [itemID]: { loading: true, text: null } }));
  try {
    const res = await api.shortlist.draftOffer(itemID);
    setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: res.Content } }));
  } catch {
    setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: null } }));
  }
}
```

**Change 3:** In the JSX for each shortlist row, after the last action button (Delete), add:
```tsx
<div className="offer-draft-row">
  <button
    type="button"
    className="btn-ghost"
    disabled={draftStates[entry.ItemID]?.loading}
    onClick={() => void draftOffer(entry.ItemID)}
  >
    {draftStates[entry.ItemID]?.loading ? "Drafting…" : "Draft offer"}
  </button>
  {draftStates[entry.ItemID]?.text && (
    <div className="offer-draft-block">
      <p>{draftStates[entry.ItemID]!.text}</p>
      <button
        type="button"
        className="btn-copy"
        onClick={() => {
          void navigator.clipboard.writeText(draftStates[entry.ItemID]!.text!);
        }}
      >
        Copy
      </button>
    </div>
  )}
</div>
```

---

## Step 21 — CSS: New Classes

### File: `web/app/globals.css`

Append these rules at the end of the file:

```css
/* Offer draft in shortlist */
.offer-draft-row { display: flex; flex-direction: column; gap: 8px; margin-top: 8px; }
.offer-draft-block {
  background: var(--brand-100); border: 1px solid var(--border-brand);
  border-radius: var(--radius-md); padding: 12px 14px;
  font-size: 0.875rem; color: var(--fg-700); white-space: pre-wrap;
  display: flex; flex-direction: column; gap: 8px;
}
.btn-copy {
  align-self: flex-end; padding: 4px 12px; border-radius: var(--radius-md);
  border: 1px solid var(--border-brand); background: transparent;
  color: var(--brand-600); font-size: 0.8rem; font-weight: 600; cursor: pointer;
}
.btn-copy:hover { background: var(--brand-100); }

/* Risk flags on listing cards */
.risk-flags { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.risk-flag {
  font-size: 0.75rem; color: var(--warning-500); background: rgba(180,83,9,0.08);
  border: 1px solid rgba(180,83,9,0.18); border-radius: 6px; padding: 2px 7px;
}

/* Post-brief CTA in chat */
.chat-feed-cta {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 14px; background: var(--surface); border: 1px solid var(--border-brand);
  border-radius: var(--radius-md); gap: 12px; flex-wrap: wrap; margin-top: 8px;
}
.chat-feed-cta p { margin: 0; font-size: 0.875rem; color: var(--fg-500); }
```

---

## Step 22 — `.env.example`: Add SMTP Entries

Append to `.env.example`:
```
SMTP_HOST=smtp.yourprovider.com
SMTP_PORT=587
SMTP_USER=alerts@yourapp.com
SMTP_PASS=yourpassword
SMTP_FROM=alerts@marktbot.app
ALERT_SCORE=8.0
```

---

## Files Changed Summary

| File | Change |
|---|---|
| `internal/store/store.go` | DB migration: 8 ALTER TABLE stmts; replace `SaveListing`; replace `ListRecentListings` |
| `internal/store/postgres.go` | DB migration: 8 ADD COLUMN IF NOT EXISTS; replace `SaveListing`; replace `ListRecentListings` |
| `internal/store/iface.go` | `SaveListing` signature: `score float64` → `scored models.ScoredListing` |
| `internal/models/listing.go` | Add 6 fields to `Listing`; add `RiskFlags` to `ScoredListing` |
| `internal/scorer/scorer.go` | Add `computeRiskFlags` + `isElectronicsListing`; assign `RiskFlags` in return |
| `internal/worker/worker.go` | Pass `scored` (not `scored.Score`) to `SaveListing`; add `emailNotifier` field; call email notifier |
| `internal/worker/pool.go` | Add `emailNotifier` field; add `SetEmailNotifier` setter; pass to `UserWorker` in both run methods |
| `internal/notify/email.go` | **NEW** — `EmailNotifier` struct with `NewEmail`, `Enabled`, `SendDealAlert` |
| `internal/config/env.go` | Add SMTP fields + `AlertScore`; add `parseFloatDefault` helper |
| `cmd/server/main.go` | Init `EmailNotifier`; call `pool.SetEmailNotifier` |
| `internal/assistant/assistant.go` | Add `autoDeployHunts`, `intervalForTier`, `marketplacesForTier`; replace second half of `startBriefConversation`; fix Dutch in `collectConcerns` |
| `internal/generator/generator.go` | Replace system prompt; replace Dutch conditions in `buildPrompt`; replace Dutch rule line |
| `internal/api/server.go` | Expand `handleShortlistItem` to handle `/draft` sub-path |
| `web/lib/api.ts` | Add scored fields to `Listing` type; add `shortlist.draftOffer` |
| `web/components/ListingCard.tsx` | Remove `ScoredListing` interface + type guard; use flat `Listing` fields; add risk flag badges |
| `web/app/(dashboard)/feed/page.tsx` | Fix SSE handler to flatten scored data; update empty state CTA |
| `web/components/AssistantChat.tsx` | Update `PROMPTS`; fix "View hunts" → "View deals" link; add post-brief Deals CTA |
| `web/app/(dashboard)/layout.tsx` | Remove `IconHunts`; rename NAV to Brief/Deals/Saved/Settings |
| `web/app/(dashboard)/shortlist/page.tsx` | Add `draftStates`, `draftOffer` function, draft offer UI per row |
| `web/app/globals.css` | Add `offer-draft-row`, `offer-draft-block`, `btn-copy`, `risk-flags`, `risk-flag`, `chat-feed-cta` |
| `.env.example` | SMTP entries |

---

## Verification

Run these checks in order:

1. `go build ./...` — must compile clean. Fix any type mismatches from the `SaveListing` signature change first.
2. `cd web && npx tsc --noEmit` — must compile clean. The `isScoredListing` removal and `Listing` type expansion are the most likely sources of errors.
3. **Score persists on reload:** Start the server, trigger a search, wait for a deal to appear in the SSE feed. Reload the browser. Verify that `FairPrice` and `OfferPrice` are still shown on the listing card (non-zero values).
4. **SSE shows score immediately:** Keep the browser open during step 3. Verify that the score bar, fair price, and offer price are visible on the card as soon as it arrives via SSE (before reload).
5. **Risk flags:** Look for a listing with "as is" or "untested" in its title → `⚠ Vague condition` badge should appear.
6. **Brief → auto-deploy:** Type "I want a Sony A6700, good condition, under €800" in the Brief chat. Reply should include "I've activated N monitors" and show immediate recommendations without needing "go for it". Navigate to `/searches` and confirm the new search config exists.
7. **Draft offer:** Go to Saved, click "Draft offer" on any entry → message appears inline → click Copy → paste into a text editor to verify content.
8. **Nav:** Confirm sidebar shows Brief, Deals, Saved, Settings only — no Hunts entry.
9. **Feed empty state:** Create a fresh test account, go to `/feed` → empty state has "Set up your brief" CTA linking to `/assistant`.
10. **Email** (optional, requires SMTP config): Trigger a deal scoring ≥ 8.0 → email arrives at the user's registered address.
