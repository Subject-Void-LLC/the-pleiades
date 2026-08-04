package registry

import (
	"fmt"
	"testing"
)

// BenchmarkRegistry_Get measures the read-hot-path cost this primitive adds
// per lookup: every ItemFactory.Build call (and every future sync-plugin,
// transport, or launchable-kind lookup this registry backs) pays this cost
// once per call, potentially once per device in a Fan-Out hydration loop.
func BenchmarkRegistry_Get(b *testing.B) {
	r := New[int]()
	for i := 0; i < 100; i++ {
		r.MustRegister(fmt.Sprintf("key-%d", i), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Get("key-50")
	}
}

// BenchmarkRegistry_All measures the snapshot-copy cost NewItemFactory pays
// once, at construction, drawing every registered device type.
func BenchmarkRegistry_All(b *testing.B) {
	r := New[int]()
	for i := 0; i < 100; i++ {
		r.MustRegister(fmt.Sprintf("key-%d", i), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.All()
	}
}
