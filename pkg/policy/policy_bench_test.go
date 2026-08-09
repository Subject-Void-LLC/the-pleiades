package policy_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// BenchmarkResolve_Override measures the per-call cost of resolving a
// typical hierarchy depth (System/Inventory/Group/Device, PLAN.md Section
// 25's own four-level chain), proving the resolver's cost stays flat and
// cheap enough to call once per device in a Fan-Out hydration loop
// (FAILURE_PATTERNS.md's established "measure the second consumer's real
// cost" standard).
func BenchmarkResolve_Override(b *testing.B) {
	layers := []policy.Layer[string]{
		{Name: "system", Value: "queue"},
		{Name: "inventory", Value: "queue"},
		{Name: "group:databases", Value: "reject"},
		{Name: "device:web1", Value: "reject"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy.Resolve(policy.ModeOverride, "queue", layers, policy.Override[string])
	}
}

// BenchmarkResolve_Depth measures cost as a function of chain depth, for
// internal/classification's own deeper (unbounded) rule-tree paths.
func BenchmarkResolve_Depth(b *testing.B) {
	for _, depth := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			layers := make([]policy.Layer[int], depth)
			for i := range layers {
				layers[i] = policy.Layer[int]{Name: fmt.Sprintf("level-%d", i), Value: i}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				policy.Resolve(policy.ModeOverride, 0, layers, policy.Override[int])
			}
		})
	}
}
