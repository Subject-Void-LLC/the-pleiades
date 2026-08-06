package catalystcenter_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/catalystcenter"
)

// BenchmarkEachDevice measures a full paged walk over a large fleet.
//
// The point is not raw speed, which is dominated by the loopback HTTP
// round trip here and by the real controller in production. It is that cost
// grows with fleet size and not worse, and that per-operation allocations
// stay bounded: EachDevice holds one page at a time by design, and a change
// that quietly started accumulating every device would show up here as
// allocations scaling with the total rather than with the page.
func BenchmarkEachDevice(b *testing.B) {
	sizes := []struct {
		name     string
		total    int
		pageSize int
	}{
		{name: "100_devices_page_50", total: 100, pageSize: 50},
		{name: "1000_devices_page_500", total: 1000, pageSize: 500},
		{name: "1000_devices_page_50", total: 1000, pageSize: 50},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			s := newSandbox(b, size.total, 0)
			c, err := catalystcenter.New(s.server.URL, "user", "pass")
			if err != nil {
				b.Fatalf("New: %v", err)
			}
			defer func() { _ = c.Close() }()

			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				var seen int
				if err := c.EachDevice(ctx, size.pageSize, func(catalystcenter.Device) error {
					seen++
					return nil
				}); err != nil {
					b.Fatalf("EachDevice: %v", err)
				}
				if seen != size.total {
					b.Fatalf("saw %d devices, want %d", seen, size.total)
				}
			}
		})
	}
}

// BenchmarkEachDeviceMemoryFlatline is the release-gate memory proof: a walk
// over a large fleet must not retain the fleet.
//
// It measures heap growth across one full walk rather than timing it. The
// budget is deliberately generous relative to one page, because the
// assertion that matters is the shape (bounded by page size, not by total
// device count), not a precise byte count that would make this test fail on
// an unrelated allocator change. This mirrors what
// internal/inventory/iterator_pprof_test.go already proves for the
// database-backed iterator.
func BenchmarkEachDeviceMemoryFlatline(b *testing.B) {
	const (
		total    = 5000
		pageSize = 100
		budget   = 8 << 20 // 8 MiB
	)

	s := newSandbox(b, total, 0)
	c, err := catalystcenter.New(s.server.URL, "user", "pass")
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		var seen int
		if err := c.EachDevice(ctx, pageSize, func(catalystcenter.Device) error {
			seen++
			return nil
		}); err != nil {
			b.Fatalf("EachDevice: %v", err)
		}

		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		if seen != total {
			b.Fatalf("saw %d devices, want %d", seen, total)
		}
		if growth := int64(after.HeapAlloc) - int64(before.HeapAlloc); growth > budget {
			b.Fatalf("heap grew %d bytes across a %d device walk, budget %d: the walk is retaining devices rather than streaming them",
				growth, total, budget)
		}
	}
}
