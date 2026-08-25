// This file benchmarks the subject builders, which sit on the dispatch
// path rather than beside it.
//
// The cost is worth measuring rather than assuming because of where it
// runs: DispatchSubject is called once per device per job, inside the
// fan-out loop that internal/api's own release gate drives to 10,000
// devices, and it now does a SHA-256 where it previously did nothing at
// all. The question this answers is whether that per-device hash is
// visible next to the JetStream publish it precedes.
package topology_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// BenchmarkDispatchSubject measures one device's subject construction: the
// sanitizing pass, the hash, and the concatenation.
func BenchmarkDispatchSubject(b *testing.B) {
	// A UUID device id, which is the common case, and a dotted hostname,
	// which is the case that actually exercises the regexp's replacement
	// path rather than just its scan.
	for _, id := range []string{
		"4d6e9c14-0785-49d2-b821-51a81b54b1cc",
		"router1.example.com",
	} {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = topology.DispatchSubject(id)
			}
		})
	}
}

// BenchmarkDispatchSubjectFanOut measures the whole per-job cost at the
// device count internal/api's release gate already uses, which is the
// number that decides whether this belongs on the dispatch path at all.
func BenchmarkDispatchSubjectFanOut(b *testing.B) {
	ids := make([]string, 10000)
	for i := range ids {
		ids[i] = fmt.Sprintf("release-gate-device-%05d", i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			_ = topology.DispatchSubject(id)
		}
	}
}
