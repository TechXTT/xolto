package api

// W18-2: Anonymous /matches/analyze entry point.
//
// Mounted at POST /public/matches/analyze. The authenticated path remains
// `POST /matches/analyze` (registered with requireAuth in registerListingRoutes)
// and is intentionally byte-for-byte identical to its previous behaviour. The
// anonymous handler below adds three abuse / cost guards and a Sentry tag so
// the open path is safe to expose to a landing-site "Try a verdict" widget:
//
//  1. Per-IP rate limit (5/IP/hour, sliding window, in-memory).
//  2. URL cache (TTL 6h, in-memory). Same OLX.bg URL inside the window
//     short-circuits the AI/scorer call. Cache hits DO consume rate-limit
//     budget so an attacker cannot replay a single URL infinitely cheaply.
//  3. Daily cost circuit-breaker (USD 5 ceiling per UTC day, fail-closed).
//
// Why a parallel route rather than broadened auth on /matches/analyze:
// keeping the authenticated path's middleware chain unchanged is the smallest
// blast radius for the wedge dash users. Any regression on authed
// /matches/analyze should be impossible with this layout because no code on
// that path was modified by W18-2.
//
// Founder constraints (Decision Log 2026-04-25):
//  - $5 USD/day hard ceiling. Do not raise without founder approval.
//  - No user-facing abuse gate (no captcha). Cost-cap + rate-limit only.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/observability"

	"github.com/getsentry/sentry-go"
)

// anonymousAnalyzeRateLimit is the per-IP request budget for the anonymous
// analyze endpoint. Five requests per IP per hour, sliding window. Tuned
// with the founder's $5/day cost ceiling in mind: at 5/hr × 24 = 120 max
// hourly-budget requests per IP per day, well under the budget when only a
// handful of distinct IPs hit the endpoint legitimately.
const anonymousAnalyzeRateLimit = 5
const anonymousAnalyzeRateWindow = time.Hour

// anonymousAnalyzeCacheTTL is how long a single canonical URL's analysis
// remains cached for the anonymous path. Six hours is long enough to absorb
// a marketing campaign refresh storm without making the verdict stale.
const anonymousAnalyzeCacheTTL = 6 * time.Hour

// anonymousAnalyzeCostCeilingUSD is the founder-locked daily spend ceiling
// for AI calls served from the anonymous path. Trip → 503 until UTC midnight.
// Do NOT raise this without founder approval (Decision Log 2026-04-25).
const anonymousAnalyzeCostCeilingUSD = 5.0

// estimatedCostPerAnonymousCallUSD is a conservative upper-bound *pre-spend*
// estimate of the AI cost per analyze call. It is intentionally an upper
// bound so the circuit-breaker fails safely conservative — i.e. trips
// slightly earlier than the true spend would require, never later.
//
// Rationale (gpt-5-mini, Jan 2026 published prices):
//   input  $0.25 / 1M tokens × ~3,000 input tokens  ≈ $0.000750
//   output $2.00 / 1M tokens × ~  500 output tokens ≈ $0.001000
//   per-call true expectation                       ≈ $0.0018
//
// We round up by ~5× to $0.01/call to:
//   - absorb prompt-version drift,
//   - absorb retry loops on a noisy upstream,
//   - keep the maths trivially auditable ($5 / $0.01 = 500 calls/day budget).
//
// W19-3 plumb: scorer.Score now returns the real per-call USD cost in
// scored.CostUSD. The pre-spend projection still uses this conservative
// constant so a burst of concurrent calls cannot blow past the ceiling
// before any of them returns; *after* the scorer returns, we reconcile by
// charging (or rebating) the delta between this estimate and the actual
// cost. The breaker therefore stays honest on cumulative spend while
// remaining fail-closed at the projection step.
const estimatedCostPerAnonymousCallUSD = 0.01

// anonymousAnalyzeCostKey is a sentinel string used as the Sentry breadcrumb
// tag value to identify anonymous-analyze events.
const anonymousAnalyzeTagValue = "true"
const anonymousAnalyzeTagKey = "anonymous_analyze"

// anonymousAnalyzeBudget tracks the running daily spend on the anonymous
// path. It resets at UTC midnight. Goroutine-safe via the embedded mutex.
type anonymousAnalyzeBudget struct {
	mu              sync.Mutex
	day             time.Time // UTC date (00:00:00 UTC of the current day)
	spentUSD        float64
	breakerNotified bool // true once Sentry has been alerted for today's trip
}

// addAndProject locks, rolls the day if needed, adds delta to the running
// spend, and returns the new total. day rollover automatically resets the
// breakerNotified flag. delta should be the upper-bound cost of the call we
// are *about to* make so the breaker can fail-closed before we incur it.
func (b *anonymousAnalyzeBudget) addAndProject(delta float64, now time.Time) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	utc := now.UTC()
	startOfDay := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if !b.day.Equal(startOfDay) {
		b.day = startOfDay
		b.spentUSD = 0
		b.breakerNotified = false
	}
	b.spentUSD += delta
	return b.spentUSD
}

// projectedSpend returns the current spend without modifying state. Mainly
// used by tests; the request path uses addAndProject which is atomic.
func (b *anonymousAnalyzeBudget) projectedSpend(now time.Time) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	utc := now.UTC()
	startOfDay := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if !b.day.Equal(startOfDay) {
		return 0
	}
	return b.spentUSD
}

// markBreakerNotified atomically returns whether this call is the first
// breaker trip of the current UTC day. If so, the caller should emit the
// Sentry alert exactly once.
func (b *anonymousAnalyzeBudget) markBreakerNotified(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	utc := now.UTC()
	startOfDay := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	if !b.day.Equal(startOfDay) {
		// Day rolled over between project & notify; reset and treat as new day.
		b.day = startOfDay
		b.spentUSD = 0
		b.breakerNotified = false
	}
	if b.breakerNotified {
		return false
	}
	b.breakerNotified = true
	return true
}

// rollback subtracts a previously-added delta when the call did not actually
// happen (e.g. fetch failed before AI invocation). Keeps the projected spend
// honest. Never goes negative.
func (b *anonymousAnalyzeBudget) rollback(delta float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spentUSD -= delta
	if b.spentUSD < 0 {
		b.spentUSD = 0
	}
}

// secondsUntilUTCMidnight returns how many seconds remain until 00:00 UTC
// of the next day, used as Retry-After for 503 breaker responses. Always
// >= 1 to avoid clients busy-looping at the boundary.
func secondsUntilUTCMidnight(now time.Time) int {
	utc := now.UTC()
	tomorrow := time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
	delta := int(tomorrow.Sub(utc).Seconds())
	if delta < 1 {
		return 1
	}
	return delta
}

// anonymousAnalyzeRateLimiter is a per-IP sliding-window limiter. Each entry
// is a slice of timestamps for that IP's requests within the last window.
// On every request we trim entries older than the window and check the count.
//
// Memory bound: at most anonymousAnalyzeRateLimit timestamps per IP plus the
// map overhead. We sweep idle entries on every Allow call so the map does
// not grow unbounded under attack: any entry whose newest timestamp is
// older than `now - window` is removed entirely.
type anonymousAnalyzeRateLimiter struct {
	mu      sync.Mutex
	ipHits  map[string][]time.Time
	limit   int
	window  time.Duration
}

func newAnonymousAnalyzeRateLimiter(limit int, window time.Duration) *anonymousAnalyzeRateLimiter {
	return &anonymousAnalyzeRateLimiter{
		ipHits: make(map[string][]time.Time),
		limit:  limit,
		window: window,
	}
}

// Allow records a hit for ip and returns (allowed, retryAfterSeconds).
// retryAfterSeconds is meaningful only when allowed=false; it's the seconds
// until the oldest in-window entry falls out (i.e. when the next call would
// succeed). Always >= 1 to avoid client busy-loops.
func (l *anonymousAnalyzeRateLimiter) Allow(ip string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	hits := l.ipHits[ip]
	// Trim entries older than cutoff. Slice is in chronological order.
	trimmed := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			trimmed = append(trimmed, t)
		}
	}
	if len(trimmed) >= l.limit {
		// Oldest in-window timestamp determines next-allowed time.
		oldest := trimmed[0]
		retry := int(oldest.Add(l.window).Sub(now).Seconds()) + 1
		if retry < 1 {
			retry = 1
		}
		// Persist trimmed even on deny so the slice doesn't keep stale entries.
		l.ipHits[ip] = trimmed
		return false, retry
	}
	trimmed = append(trimmed, now)
	l.ipHits[ip] = trimmed

	// Best-effort eviction of fully-expired entries to bound memory growth
	// under attack (different attacker IP every call).
	for k, v := range l.ipHits {
		if len(v) == 0 || !v[len(v)-1].After(cutoff) {
			delete(l.ipHits, k)
		}
	}

	return true, 0
}

// anonymousAnalyzeCacheEntry is one cached analyze response. The body is
// stored as the already-marshalled JSON bytes so the cache hit path avoids
// re-encoding the response payload on every replay.
type anonymousAnalyzeCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

// anonymousAnalyzeCache is an in-memory TTL cache keyed by canonicalised
// listing URL. Survival across process restarts is intentionally NOT
// guaranteed (per W18-2 spec); a restart is a deliberate cache-flush.
type anonymousAnalyzeCache struct {
	mu      sync.Mutex
	entries map[string]anonymousAnalyzeCacheEntry
	ttl     time.Duration
}

func newAnonymousAnalyzeCache(ttl time.Duration) *anonymousAnalyzeCache {
	return &anonymousAnalyzeCache{
		entries: make(map[string]anonymousAnalyzeCacheEntry),
		ttl:     ttl,
	}
}

// Get returns the cached body for key when present and not expired.
func (c *anonymousAnalyzeCache) Get(key string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.After(now) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.body, true
}

// Put stores body under key with the cache's configured TTL.
func (c *anonymousAnalyzeCache) Put(key string, body []byte, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Cheap GC pass: drop expired entries on every write so the map cannot
	// grow unbounded over time.
	for k, e := range c.entries {
		if !e.expiresAt.After(now) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = anonymousAnalyzeCacheEntry{
		body:      body,
		expiresAt: now.Add(c.ttl),
	}
}

// canonicalizeAnonymousAnalyzeURL normalises a listing URL for use as the
// anonymous-analyze cache key. The shape is "<marketplace>:<id>" when we can
// identify those parts, falling back to the lowercased URL otherwise. We
// reuse the existing marketplace detector and ID extractor in
// internal/marketplace/listingfetcher so any future canonicalisation tweak
// there flows through here automatically.
//
// We deliberately key on (marketplace, listing-id) rather than on the raw URL
// so that:
//   - tracking query params (?utm_*, ?refresh=42) do not poison cache hits,
//   - desktop/mobile URL variants resolve to the same entry,
//   - one shared listing replayed across share-channels gets one analysis.
func canonicalizeAnonymousAnalyzeURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	host := strings.ToLower(parsed.Host)
	// Trim port when present so olx.bg and olx.bg:443 hash to the same key.
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	// Path lowercased; fragment + query stripped. Trailing slash trimmed.
	path := strings.ToLower(strings.TrimRight(parsed.Path, "/"))
	if host == "" && path == "" {
		return strings.ToLower(rawURL)
	}
	return host + path
}

// initAnonymousAnalyze lazily wires the rate limiter, cache, and budget once
// per Server. Called from the request path so test servers (which never call
// the anonymous handler unless the test does so explicitly) avoid the
// allocation when not needed.
func (s *Server) initAnonymousAnalyze() {
	s.anonymousAnalyzeOnce.Do(func() {
		s.anonymousAnalyzeRateLimiter = newAnonymousAnalyzeRateLimiter(anonymousAnalyzeRateLimit, anonymousAnalyzeRateWindow)
		s.anonymousAnalyzeCache = newAnonymousAnalyzeCache(anonymousAnalyzeCacheTTL)
		s.anonymousAnalyzeBudget = &anonymousAnalyzeBudget{}
	})
}

// nowAnonymousAnalyze is overridable in tests so we can drive day rollover,
// rate-limit window expiry, and cache TTL deterministically. Defaults to the
// real wall clock.
var nowAnonymousAnalyze = func() time.Time { return time.Now() }

// tagAnonymousAnalyzeSentry emits a no-op breadcrumb scoped to the current
// hub with the W18-2 anonymous_analyze tag set. Every call site on the
// anonymous path (success, 429, cache short-circuit, 503 breaker) calls this
// so downstream Sentry filtering can isolate this traffic class. When the
// SDK is disabled (local dev / tests without Init) this is a cheap no-op.
func tagAnonymousAnalyzeSentry(r *http.Request, status int, outcome string) {
	if !observability.SentryEnabled() {
		return
	}
	hub := sentry.CurrentHub().Clone()
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag(anonymousAnalyzeTagKey, anonymousAnalyzeTagValue)
		scope.SetTag("anonymous_analyze_outcome", outcome)
		scope.SetTag("status", strconv.Itoa(status))
		if r != nil {
			scope.SetTag("method", r.Method)
			scope.SetTag("path", r.URL.Path)
			scope.SetRequest(r)
		}
	})
	hub.AddBreadcrumb(&sentry.Breadcrumb{
		Category: "anonymous_analyze",
		Message:  outcome,
		Level:    sentry.LevelInfo,
		Data: map[string]any{
			"status":  status,
			"outcome": outcome,
		},
	}, nil)
}

// alertAnonymousAnalyzeBreakerOnce sends a Sentry CaptureMessage exactly
// once per UTC day when the cost circuit-breaker first trips. Subsequent
// trips on the same day are suppressed so the alert channel does not flood
// during sustained over-budget conditions.
func alertAnonymousAnalyzeBreakerOnce(spent float64, ceiling float64) {
	if !observability.SentryEnabled() {
		return
	}
	hub := sentry.CurrentHub().Clone()
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag(anonymousAnalyzeTagKey, anonymousAnalyzeTagValue)
		scope.SetTag("anonymous_analyze_outcome", "circuit_breaker_tripped")
		scope.SetExtra("projected_spend_usd", spent)
		scope.SetExtra("ceiling_usd", ceiling)
		scope.SetLevel(sentry.LevelError)
	})
	hub.CaptureMessage("anonymous_analyze cost circuit-breaker tripped")
}

// handleAnonymousAnalyze serves POST /public/matches/analyze.
//
// Request body: {"url": "https://www.olx.bg/..."} — same contract as the
// authenticated /matches/analyze handler. mission_id is intentionally not
// supported on the anonymous path because there is no user to scope a
// mission to.
//
// Response: identical envelope shape to the authenticated path:
//
//	{
//	  "listing":          <enriched Listing>,
//	  "reasoning_source": "...",
//	  "search_advice":    "...",
//	  "comparables":      [...],
//	  "market_average":   <int>
//	}
//
// Order of guards: rate-limit → cache → cost breaker → fetch → score.
// Cache hits intentionally consume rate-limit budget so an attacker cannot
// replay one URL infinitely cheaply.
func (s *Server) handleAnonymousAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.scorer == nil {
		writeError(w, http.StatusServiceUnavailable, "scorer is not configured")
		return
	}
	s.initAnonymousAnalyze()

	now := nowAnonymousAnalyze()

	// 1. Per-IP rate limit (anonymous path only).
	clientIP := requestIP(r, s.cfg.TrustProxy)
	ipKey := ""
	if clientIP != nil {
		ipKey = clientIP.String()
	} else {
		// Fall back to RemoteAddr verbatim when parsing failed; better than
		// pooling all unknown clients into a single bucket.
		ipKey = strings.TrimSpace(r.RemoteAddr)
	}
	if allowed, retry := s.anonymousAnalyzeRateLimiter.Allow(ipKey, now); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		tagAnonymousAnalyzeSentry(r, http.StatusTooManyRequests, "rate_limited")
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded; retry later")
		return
	}

	// Decode body.
	var body struct {
		URL string `json:"url" validate:"required,url"`
	}
	if err := Decode(r, &body); err != nil {
		tagAnonymousAnalyzeSentry(r, http.StatusBadRequest, "bad_request")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rawURL := strings.TrimSpace(body.URL)
	if rawURL == "" {
		tagAnonymousAnalyzeSentry(r, http.StatusBadRequest, "missing_url")
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	cacheKey := canonicalizeAnonymousAnalyzeURL(rawURL)

	// 2. URL cache short-circuit.
	if cached, ok := s.anonymousAnalyzeCache.Get(cacheKey, now); ok {
		tagAnonymousAnalyzeSentry(r, http.StatusOK, "cache_hit")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Anonymous-Analyze-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cached)
		return
	}

	// 3. Daily cost circuit-breaker. ORDER: check the global $3/24h cap
	// FIRST (more restrictive — Decision Log 2026-04-27 makes the global
	// cap take precedence over the local $5/day anon cap whichever fires
	// first). If global is already exhausted, return 503 with a distinct
	// outcome tag so ops can tell global vs local apart.
	//
	// Note: we use Snapshot (not Allow) here because the actual pre-spend
	// happens inside scorer.Score downstream — pre-spending here too would
	// double-charge the global ledger. Snapshot is a read-only check.
	if budget := aibudget.Global(); budget != nil {
		snap := budget.Snapshot()
		if snap.CapUSD > 0 && snap.Rolling24hSpendUSD >= snap.CapUSD {
			// Compute retry: time until oldest entry rolls off, clamped
			// to >=1s so clients don't busy-loop.
			retrySec := 1
			if !snap.OldestEntryAt.IsZero() {
				delta := int(snap.OldestEntryAt.Add(aibudget.Window).Sub(now).Seconds())
				if delta > retrySec {
					retrySec = delta
				}
			}
			w.Header().Set("Retry-After", strconv.Itoa(retrySec))
			tagAnonymousAnalyzeSentry(r, http.StatusServiceUnavailable, "circuit_breaker_global")
			writeError(w, http.StatusServiceUnavailable, "global daily AI quota exhausted; try again later")
			return
		}
	}

	projected := s.anonymousAnalyzeBudget.addAndProject(estimatedCostPerAnonymousCallUSD, now)
	if projected > anonymousAnalyzeCostCeilingUSD {
		// Roll the projection back so future calls see the actual spend
		// (excluding the call we are about to refuse).
		s.anonymousAnalyzeBudget.rollback(estimatedCostPerAnonymousCallUSD)
		retry := secondsUntilUTCMidnight(now)
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		if s.anonymousAnalyzeBudget.markBreakerNotified(now) {
			alertAnonymousAnalyzeBreakerOnce(projected, anonymousAnalyzeCostCeilingUSD)
		}
		tagAnonymousAnalyzeSentry(r, http.StatusServiceUnavailable, "circuit_breaker_local")
		writeError(w, http.StatusServiceUnavailable, "anonymous-analyze daily quota exhausted; try again after UTC midnight")
		return
	}

	// 4. Fetch + score. Mirrors the authenticated handler's pipeline but
	// without mission resolution (no user → no mission). Cap fetch at 25s
	// to match handleAnalyzeListing.
	fetchCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	listing, err := s.anonymousAnalyzeFetch(fetchCtx, rawURL)
	if err != nil {
		// We have not actually invoked the AI yet, so refund the projected
		// cost we tentatively booked — only paid calls should count toward
		// the daily ceiling.
		s.anonymousAnalyzeBudget.rollback(estimatedCostPerAnonymousCallUSD)
		tagAnonymousAnalyzeSentry(r, http.StatusBadRequest, "fetch_failed")
		writeError(w, http.StatusBadRequest, "failed to load listing: "+err.Error())
		return
	}

	spec := models.SearchSpec{
		MarketplaceID:   listing.MarketplaceID,
		Query:           listing.Title,
		OfferPercentage: 72,
	}

	scored := s.anonymousAnalyzeScore(fetchCtx, listing, spec)

	// Reconcile the conservative pre-spend estimate against the actual cost
	// reported by the scorer (W19-3). The pre-spend projection is intentionally
	// conservative ($0.01/call) so concurrent bursts trip the breaker early;
	// after the scorer returns, we charge the delta when actual > estimate
	// (under-estimated, charge the gap so the daily ledger is honest) and
	// rebate the delta when actual < estimate (over-estimated, refund the gap
	// so an over-conservative pre-spend doesn't permanently hold budget).
	//
	// Heuristic / cache / rate-limited paths report CostUSD == 0; in that case
	// reconcile fully refunds the pre-spend projection because no AI call was
	// actually paid for.
	delta := scored.CostUSD - estimatedCostPerAnonymousCallUSD
	if delta > 0 {
		s.anonymousAnalyzeBudget.addAndProject(delta, now)
	} else if delta < 0 {
		s.anonymousAnalyzeBudget.rollback(-delta)
	}

	enriched := scored.Listing
	enriched.Score = scored.Score
	enriched.FairPrice = scored.FairPrice
	enriched.OfferPrice = scored.OfferPrice
	enriched.Confidence = scored.Confidence
	enriched.Reason = scored.Reason
	enriched.RiskFlags = scored.RiskFlags
	enriched.RecommendedAction = scored.RecommendedAction

	payload := map[string]any{
		"listing":          enriched,
		"reasoning_source": scored.ReasoningSource,
		"search_advice":    scored.SearchAdvice,
		"comparables":      scored.ComparableDeals,
		"market_average":   scored.MarketAverage,
	}

	// Marshal once; reuse for both the response and the cache so a future
	// hit replays the exact byte sequence the first caller saw.
	encoded, err := json.Marshal(payload)
	if err != nil {
		tagAnonymousAnalyzeSentry(r, http.StatusInternalServerError, "encode_failed")
		writeError(w, http.StatusInternalServerError, "failed to encode response")
		return
	}
	s.anonymousAnalyzeCache.Put(cacheKey, encoded, now)

	tagAnonymousAnalyzeSentry(r, http.StatusOK, "ok")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Anonymous-Analyze-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// anonymousAnalyzeFetch is an indirection over the listing fetcher used only
// by the anonymous path. Tests override it (via Server.SetAnonymousAnalyzeHooks)
// to avoid hitting the network. The authenticated path continues to call
// s.fetcher.Fetch directly so its behaviour is unchanged.
func (s *Server) anonymousAnalyzeFetch(ctx context.Context, rawURL string) (models.Listing, error) {
	if s.anonymousFetchOverride != nil {
		return s.anonymousFetchOverride(ctx, rawURL)
	}
	return s.fetcher.Fetch(ctx, rawURL)
}

// anonymousAnalyzeScore is an indirection over the scorer used only by the
// anonymous path. Tests override it to return a deterministic ScoredListing
// without invoking the AI/reasoner. The authenticated path continues to
// call s.scorer.Score directly so its behaviour is unchanged.
func (s *Server) anonymousAnalyzeScore(ctx context.Context, listing models.Listing, spec models.SearchSpec) models.ScoredListing {
	if s.anonymousScoreOverride != nil {
		return s.anonymousScoreOverride(ctx, listing, spec)
	}
	return s.scorer.Score(ctx, listing, spec)
}

// SetAnonymousAnalyzeHooks installs test overrides for the fetcher and
// scorer used by /public/matches/analyze. Either argument may be nil to
// fall back to the real implementation. Intended only for tests.
func (s *Server) SetAnonymousAnalyzeHooks(
	fetch func(context.Context, string) (models.Listing, error),
	score func(context.Context, models.Listing, models.SearchSpec) models.ScoredListing,
) {
	s.anonymousFetchOverride = fetch
	s.anonymousScoreOverride = score
}
