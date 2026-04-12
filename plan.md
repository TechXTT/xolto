# AI Token Optimization Plan

## Context

Every listing found by the worker pool gets an individual LLM call via `scorer.Score()` → `reasoner.Analyze()` → `callLLM()`. This happens unconditionally — even for listings we've already seen and scored at the same price. With multiple users, missions, and 3 marketplaces running searches every few minutes, this burns tokens fast and drives up API cost unnecessarily.

The goal is to reduce LLM call volume by ~95% through caching, pre-filtering, and tiered scoring, while also shrinking per-call token usage through prompt optimization.

---

## Step 1: Score Caching — Skip LLM for Already-Scored Listings

**Savings: 70-90% of all LLM calls | Complexity: Low**

The worker (`worker.go:151-153`) already queries `IsNew` and `GetListingScore` before scoring, but then calls `scorer.Score()` unconditionally. The fix is to skip the score call when we already have an AI result at the same price.

### Changes

**`internal/store/iface.go`** — Add to Reader:
```go
GetListingScoringState(userID, itemID string) (price int, reasoningSource string, found bool, err error)
```

**`internal/store/postgres.go`** + **`internal/store/store.go`** — Implement:
```sql
SELECT price, reasoning FROM listings WHERE item_id = $1
```
Parse `reasoning` to extract source — or better, add a `reasoning_source` column:
```sql
ALTER TABLE listings ADD COLUMN IF NOT EXISTS reasoning_source TEXT NOT NULL DEFAULT ''
```
Populate it in `SaveListing` from `scored.ReasoningSource`.

**`internal/worker/worker.go`** — Gate the scorer call (around line 153):
```go
isNew, _ := w.db.IsNew(spec.UserID, listing.ItemID)
prevScore, hadPrev, _ := w.db.GetListingScore(spec.UserID, listing.ItemID)

// NEW: skip re-scoring if same price and already AI-scored
if !isNew {
    storedPrice, storedSource, found, _ := w.db.GetListingScoringState(spec.UserID, listing.ItemID)
    if found && storedPrice == listing.Price && storedSource == "ai" {
        // Update last_seen timestamp only
        _ = w.db.TouchListing(spec.UserID, listing.ItemID)
        continue
    }
}

scored := w.scorer.Score(ctx, listing, spec)
```

**`internal/store/iface.go`** — Add to Writer:
```go
TouchListing(userID, itemID string) error
```
Implementation: `UPDATE listings SET last_seen = NOW() WHERE item_id = $1`

**`internal/store/postgres.go`** — Update `SaveListing` to persist `reasoning_source`:
Add `reasoning_source` to the INSERT and ON CONFLICT UPDATE columns, populating from `scored.ReasoningSource`.

---

## Step 2: Heuristic Pre-filtering — Skip Obviously Bad Listings

**Savings: 20-50% of remaining LLM calls | Complexity: Low**

Use cheap checks before the LLM to skip listings that are clearly not worth analyzing.

### Changes

**`internal/scorer/scorer.go`** — Add a `shouldSkipLLM` method before the `reasoner.Analyze` call (line 109):

```go
func (sc *Scorer) shouldSkipLLM(listing models.Listing, search models.SearchSpec, heuristic models.DealAnalysis) bool {
    // Price way above budget — not a deal even after negotiation
    if search.MaxPrice > 0 && listing.Price > search.MaxPrice*3/2 {
        return true
    }
    // Heuristic is confident AND score is clearly terrible
    if heuristic.Confidence >= 0.70 {
        ratio := float64(listing.Price) / float64(heuristic.FairPrice)
        score := clamp(10.0 - 10.0*ratio + 5.0, 1, 10)
        if score < 3.0 {
            return true
        }
    }
    return false
}
```

**`internal/scorer/scorer.go`** — Modify `Score` (around line 109):
Run `reasoner.heuristicAnalysis` first (it already computes this internally — expose it or move the call earlier), then check `shouldSkipLLM`. If skipped, use the heuristic result directly with `ReasoningSource: "prefilter"`.

**`internal/reasoner/reasoner.go`** — Export `HeuristicAnalysis` (currently lowercase `heuristicAnalysis`):
Rename to `HeuristicAnalysis` so the scorer can call it independently before deciding whether to invoke the full `Analyze` path.

---

## Step 3: Tiered Scoring — LLM Only for Borderline Cases

**Savings: 30-60% of remaining LLM calls | Complexity: Medium**

Even after pre-filtering, many listings have clear heuristic verdicts. Reserve LLM for the uncertain middle band.

### Changes

**`internal/config/config.go`** — Add fields to `AIConfig`:
```go
SkipLLMConfidence float64 `yaml:"skip_llm_confidence"` // default 0.75
SkipLLMScoreLow   float64 `yaml:"skip_llm_score_low"`  // default 3.0
SkipLLMScoreHigh  float64 `yaml:"skip_llm_score_high"` // default 9.0
```

**`internal/reasoner/reasoner.go`** — Modify `Analyze` (lines 44-81):
After computing the heuristic, check the tier thresholds:
```go
heuristic := r.heuristicAnalysis(listing, search, marketAvg, ranked)
if !r.Enabled() {
    return heuristic, nil
}

// Tiered: skip LLM when heuristic is confident and score is clear-cut
if heuristic.Confidence >= r.cfg.SkipLLMConfidence {
    ratio := float64(listing.Price) / float64(heuristic.FairPrice)
    hScore := clamp(10.0 - 10.0*ratio + 5.0, 1, 10)
    if hScore <= r.cfg.SkipLLMScoreLow || hScore >= r.cfg.SkipLLMScoreHigh {
        heuristic.Source = "heuristic-confident"
        return heuristic, nil
    }
}

// Proceed to LLM for borderline cases...
```

This means the LLM only fires for listings in the 3.0-9.0 score range with lower heuristic confidence — the cases where AI judgment actually adds value.

---

## Step 4: Prompt Optimization — Reduce Token Count Per Call

**Savings: 15-25% fewer input tokens per call | Complexity: Low**

The current `buildPrompt` (reasoner.go:275-324) serializes full `models.Listing` and `models.SearchSpec` structs including many irrelevant fields.

### Changes

**`internal/reasoner/reasoner.go`** — Replace the prompt input struct in `buildPrompt` with slim types:

```go
type promptListing struct {
    Title     string `json:"t"`
    Desc      string `json:"d,omitempty"`
    Price     int    `json:"p"`
    PriceType string `json:"pt,omitempty"`
    Condition string `json:"c,omitempty"`
}

type promptSearch struct {
    Query    string `json:"q"`
    MaxPrice int    `json:"max,omitempty"`
    MinPrice int    `json:"min,omitempty"`
}

type promptComparable struct {
    Index int     `json:"i"`
    Title string  `json:"t"`
    Price int     `json:"p"`
    Sim   float64 `json:"s"`
}
```

**Specific reductions:**
- Strip `ImageURLs`, `Seller`, `Location`, `CanonicalID`, `MarketplaceID`, `CategoryID`, all zero-valued analysis fields from listing
- Strip `UserID`, `ID`, `Enabled`, `CheckInterval`, `AutoMessage`, `MessageTemplate`, `Attributes`, `ProfileID` from search spec
- Truncate `Description` to first 300 characters
- Drop `MatchReason` string from comparables (redundant with `Similarity` score)
- Reduce `MaxComparables` default from 8 to 5 in `setDefaults`
- Use single-char JSON keys as shown above

**Compress instruction text** (lines 316-322):
```go
// Before: 7 lines of instruction
// After:
"Analyze listing vs comparables. Set relevant=false if wrong product category. " +
"Return JSON: {\"relevant\":true,\"fair_price_cents\":N,\"confidence\":0.0-1.0,\"reasoning\":\"...\",\"search_advice\":\"...\",\"comparable_indexes\":[0,2]}"
```

---

## Step 5: Per-User Rate Limiting (safety net)

**Savings: Cost ceiling | Complexity: Medium**

### Changes

**`internal/config/config.go`** — Add to `AIConfig`:
```go
MaxCallsPerUserPerHour int `yaml:"max_calls_per_user_per_hour"` // default 200
MaxCallsGlobalPerHour  int `yaml:"max_calls_global_per_hour"`   // default 2000
```

**`internal/reasoner/ratelimit.go`** — New file:
Simple in-memory sliding window rate limiter with `Allow(userID string) bool`. Uses `sync.Mutex` + a map of user → recent call timestamps. Prunes expired entries on each check.

**`internal/reasoner/reasoner.go`** — Wire into `callLLM`:
If rate limit exceeded, return heuristic fallback with `Source: "rate-limited"` instead of erroring.

---

## Files Modified (summary)

| File | Changes |
|------|---------|
| `internal/worker/worker.go` | Gate scorer call with cache check (Step 1) |
| `internal/scorer/scorer.go` | Add `shouldSkipLLM`, call heuristic before LLM (Steps 2-3) |
| `internal/reasoner/reasoner.go` | Export `HeuristicAnalysis`, add tier thresholds in `Analyze`, slim `buildPrompt`, wire rate limiter (Steps 2-5) |
| `internal/reasoner/ratelimit.go` | New file: sliding window rate limiter (Step 5) |
| `internal/store/iface.go` | Add `GetListingScoringState`, `TouchListing` (Step 1) |
| `internal/store/postgres.go` | Implement new methods, add `reasoning_source` column, update `SaveListing` (Step 1) |
| `internal/store/store.go` | SQLite implementations of above (Step 1) |
| `internal/config/config.go` | Add `SkipLLM*` thresholds, rate limit config, reduce `MaxComparables` default (Steps 3-5) |

---

## Compound Effect Estimate

Starting from 1000 LLM calls per cycle:

| After Step | Calls Remaining | Cumulative Reduction |
|-----------|----------------|---------------------|
| 1 — Caching | ~150 | 85% |
| 2 — Pre-filter | ~100 | 90% |
| 3 — Tiered | ~50 | 95% |
| 4 — Prompt slim | 50 (each 20% cheaper) | ~96% cost |
| 5 — Rate limit | Hard ceiling | Safety net |

---

## Verification

1. **Unit tests**: Add test cases in `internal/reasoner/reasoner_test.go` for the tiered scoring thresholds and heuristic confidence gating
2. **Integration test**: Update `internal/api/server_test.go` to verify the `reasoning_source` column is persisted correctly through `SaveListing`
3. **Manual verification**: 
   - Run the server with `ADMIN_EMAILS` set to your email
   - Create a mission and let it run for a few cycles
   - Check the admin dashboard `/admin` — the "Usage" tab should show significantly fewer AI calls
   - Compare `reasoning_source` distribution: should see a mix of `"ai"`, `"heuristic-confident"`, and `"prefilter"` instead of all `"ai"`
4. **Go build + test**: `go build ./...` and `go test ./...` must pass
5. **TypeScript check**: `npx tsc --noEmit` in `web/` must pass (no frontend changes in this plan)
