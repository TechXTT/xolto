package aibudget

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock returns a controllable clock for tests. The current time is
// stored atomically so tests can drive deterministic windows without race
// conditions when tests mix .Set with concurrent Allow/Reconcile.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }
func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}
func (f *fakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newTrackerAt(t *testing.T, start time.Time) (*Tracker, *fakeClock) {
	t.Helper()
	clk := newFakeClock(start)
	tr := New()
	tr.SetNowFunc(clk.Now)
	return tr, clk
}

func TestAllowWithinCapReturnsTrue(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	ok, retry := tr.Allow(context.Background(), "scorer", 0.01)
	if !ok {
		t.Fatalf("expected allow=true within cap, got false")
	}
	if retry != 0 {
		t.Fatalf("expected retry=0 within cap, got %v", retry)
	}
	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD != 0.01 {
		t.Fatalf("expected spend=0.01, got %v", snap.Rolling24hSpendUSD)
	}
	if snap.CapUSD != DefaultCapUSD {
		t.Fatalf("expected cap=%v, got %v", DefaultCapUSD, snap.CapUSD)
	}
}

func TestAllowOverCapReturnsFalseWithRetryAfter(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	tr, clk := newTrackerAt(t, now)

	// Fill up exactly to the cap.
	ok, _ := tr.Allow(context.Background(), "scorer", DefaultCapUSD)
	if !ok {
		t.Fatalf("first Allow should succeed at exactly cap")
	}

	// Advance 30 minutes; oldest entry now ~30m old.
	clk.Advance(30 * time.Minute)

	// Next Allow with any positive estimate should fail.
	ok, retry := tr.Allow(context.Background(), "reasoner", 0.01)
	if ok {
		t.Fatalf("expected Allow to fail when projecting over cap")
	}
	// Retry-after should be roughly Window - 30min = 23.5h.
	wantMin := 23*time.Hour + 29*time.Minute
	wantMax := 23*time.Hour + 31*time.Minute
	if retry < wantMin || retry > wantMax {
		t.Fatalf("expected retry near 23h30m, got %v", retry)
	}
}

func TestReconcileEstimateTooHighRefunds(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	tr.Allow(context.Background(), "scorer", 0.01)
	tr.Reconcile(0.002) // actual 0.002, estimate 0.01 → refund 0.008

	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD-0.002 > 1e-9 || snap.Rolling24hSpendUSD-0.002 < -1e-9 {
		t.Fatalf("expected spend=0.002 after reconcile, got %v", snap.Rolling24hSpendUSD)
	}
}

func TestReconcileEstimateTooLowCharges(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	tr.Allow(context.Background(), "scorer", 0.01)
	tr.Reconcile(0.025) // actual 0.025, estimate 0.01 → charge extra 0.015

	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD-0.025 > 1e-9 || snap.Rolling24hSpendUSD-0.025 < -1e-9 {
		t.Fatalf("expected spend=0.025 after reconcile, got %v", snap.Rolling24hSpendUSD)
	}
}

func TestRollbackClearsEstimate(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	tr.Allow(context.Background(), "scorer", 0.01)
	tr.Rollback(context.Background(), 0.01)

	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD != 0 {
		t.Fatalf("expected spend=0 after rollback, got %v", snap.Rolling24hSpendUSD)
	}
}

func TestWindowRollDropsOldEntries(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	tr, clk := newTrackerAt(t, now)

	tr.Allow(context.Background(), "scorer", 1.0)
	tr.Allow(context.Background(), "scorer", 1.0)

	// Advance 25h; both entries should now be outside the rolling window.
	clk.Advance(25 * time.Hour)

	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD != 0 {
		t.Fatalf("expected old entries to roll off, got spend=%v", snap.Rolling24hSpendUSD)
	}
	// And a new Allow should succeed.
	ok, _ := tr.Allow(context.Background(), "scorer", 1.0)
	if !ok {
		t.Fatalf("expected fresh Allow after window roll, got blocked")
	}
}

func TestConcurrentAccessRaceFree(t *testing.T) {
	// Run with `go test -race` to validate.
	tr := New()
	const goroutines = 64
	const callsEach = 200

	var wg sync.WaitGroup
	var allowed atomic.Int64
	var blocked atomic.Int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < callsEach; i++ {
				ok, _ := tr.Allow(context.Background(), "stress", 0.001)
				if ok {
					allowed.Add(1)
					tr.Reconcile(0.001)
				} else {
					blocked.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// Total spend must not exceed the cap.
	snap := tr.Snapshot()
	if snap.Rolling24hSpendUSD > snap.CapUSD+1e-6 {
		t.Fatalf("cap breached under concurrency: spend=%v cap=%v", snap.Rolling24hSpendUSD, snap.CapUSD)
	}
	// Allowed + blocked must equal total attempts.
	totalAttempts := int64(goroutines * callsEach)
	if allowed.Load()+blocked.Load() != totalAttempts {
		t.Fatalf("allowed+blocked != attempts: got %d+%d, want %d", allowed.Load(), blocked.Load(), totalAttempts)
	}
}

func TestSnapshotReportsOldestEntry(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	tr, clk := newTrackerAt(t, now)

	tr.Allow(context.Background(), "scorer", 0.01)
	clk.Advance(time.Hour)
	tr.Allow(context.Background(), "scorer", 0.01)

	snap := tr.Snapshot()
	if !snap.OldestEntryAt.Equal(now) {
		t.Fatalf("expected oldest=%v, got %v", now, snap.OldestEntryAt)
	}
}

func TestSetCapUSDValid(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	if !tr.SetCapUSD(5.0) {
		t.Fatalf("SetCapUSD(5.0) should succeed")
	}
	if got := tr.CapUSD(); got != 5.0 {
		t.Fatalf("expected cap=5.0, got %v", got)
	}
}

func TestSetCapUSDRejectsOutOfRange(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	if tr.SetCapUSD(-1) {
		t.Fatalf("SetCapUSD(-1) should fail")
	}
	if tr.SetCapUSD(0) {
		t.Fatalf("SetCapUSD(0) should fail")
	}
	if tr.SetCapUSD(HardCeilingUSD + 1) {
		t.Fatalf("SetCapUSD over hard ceiling should fail")
	}
	if got := tr.CapUSD(); got != DefaultCapUSD {
		t.Fatalf("cap should be unchanged after rejected sets, got %v", got)
	}
}

func TestThresholdAlertsFireOnceUntilRecross(t *testing.T) {
	tr, clk := newTrackerAt(t, time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC))

	// Fire 70% — at $2.10 spend.
	tr.Allow(context.Background(), "scorer", 2.1)
	snap := tr.Snapshot()
	if snap.WarningTiersFired["70"] == nil {
		t.Fatalf("expected 70%% threshold fired")
	}
	if snap.WarningTiersFired["90"] != nil {
		t.Fatalf("expected 90%% NOT fired yet")
	}

	// Cross 90% — to $2.75 total.
	tr.Allow(context.Background(), "scorer", 0.65)
	snap = tr.Snapshot()
	if snap.WarningTiersFired["90"] == nil {
		t.Fatalf("expected 90%% threshold fired")
	}

	// Cross 100% — to $3.05 total. This should be REFUSED but the 100%
	// alert should still fire.
	ok, _ := tr.Allow(context.Background(), "scorer", 0.30)
	if ok {
		t.Fatalf("expected Allow to fail when projecting over cap")
	}
	snap = tr.Snapshot()
	if snap.WarningTiersFired["100"] == nil {
		t.Fatalf("expected 100%% threshold fired on rejected over-cap Allow")
	}

	// Drop below 70% via window roll.
	clk.Advance(25 * time.Hour)

	// Now charge a small amount. The 70% threshold should NOT re-fire
	// immediately at $0.10 because we're well below 70% of $3 (= $2.10).
	tr.Allow(context.Background(), "scorer", 0.10)
	snap = tr.Snapshot()
	// After the rolloff and a sub-threshold spend, plus >60s gap, the 70%
	// fired-time should reset to nil because the spend is now strictly
	// below the threshold.
	if snap.WarningTiersFired["70"] != nil {
		t.Fatalf("expected 70%% to reset after drop+gap, got non-nil")
	}

	// Re-cross 70% — should fire again.
	tr.Allow(context.Background(), "scorer", 2.1)
	snap = tr.Snapshot()
	if snap.WarningTiersFired["70"] == nil {
		t.Fatalf("expected 70%% to re-fire after re-cross")
	}
}

func TestSetCapResetsThresholds(t *testing.T) {
	tr, _ := newTrackerAt(t, time.Now())
	tr.Allow(context.Background(), "scorer", 2.1) // crosses 70%
	if tr.Snapshot().WarningTiersFired["70"] == nil {
		t.Fatalf("expected 70%% fired pre-override")
	}
	if !tr.SetCapUSD(5.0) {
		t.Fatalf("SetCapUSD failed")
	}
	if tr.Snapshot().WarningTiersFired["70"] != nil {
		t.Fatalf("expected 70%% reset after SetCapUSD")
	}
}

func TestGlobalSingleton(t *testing.T) {
	// Save / restore so this test does not leak.
	orig := Global()
	t.Cleanup(func() { SetGlobal(orig) })

	if Global() != orig {
		t.Fatalf("Global() should be stable until SetGlobal called")
	}
	tr := New()
	SetGlobal(tr)
	if Global() != tr {
		t.Fatalf("SetGlobal(tr) followed by Global() should return tr")
	}
}

func TestEstimatedCostConstantSanity(t *testing.T) {
	// Golden test: ensure the constant didn't drift accidentally below the
	// scorer's true expected spend (~$0.0018 per gpt-5-mini call). The
	// estimate must be conservative — strictly higher than the expectation.
	const expectedCallSpend = 0.002
	if EstimatedCostPerCallUSD < expectedCallSpend {
		t.Fatalf("EstimatedCostPerCallUSD (%v) must be >= scorer expected spend (%v) to keep the breaker conservative",
			EstimatedCostPerCallUSD, expectedCallSpend)
	}
}

func TestSnapshotPerSiteSpendBreakdown(t *testing.T) {
	clk := newFakeClock(time.Now())
	tr := &Tracker{
		capUSD: DefaultCapUSD,
		now:    clk.Now,
	}

	callSites := []string{"scorer", "reasoner.musthave", "assistant.brief"}
	for _, site := range callSites {
		ok, _ := tr.Allow(context.Background(), site, 0.01)
		if !ok {
			t.Fatalf("Allow(%q) returned false unexpectedly", site)
		}
	}

	// An Allow with empty callSite should land under "unknown".
	ok, _ := tr.Allow(context.Background(), "", 0.01)
	if !ok {
		t.Fatal("Allow('') returned false unexpectedly")
	}

	snap := tr.Snapshot()

	for _, site := range callSites {
		got, exists := snap.PerSiteSpendUSD[site]
		if !exists {
			t.Fatalf("PerSiteSpendUSD missing key %q", site)
		}
		const want = 0.01
		if got < want-1e-9 || got > want+1e-9 {
			t.Fatalf("PerSiteSpendUSD[%q] = %v, want %v", site, got, want)
		}
	}

	unknown, exists := snap.PerSiteSpendUSD["unknown"]
	if !exists {
		t.Fatal("PerSiteSpendUSD missing key \"unknown\" for empty-callSite Allow")
	}
	const wantUnknown = 0.01
	if unknown < wantUnknown-1e-9 || unknown > wantUnknown+1e-9 {
		t.Fatalf("PerSiteSpendUSD[\"unknown\"] = %v, want %v", unknown, wantUnknown)
	}
}
