package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkCronNextRun measures the cost of this phase's own bounded
// day-then-minute search, both in the common case (a match within the
// first candidate day) and in the worst case this package's own
// cronSearchBoundDays exists to cap: an unsatisfiable expression that
// exhausts the entire four-year search window before giving up. The
// worst case is the one that matters for a cost guarantee, the same
// "measure the pathological input, not just the happy path" standard
// SubnetSplit's own maxSubnetSplitCount justification sets in
// pkg/filters/network.go.
func BenchmarkCronNextRun(b *testing.B) {
	b.Run("matches_within_first_day", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.CronNextRun("0 9 * * *", "2024-01-01T08:00:00Z")
		}
	})
	b.Run("unsatisfiable_exhausts_search_bound", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.CronNextRun("0 0 31 2 *", "2024-01-01T00:00:00Z")
		}
	})
}

// BenchmarkISO8601ToEpoch measures the cost of parseISO8601, the one
// helper nearly every function in this file calls.
func BenchmarkISO8601ToEpoch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		filters.ISO8601ToEpoch("2024-01-01T00:00:00Z")
	}
}

// BenchmarkIsBusinessHour measures the cost of a business-hours check
// including its own map lookups and weekday-name matching against a
// realistic five-day schedule.
func BenchmarkIsBusinessHour(b *testing.B) {
	schedule := map[string]any{
		"start": "09:00",
		"end":   "17:00",
		"days":  []any{"Mon", "Tue", "Wed", "Thu", "Fri"},
	}
	for i := 0; i < b.N; i++ {
		filters.IsBusinessHour(schedule, "2024-01-01T10:00:00Z")
	}
}
