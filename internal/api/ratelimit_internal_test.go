// Tests for the rate limiter's eviction order when every caller shares one
// clock reading, as a flood does on a platform whose clock ticks coarsely.
package api

import (
	"strconv"
	"testing"
	"time"
)

// TestRateLimiter_EvictsTheOldestCallerWhenTheClockDoesNotMove: with a
// frozen clock every bucket has the same lastSeen, which is what a
// Windows clock tick gives a flood of callers. Eviction ordered by time then
// fell to the map's random iteration order, and the oldest caller could
// survive (seen on win11-arm, 2026-10-28). Ordered by use, the first caller
// is always the one evicted. Repeated, since a random order passes by luck.
func TestRateLimiter_EvictsTheOldestCallerWhenTheClockDoesNotMove(t *testing.T) {
	frozen := time.Unix(1_800_000_000, 0)
	for round := 0; round < 50; round++ {
		rl := NewRateLimiter(RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 1, MaxCallers: 16})
		rl.now = func() time.Time { return frozen }
		if !rl.Allow("victim") || rl.Allow("victim") {
			t.Fatal("the victim's bucket should admit one request and then refuse")
		}
		for i := 0; i < 16; i++ {
			rl.Allow("flood-" + strconv.Itoa(i))
		}
		if !rl.Allow("victim") {
			t.Fatalf("round %d: the victim, the least recently used caller, survived eviction at one clock reading", round)
		}
	}
}
