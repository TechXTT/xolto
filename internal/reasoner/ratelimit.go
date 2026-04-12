package reasoner

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu           sync.Mutex
	perUserLimit int
	globalLimit  int
	userCalls    map[string][]time.Time
	globalCalls  []time.Time
	now          func() time.Time
}

func newRateLimiter(perUserLimit, globalLimit int) *rateLimiter {
	if perUserLimit <= 0 && globalLimit <= 0 {
		return nil
	}
	return &rateLimiter{
		perUserLimit: perUserLimit,
		globalLimit:  globalLimit,
		userCalls:    make(map[string][]time.Time),
		now:          time.Now,
	}
}

func (rl *rateLimiter) Allow(userID string) bool {
	if rl == nil {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	cutoff := now.Add(-time.Hour)

	rl.globalCalls = pruneCalls(rl.globalCalls, cutoff)
	if rl.globalLimit > 0 && len(rl.globalCalls) >= rl.globalLimit {
		return false
	}

	if userID != "" {
		pruned := pruneCalls(rl.userCalls[userID], cutoff)
		if len(pruned) == 0 {
			delete(rl.userCalls, userID)
		} else {
			rl.userCalls[userID] = pruned
		}
		if rl.perUserLimit > 0 && len(pruned) >= rl.perUserLimit {
			return false
		}
		rl.userCalls[userID] = append(pruned, now)
	}

	rl.globalCalls = append(rl.globalCalls, now)
	return true
}

func pruneCalls(calls []time.Time, cutoff time.Time) []time.Time {
	keep := 0
	for keep < len(calls) && calls[keep].Before(cutoff) {
		keep++
	}
	if keep == 0 {
		return calls
	}
	return append([]time.Time(nil), calls[keep:]...)
}
