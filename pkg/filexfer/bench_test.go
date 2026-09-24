// Benchmarks for the path resolver and the size check every transfer pays.
package filexfer_test

import (
	"io"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// BenchmarkResolve measures the guard every transfer pays before any
// network call. It should cost well under a microsecond, which is noise
// beside even a loopback SSH round trip.
func BenchmarkResolve(b *testing.B) {
	for b.Loop() {
		if _, err := filexfer.Resolve("/srv/xfer", "images/2026/09/firmware-17.9.4.bin"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExactReader measures the per-byte cost the size check adds
// to every Put's stream.
func BenchmarkExactReader(b *testing.B) {
	const size = 1 << 20
	payload := strings.Repeat("x", size)
	b.SetBytes(size)
	for b.Loop() {
		if _, err := io.Copy(io.Discard, filexfer.ExactReader(strings.NewReader(payload), size)); err != nil {
			b.Fatal(err)
		}
	}
}
