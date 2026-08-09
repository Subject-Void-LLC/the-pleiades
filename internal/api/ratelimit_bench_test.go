package api_test

import (
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

// BenchmarkRateLimiter_Allow measures the hot path: one caller already in
// the table, which is what every request after the first from a given
// client looks like.
func BenchmarkRateLimiter_Allow(b *testing.B) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 1e9, Burst: 1e6})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow("sub:ci-pipeline")
	}
}

// BenchmarkRateLimiter_AllowDistinctCallers measures the eviction path: a
// table permanently at its cap, so every request both inserts and evicts.
// This is the shape a source-address flood produces, and the number worth
// knowing is how expensive the defense is under the attack it defends
// against.
func BenchmarkRateLimiter_AllowDistinctCallers(b *testing.B) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 1e9, Burst: 1e6, MaxCallers: 1024})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow("addr:" + strconv.Itoa(i))
	}
}
