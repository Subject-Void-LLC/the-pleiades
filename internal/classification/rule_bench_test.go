package classification_test

import (
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
)

// BenchmarkClassify measures the per-host cost of classification against
// the real DefaultRuleSet, proving it is cheap enough to call once per
// device in a future Fan-Out hydration loop (the same standard
// pkg/policy's own BenchmarkResolve_Depth applies to the resolver
// underneath it).
func BenchmarkClassify(b *testing.B) {
	rs := classification.DefaultRuleSet()
	path := []string{"linux_server", "debian_family", "ubuntu"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rs.Classify(path); err != nil {
			b.Fatalf("Classify: unexpected error: %v", err)
		}
	}
}

// BenchmarkClassify_Depth measures cost as a function of path length, up to
// the maximum maxPathSegments permits. An adversarial review of this phase
// found Classify's original key-building (rejoining the whole prefix from
// scratch on every iteration via strings.Join) was O(n^2) in path length,
// measured at ~40s for a 100,000-segment path; the fix builds the key
// incrementally with a strings.Builder instead (O(n) total), and
// maxPathSegments independently caps how large n can ever be. ns/op here
// should scale roughly linearly with depth, not quadratically, across
// this benchmark's own range.
func BenchmarkClassify_Depth(b *testing.B) {
	rules := map[string]classification.Rule{}
	path := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		path = append(path, fmt.Sprintf("level%d", i))
		key := ""
		for j, s := range path {
			if j > 0 {
				key += "."
			}
			key += s
		}
		rules[key] = classification.Rule{}
	}
	rs, err := classification.NewRuleSet(rules)
	if err != nil {
		b.Fatalf("NewRuleSet: %v", err)
	}

	for _, depth := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			p := path[:depth]
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := rs.Classify(p); err != nil {
					b.Fatalf("Classify: unexpected error: %v", err)
				}
			}
		})
	}
}
