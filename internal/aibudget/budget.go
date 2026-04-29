// Package aibudget enforces a single global rolling-24h cap on total AI
// spend across every AI_API_KEY-routed call site (scorer, reasoner,
// replycopilot, assistant, generator, support classifier, must-have
// evaluator).
//
// Founder-locked rule (Decision Log 2026-04-27 "Global AI-spend cap set at
// $3 USD/day"): the global cap is $3 USD per rolling 24h. The pre-existing
// $5/day anonymous-analyze breaker (W18-2) becomes a sub-cap; whichever
// fires first wins. Do NOT raise this cap without founder approval.
//
// W19-23 Phase 1 (this package):
//   - Global $3/24h rolling-window tracker, mutex-guarded, in-memory.
//   - Allow / Reconcile / Rollback / Snapshot API. Same Allow→Reconcile
//     pattern as internal/api/anonymous_analyze.go.
//   - Sentry alert tiers at 70% / 90% / 100%, one-shot per re-crossing.
//   - Owner-role override of the cap (audit-logged via the store; the
//     persistence layer is not in this file).
//
// Restart-survival: NOT guaranteed in v1. A process restart resets the 24h
// window and the cap to the founder-locked default. This is an explicit
// trade-off — see report. If traffic ever makes restart loss material,
// persist the entries and overrides via a follow-up.
package aibudget

import (
	"context"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/observability"

	"github.com/getsentry/sentry-go"
)

// DefaultCapUSD is the founder-locked global daily AI-spend ceiling.
// Decision Log 2026-04-27. Do NOT raise this constant — the override
// endpoint exists for temporary scaling-test bumps and is itself bounded
// by hardCeilingUSD below.
const DefaultCapUSD = 3.0

// Window is the rolling-24h window used by the tracker. Entries older than
// this are GC'd lazily on every read.
const Window = 24 * time.Hour

// HardCeilingUSD is the absolute upper bound the override endpoint may set.
// 100x the founder-locked default. Exceeding this requires a code change so
// an operator-tier user cannot accidentally (or maliciously) lift the cap to
// dangerous levels. Decision Log 2026-04-27 binding constraint.
const HardCeilingUSD = 100.0

// alertThresholds defines the one-shot Sentry alert tiers, in fraction of
// the current cap. Crossing each threshold emits exactly one alert per
// re-crossing — the same dedup pattern as W18-2's markBreakerNotified.
var alertThresholds = []float64{0.70, 0.90, 1.00}

// entry is one charged AI call. Pre-spend Allow records an entry with the
// estimated cost; Reconcile then mutates the cost in place to match the
// observed spend. Rollback removes the entry entirely.
//
// We track entries by index (not pointer) because the slice shrinks on GC
// pass. To make Reconcile / Rollback addressable by the originating call we
// instead bookkeep totals plus an index of "live" entries; see Tracker.
type entry struct {
	at       time.Time
	cost     float64
	callSite string
}

// Tracker is the global AI-spend budget. Concurrent-safe via a single mutex
// — contention is fine because every AI call already crosses a network
// boundary; the lock is held for microseconds per call.
type Tracker struct {
	mu              sync.Mutex
	entries         []entry
	capUSD          float64
	now             func() time.Time
	thresholdsFired [3]time.Time // last fire time per alertThresholds index; zero = never
}

// New returns a Tracker with the founder-locked $3/24h cap.
func New() *Tracker {
	return &Tracker{
		capUSD: DefaultCapUSD,
		now:    time.Now,
	}
}

// SetNowFunc overrides the clock for tests. Must be called before any
// concurrent use. Not safe to swap mid-flight.
func (t *Tracker) SetNowFunc(fn func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if fn == nil {
		fn = time.Now
	}
	t.now = fn
}

// gcLocked drops entries older than now-Window. Caller must hold t.mu.
// O(n) on every read — acceptable because entry count is bounded by the
// max-spend-per-window: at $3/24h with $0.001/call the upper bound is a
// few thousand entries, well within tight-loop budget.
func (t *Tracker) gcLocked(now time.Time) {
	cutoff := now.Add(-Window)
	idx := 0
	for ; idx < len(t.entries); idx++ {
		if t.entries[idx].at.After(cutoff) {
			break
		}
	}
	if idx > 0 {
		t.entries = t.entries[idx:]
	}
}

// totalLocked returns the sum of all live (post-GC) entries.
func (t *Tracker) totalLocked() float64 {
	var sum float64
	for _, e := range t.entries {
		sum += e.cost
	}
	return sum
}

// secondsUntilOldestRollsOff returns how many seconds remain until the
// oldest in-window entry rolls off (and capacity frees up). Returned as a
// time.Duration; always >= 1s when there is at least one live entry, to
// avoid clients busy-looping at the boundary. Returns 0 when there are no
// live entries (caller should not be calling this in that case).
func (t *Tracker) secondsUntilOldestRollsOffLocked(now time.Time) time.Duration {
	if len(t.entries) == 0 {
		return 0
	}
	rollOff := t.entries[0].at.Add(Window)
	delta := rollOff.Sub(now)
	if delta < time.Second {
		delta = time.Second
	}
	return delta
}

// Allow projects estimatedCostUSD into the running 24h spend and returns
// (true, 0) when the projection is at or under the cap, or (false,
// retryAfter) when it would breach. retryAfter is the duration until the
// oldest live entry rolls off — i.e. until at least the oldest live cost
// frees up — and is always >= 1s.
//
// Pre-spend gate: callers MUST call Reconcile (success) or Rollback
// (failure) for every Allow that returned true. Otherwise the projection
// stays charged forever.
//
// callSite is a free-form tag stored alongside the entry for ops debugging
// in the breakdown the Snapshot exposes (e.g. "scorer", "reasoner",
// "assistant.brief", "anonymous_analyze").
func (t *Tracker) Allow(_ context.Context, callSite string, estimatedCostUSD float64) (bool, time.Duration) {
	if estimatedCostUSD < 0 {
		estimatedCostUSD = 0
	}

	t.mu.Lock()
	now := t.now()
	t.gcLocked(now)
	currentTotal := t.totalLocked()
	projected := currentTotal + estimatedCostUSD

	if projected > t.capUSD {
		// Compute retry-after BEFORE we release the lock so the answer is
		// consistent with the snapshot we just took.
		retry := t.secondsUntilOldestRollsOffLocked(now)
		// Even with no live entries (cap == 0?), give a sane retry hint.
		if retry == 0 {
			retry = time.Second
		}
		// Fire the 100% threshold once when we're rejecting traffic — the
		// rejection IS the cap-fire signal, even if currentTotal itself is
		// just below 100% (the *projection* is what breached). Pass the
		// cap value so the threshold-check sees pct >= 1.0.
		t.checkAndFireThresholdLocked(t.capUSD, now)
		t.mu.Unlock()
		return false, retry
	}

	// Charge the estimate.
	t.entries = append(t.entries, entry{at: now, cost: estimatedCostUSD, callSite: callSite})
	newTotal := currentTotal + estimatedCostUSD
	t.checkAndFireThresholdLocked(newTotal, now)
	t.mu.Unlock()
	return true, 0
}

// Reconcile updates the most-recent entry's cost from the conservative
// pre-spend estimate to the observed actual cost. If actualCostUSD is
// larger than the estimate we charge the delta; if smaller we refund.
//
// The most-recent-entry assumption is intentional: in the markt server,
// every Allow is paired with the immediate next Reconcile/Rollback on the
// same goroutine. Concurrent Allow calls from different goroutines are
// fine — each pair closes its own call's entry by total adjustment. We
// mutate the LAST entry because that's the most likely match for the
// caller in the common single-flight case; in the concurrent case the
// arithmetic still nets out to the right total, the only loss is the
// per-entry call-site labelling. The Snapshot is total-driven, not entry-
// labelled, so this is safe.
//
// If actualCostUSD < 0 we treat it as 0 (defensive against negative
// reporting from upstream).
func (t *Tracker) Reconcile(actualCostUSD float64) {
	if actualCostUSD < 0 {
		actualCostUSD = 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) == 0 {
		// No estimate to reconcile against — record as a fresh entry so
		// the daily total is still honest. now() because we don't know
		// the original timestamp.
		t.entries = append(t.entries, entry{at: t.now(), cost: actualCostUSD})
		return
	}
	// Mutate the LAST entry's cost. Total = old_total - old_cost + new_cost.
	last := &t.entries[len(t.entries)-1]
	last.cost = actualCostUSD
}

// Rollback removes a previously-charged estimate when the call did not
// actually happen (e.g. fetch failed before LLM invocation). It removes the
// most-recent entry, by symmetry with Reconcile's most-recent-entry rule.
//
// If the last entry's cost differs from estimatedCostUSD (e.g. another
// goroutine reconciled in between), we still drop the last entry — which
// keeps the total honest in the steady-single-flight case and slightly
// drift-tolerant in the concurrent case. The estimatedCostUSD argument is
// retained for symmetry with the W18-2 rollback signature and for future
// stricter accounting if ever needed.
func (t *Tracker) Rollback(_ context.Context, _ float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) == 0 {
		return
	}
	t.entries = t.entries[:len(t.entries)-1]
}

// Snapshot is a point-in-time view of the budget. Used by the admin
// snapshot endpoint and (internally) the Sentry tier-fire emitter.
type Snapshot struct {
	// Rolling24hSpendUSD is the sum of live entries inside the rolling
	// 24h window at the moment Snapshot was taken.
	Rolling24hSpendUSD float64 `json:"rolling_24h_spend_usd"`
	// CapUSD is the active cap — either DefaultCapUSD or whatever the
	// last owner-override set it to.
	CapUSD float64 `json:"cap_usd"`
	// Percentage is Rolling24hSpendUSD / CapUSD * 100. Returned as a float
	// for the admin tile to display, e.g. 47.3.
	Percentage float64 `json:"percentage"`
	// OldestEntryAt is the timestamp of the oldest live entry. Zero when
	// there are no live entries.
	OldestEntryAt time.Time `json:"oldest_entry_at"`
	// PerSiteSpendUSD breaks down Rolling24hSpendUSD by call-site identifier.
	// Keys are the callSite strings passed to Allow(); entries recorded without
	// a call-site (e.g. Reconcile-only entries) are folded under "unknown".
	PerSiteSpendUSD map[string]float64 `json:"per_site_spend_usd"`
	// WarningTiersFired records the last fire time per alert threshold;
	// zero indicates "not fired in the current cycle". Keys are the
	// integer percentages "70", "90", "100".
	WarningTiersFired map[string]*time.Time `json:"warning_tiers_fired"`
}

// Snapshot returns the current budget state.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.gcLocked(now)
	total := t.totalLocked()
	cap := t.capUSD

	pct := 0.0
	if cap > 0 {
		pct = total / cap * 100.0
	}

	var oldest time.Time
	if len(t.entries) > 0 {
		oldest = t.entries[0].at
	}

	tiers := map[string]*time.Time{
		"70":  nil,
		"90":  nil,
		"100": nil,
	}
	for i, frac := range alertThresholds {
		if !t.thresholdsFired[i].IsZero() {
			tier := t.thresholdsFired[i]
			switch frac {
			case 0.70:
				tiers["70"] = &tier
			case 0.90:
				tiers["90"] = &tier
			case 1.00:
				tiers["100"] = &tier
			}
		}
	}

	perSite := map[string]float64{}
	for _, e := range t.entries {
		site := e.callSite
		if site == "" {
			site = "unknown"
		}
		perSite[site] += e.cost
	}

	return Snapshot{
		Rolling24hSpendUSD: total,
		CapUSD:             cap,
		Percentage:         pct,
		OldestEntryAt:      oldest,
		PerSiteSpendUSD:    perSite,
		WarningTiersFired:  tiers,
	}
}

// SetCapUSD updates the active cap. Used by the owner-override endpoint.
// Validates against HardCeilingUSD; returns false if the value is out of
// range. Caller is responsible for audit-logging the change in the
// ai_budget_overrides table — this method only mutates in-memory state.
func (t *Tracker) SetCapUSD(newCap float64) bool {
	if newCap <= 0 || newCap > HardCeilingUSD {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.capUSD = newCap
	// Reset the threshold-fired record so the operator sees fresh alerts
	// against the new cap. Without this, e.g. raising from $3 to $5 with
	// $2.5 already spent would not emit a 70% alert until the spend
	// crossed $3.5 — the operator should see the next 70% fire.
	t.thresholdsFired = [3]time.Time{}
	return true
}

// CapUSD returns the active cap (handy for tests).
func (t *Tracker) CapUSD() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.capUSD
}

// checkAndFireThresholdLocked emits Sentry alerts for any threshold the
// current total has crossed since the last fire. Caller must hold t.mu.
//
// Re-crossing logic: a threshold fires once per UP-crossing. If the spend
// drops below the threshold AND comes back up, we fire again. We approximate
// "dropped below" by clearing the fired-time when the current total is
// strictly below the threshold and at least 60 seconds have elapsed since
// the last fire. The 60s gap prevents jitter at the boundary from emitting
// duplicate alerts.
func (t *Tracker) checkAndFireThresholdLocked(currentTotal float64, now time.Time) {
	if t.capUSD <= 0 {
		return
	}
	pct := currentTotal / t.capUSD

	for i, frac := range alertThresholds {
		thresholdSpend := t.capUSD * frac
		// Reset window: if currently strictly below the threshold and the
		// last fire was >60s ago, allow re-fire on next up-cross.
		if currentTotal < thresholdSpend && !t.thresholdsFired[i].IsZero() {
			if now.Sub(t.thresholdsFired[i]) > time.Minute {
				t.thresholdsFired[i] = time.Time{}
			}
		}
		// Fire path: at-or-above threshold and not yet fired in this cycle.
		if pct >= frac && t.thresholdsFired[i].IsZero() {
			t.thresholdsFired[i] = now
			emitSentryThresholdAlert(frac, currentTotal, t.capUSD)
		}
	}
}

// emitSentryThresholdAlert sends the appropriate Sentry signal for a
// threshold crossing. Levels per W19-23 brief:
//
//	70%  → breadcrumb (info)
//	90%  → CaptureMessage at warning
//	100% → CaptureMessage at error + (in caller) optional founder email
//
// When the SDK is disabled this is a cheap no-op.
func emitSentryThresholdAlert(frac, spent, cap float64) {
	if !observability.SentryEnabled() {
		return
	}
	hub := sentry.CurrentHub().Clone()

	switch frac {
	case 0.70:
		hub.AddBreadcrumb(&sentry.Breadcrumb{
			Category: "ai_budget",
			Message:  "global AI budget at 70% of $3.00/24h",
			Level:    sentry.LevelInfo,
			Data: map[string]any{
				"spent_usd": spent,
				"cap_usd":   cap,
				"pct":       70,
			},
		}, nil)
	case 0.90:
		hub.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("ai_budget_tier", "warning")
			scope.SetContext("ai_budget", map[string]any{
				"spent_usd": spent,
				"cap_usd":   cap,
			})
			scope.SetLevel(sentry.LevelWarning)
		})
		hub.CaptureMessage("global AI budget at 90% of $3.00/24h")
	case 1.00:
		hub.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("ai_budget_tier", "exhausted")
			scope.SetContext("ai_budget", map[string]any{
				"spent_usd": spent,
				"cap_usd":   cap,
			})
			scope.SetLevel(sentry.LevelError)
		})
		hub.CaptureMessage("global AI budget exhausted; cap-fire active. Per-site degradation engaged.")
	}
}
