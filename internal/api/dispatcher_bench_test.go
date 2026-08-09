package api_test

import (
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// BenchmarkDispatchRunbook measures the cost of a launch itself: one Job
// row persisted through a real dispatch.JobStore, plus one job.requested
// event published through a real event.Bus.
//
// Before Phase 14, this benchmark measured DispatchRunbook's own inline
// per-device fan-out loop over a fixed device count, because that loop ran
// synchronously inside the HTTP request. That loop no longer exists here:
// internal/dispatch.Worker now owns fan-out entirely, off the request path,
// so DispatchRunbook's own cost is now constant with respect to how many
// devices a dispatch will eventually reach. Benchmarking per-device
// fan-out throughput is internal/dispatch's own concern now, not this
// package's.
func BenchmarkDispatchRunbook(b *testing.B) {
	runbooks := newTestRunbookSource(b, "pb-1")
	jobs := newTestJobStore(b)
	bus := event.NewInProcessBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	req := dispatchTestRequest(b, "routers", "pb-1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		dispatcher.DispatchRunbook(rr, req)
	}
}
