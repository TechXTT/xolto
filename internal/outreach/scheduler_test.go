package outreach

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type stubTransitioner struct {
	callCount atomic.Int64
	returnN   int64
	returnErr error
}

func (s *stubTransitioner) TransitionStaleThreads(_ context.Context, _ time.Duration) (int64, error) {
	s.callCount.Add(1)
	return s.returnN, s.returnErr
}

func TestStartStaleTransitionScheduler_CallsStoreAndExitsOnCancel(t *testing.T) {
	st := &stubTransitioner{returnN: 2}
	ctx, cancel := context.WithCancel(context.Background())

	StartStaleTransitionScheduler(ctx, st, 10*time.Millisecond, 7*24*time.Hour)

	// Wait for at least one tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if st.callCount.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.callCount.Load() < 1 {
		t.Fatal("expected TransitionStaleThreads to be called at least once within 500ms")
	}

	// Cancel context and verify the goroutine exits cleanly (no more calls after cancel).
	cancel()
	time.Sleep(30 * time.Millisecond)
	countAfterCancel := st.callCount.Load()
	time.Sleep(30 * time.Millisecond)
	if st.callCount.Load() != countAfterCancel {
		t.Fatal("expected no more TransitionStaleThreads calls after context cancel")
	}
}

func TestStartStaleTransitionScheduler_ToleratesStoreError(t *testing.T) {
	st := &stubTransitioner{returnN: 0, returnErr: errStub}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartStaleTransitionScheduler(ctx, st, 10*time.Millisecond, 7*24*time.Hour)

	// Scheduler should keep running after an error.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if st.callCount.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.callCount.Load() < 2 {
		t.Fatal("expected scheduler to keep running after store error; got fewer than 2 calls")
	}
}

// errStub is a sentinel error for tests.
var errStub = stubError("store error")

type stubError string

func (e stubError) Error() string { return string(e) }
