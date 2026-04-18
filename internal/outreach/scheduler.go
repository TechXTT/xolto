package outreach

import (
	"context"
	"log/slog"
	"time"
)

// StaleTransitioner is the subset of the store needed by the stale-transition
// background goroutine. Satisfied by *store.PostgresStore and *store.SQLiteStore.
type StaleTransitioner interface {
	TransitionStaleThreads(ctx context.Context, cutoff time.Duration) (int64, error)
}

// StartStaleTransitionScheduler starts a background goroutine that wakes every
// interval and transitions any outreach threads whose last_state_transition_at
// is older than staleCutoff from awaiting_reply to stale.
//
// The goroutine respects ctx.Done() and exits cleanly when the context is
// cancelled. It logs the number of transitioned threads at INFO level on each
// run.
func StartStaleTransitionScheduler(ctx context.Context, st StaleTransitioner, interval time.Duration, staleCutoff time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, err := st.TransitionStaleThreads(ctx, staleCutoff)
				if err != nil {
					slog.Default().Error(
						"outreach stale transition failed",
						"op", "outreach.stale_transition",
						"error", err,
					)
					continue
				}
				if count > 0 {
					slog.Default().Info(
						"outreach stale threads transitioned",
						"op", "outreach.stale_transition",
						"count", count,
					)
				}
			}
		}
	}()
}
