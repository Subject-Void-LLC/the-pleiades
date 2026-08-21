package filters_test

import (
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkSafeInt measures the per-call cost of the common paths: a
// well-formed value, a malformed one falling back, and an over-cap input
// refused before parsing. This proves the cost stays flat and cheap
// enough to call once per condition per device in a Fan-Out run
// (FAILURE_PATTERNS.md's established "measure the second consumer's real
// cost" standard), the same reasoning pkg/policy's own benchmarks apply.
func BenchmarkSafeInt(b *testing.B) {
	b.Run("valid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeInt("42", -1)
		}
	})
	b.Run("malformed", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeInt("not-a-number", -1)
		}
	})
	b.Run("over_cap", func(b *testing.B) {
		oversized := ""
		for len(oversized) <= filters.MaxInputBytes {
			oversized += "1"
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			filters.SafeInt(oversized, -1)
		}
	})
}

// BenchmarkSafeFloat mirrors BenchmarkSafeInt for SafeFloat.
func BenchmarkSafeFloat(b *testing.B) {
	b.Run("valid", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeFloat("3.14159", -1)
		}
	})
	b.Run("malformed", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeFloat("not-a-number", -1)
		}
	})
}

// BenchmarkSafeBool mirrors BenchmarkSafeInt for SafeBool, including the
// yes/no/on/off vocabulary this function adds beyond strconv.ParseBool's
// own, since that switch is the one place this function's cost could
// plausibly grow with the size of its vocabulary.
func BenchmarkSafeBool(b *testing.B) {
	b.Run("recognized", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeBool("yes", false)
		}
	})
	b.Run("unrecognized", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.SafeBool("enabled", false)
		}
	})
}

// BenchmarkSafeInt_ForComparison runs the stdlib primitive SafeInt wraps,
// so a benchmark result on its own answers "how much does the safety net
// cost" rather than needing a human to remember strconv.Atoi's own number
// from a different run.
func BenchmarkSafeInt_ForComparison(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = strconv.Atoi("42")
	}
}
