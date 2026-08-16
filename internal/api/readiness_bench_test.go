package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Benchmarks for the /readyz bound.
//
// THE HARNESS, stated because a ratio without one is not a measurement.
// Both benchmarks serve the real handler through httptest.NewRecorder with
// b.RunParallel, so every iteration goes through the same code path a real
// request does minus the network. The dependency check is a stand-in for a
// database round trip: it sleeps for probeLatency and returns nil. That
// sleep is the point of the whole exercise, because the cost this endpoint
// imposes is not CPU in this process, it is a query on a database shared
// with everything else the controller does.
//
// The BASELINE is not a memory or an estimate. unboundedReadyzHandler below
// is the handler this package shipped before the gate existed, kept here
// and exercised by BenchmarkReadyzUnbounded so the "before" number is
// measured on the same machine, in the same run, as the "after" number.
// Anything else invites comparing a number from one laptop with a number
// from another and calling the difference a fix.
//
// Run both:
//
//	go test ./internal/api/ -run '^$' -bench 'BenchmarkReadyz' -benchtime 2s
//
// Each reports probes/op, which is the number that matters: real dependency
// checks divided by requests served. The unbounded handler's is 1.0 by
// construction. The gate's falls as concurrency rises, which is the
// property being claimed.

// probeLatency stands in for one database round trip.
//
// One millisecond is deliberately modest. A local PostgreSQL on a loopback
// socket answers a trivial SELECT in well under this; anything with a
// network hop, a busy server or a contended SQLite write lock is worse. A
// smaller number would flatter the fix by making the work being avoided
// look cheap.
const probeLatency = time.Millisecond

// unboundedReadyzHandler is the handler this package shipped before
// readinessGate, preserved verbatim in behavior so the baseline is a
// measurement rather than a recollection: every request runs every check.
func unboundedReadyzHandler(checks []ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessProbeTimeout)
		defer cancel()

		body := healthResponse{Status: "ready", Checks: make(map[string]string, len(checks))}
		status := http.StatusOK
		for _, check := range checks {
			if err := check.Probe(ctx); err != nil {
				body.Checks[check.Name] = "failed"
				body.Status = "not ready"
				status = http.StatusServiceUnavailable
				continue
			}
			body.Checks[check.Name] = "ok"
		}
		writeJSON(w, status, body)
	}
}

// benchCheck is a dependency that costs probeLatency and counts itself.
func benchCheck(probes *atomic.Int64) ReadinessCheck {
	return ReadinessCheck{
		Name: "database",
		Probe: func(ctx context.Context) error {
			probes.Add(1)
			select {
			case <-time.After(probeLatency):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func serve(b *testing.B, h http.HandlerFunc) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h(httptest.NewRecorder(), req)
		}
	})
}

// BenchmarkReadyzUnbounded measures what the endpoint cost before the gate:
// one real dependency check per request, without limit.
func BenchmarkReadyzUnbounded(b *testing.B) {
	var probes atomic.Int64
	h := unboundedReadyzHandler([]ReadinessCheck{benchCheck(&probes)})

	b.ResetTimer()
	serve(b, h)
	b.StopTimer()

	b.ReportMetric(float64(probes.Load())/float64(b.N), "probes/op")
	b.ReportMetric(float64(probes.Load()), "probes")
}

// BenchmarkReadyzBounded measures the same load through readinessGate at
// the shipped default interval.
func BenchmarkReadyzBounded(b *testing.B) {
	var probes atomic.Int64
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{benchCheck(&probes)},
		readinessProbeTimeout, defaultReadinessMinInterval)
	h := readyzHandler(gate)

	b.ResetTimer()
	serve(b, h)
	b.StopTimer()

	b.ReportMetric(float64(probes.Load())/float64(b.N), "probes/op")
	b.ReportMetric(float64(probes.Load()), "probes")
}
