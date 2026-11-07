// Package dispatch_test: every dispatch names its payload version.
package dispatch_test

import (
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWorker_EveryDispatchNamesItsPayloadVersion proves fan-out writes this
// build's payload version on the wire, so a Runner of an older release can
// refuse a dispatch it would misread (wire.DispatchPayload.CheckVersion).
// The raw message is read, not a decoded struct, because the contract is
// the key on the wire.
func TestWorker_EveryDispatchNamesItsPayloadVersion(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	worker := dispatch.NewWorker(store, &fakeRepository{Devices: []pkginventory.InventoryItem{capableDevice("dev-1", "router", "10.0.0.1")}}, newTestRunbookSource(t), bus, nil)
	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "auditor@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.HandleJobRequested(event.Event{ID: job.JobID, Type: "job.requested", Data: data}); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(bus.last().Data, &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw["payload_version"]; got != float64(wire.DispatchPayloadVersion) {
		t.Errorf("payload_version on the wire = %v, want %d", got, wire.DispatchPayloadVersion)
	}
}
