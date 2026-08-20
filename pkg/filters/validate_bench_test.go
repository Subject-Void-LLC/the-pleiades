package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// BenchmarkIsValidCronExpr measures the cost of this phase's own
// heaviest parser (five fields, each expanded into a map[int]bool), the
// same "measure the second consumer's real cost" standard
// cast_bench_test.go's own BenchmarkSafeInt already establishes for this
// package.
func BenchmarkIsValidCronExpr(b *testing.B) {
	b.Run("simple", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.IsValidCronExpr("30 4 * * *")
		}
	})
	b.Run("wide_step_range", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.IsValidCronExpr("*/1 * * * *")
		}
	})
	b.Run("malformed", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			filters.IsValidCronExpr("not a cron expression at all")
		}
	})
}

// BenchmarkIsValidJSON and BenchmarkIsValidYAML measure this phase's two
// document-shaped validators against a realistic device-fact-sized
// payload, proving the cost stays proportional to input size rather than
// exploding, the concern MaxStructuredInputBytes' own 1 MiB cap exists
// to bound.
func BenchmarkIsValidJSON(b *testing.B) {
	payload := `{"hostname":"switch-01","interfaces":[{"name":"Gi0/1","up":true},{"name":"Gi0/2","up":false}],"vlans":[10,20,30]}`
	for i := 0; i < b.N; i++ {
		filters.IsValidJSON(payload)
	}
}

func BenchmarkIsValidYAML(b *testing.B) {
	payload := "hostname: switch-01\ninterfaces:\n  - name: Gi0/1\n    up: true\n  - name: Gi0/2\n    up: false\nvlans: [10, 20, 30]\n"
	for i := 0; i < b.N; i++ {
		filters.IsValidYAML(payload)
	}
}

// BenchmarkFilterListByKV measures this phase's own list-scanning
// functions against a realistically sized device inventory slice (a few
// hundred entries, this project's own domain scale per
// coverage-floor.json's and this package's earlier benchmarks' shared
// justification), since these are the one family in this phase whose
// cost is O(n) in a caller-supplied list rather than in a bounded flat
// string.
func BenchmarkFilterListByKV(b *testing.B) {
	list := make([]map[string]any, 500)
	for i := range list {
		role := "web"
		if i%3 == 0 {
			role = "db"
		}
		list[i] = map[string]any{"name": role, "role": role}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filters.FilterListByKV(list, "role", "db")
	}
}
