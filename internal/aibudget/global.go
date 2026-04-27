package aibudget

import "sync"

// Global is the process-wide AI-spend tracker. Populated by main / server
// wiring at startup via SetGlobal; consumers (scorer, reasoner,
// replycopilot, assistant, generator, support classifier, must-have
// evaluator) call Global() to gate every LLM invocation.
//
// Background: the budget MUST be a singleton across packages — it's a
// process-level cap, not a per-package cap. Wiring the same *Tracker via
// constructor injection into every callsite would explode the surface
// area for a 1-line concern. Test code overrides it via SetGlobalForTest.

var (
	globalMu      sync.RWMutex
	globalTracker *Tracker
)

// SetGlobal installs the process-wide tracker. Safe to call once at
// startup. Subsequent calls replace the tracker — typically only used by
// tests resetting state.
func SetGlobal(t *Tracker) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalTracker = t
}

// Global returns the process-wide tracker. Callers MUST handle nil — when
// the tracker is not installed (e.g. tests that don't bother wiring it),
// the call site should treat the budget as unconstrained and proceed with
// the LLM call. This matches the existing W18-2 behaviour where a missing
// limiter does not block production traffic.
func Global() *Tracker {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalTracker
}

// EstimatedCostPerCallUSD is the conservative pre-spend estimate used by
// every call site. Same value as the W18-2 anonymous-analyze breaker —
// $0.01/call rounds up ~5x the true expectation for gpt-5-mini, so a burst
// of concurrent calls trips the global cap early rather than late.
//
// W19-3 plumbed real per-call cost via ScoredListing.CostUSD and
// DealAnalysis.CostUSD; callers must Reconcile against the real value
// post-call so the rolling sum stays honest. See the package doc comment
// in budget.go for the full Allow → LLM → Reconcile flow.
const EstimatedCostPerCallUSD = 0.01
