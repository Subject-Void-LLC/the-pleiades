// Package dispatch_test: tests of the connection persistence decision
// fan-out puts on each device's payload.
package dispatch_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWorker_PersistConnectionsIsOffWhenEitherLadderSaysSo proves the
// payload carries persistence only when the job's own setting and the
// device's hierarchy both allow it: off at either is off, the device's
// own value beats its groups', and a hierarchy that cannot be read is
// off.
func TestWorker_PersistConnectionsIsOffWhenEitherLadderSaysSo(t *testing.T) {
	off := []inventory.HierarchyLayer{{Name: "lab", Properties: map[string]interface{}{engine.PersistConnectionsProperty: false}}}
	for _, tc := range []struct {
		name        string
		job         any // nil: the job does not set it
		device      any // nil: the device does not set it
		ancestry    []inventory.HierarchyLayer
		ancestryErr error
		want        bool
	}{
		{name: "on by default", want: true},
		{name: "the job on", job: launch.PersistOn, want: true},
		{name: "the job off", job: launch.PersistOff, want: false},
		{name: "a group off", ancestry: off, want: false},
		{name: "a group off beats the job on", job: launch.PersistOn, ancestry: off, want: false},
		{name: "the device off", device: false, want: false},
		{name: "the device on beneath a group off", device: true, ancestry: off, want: true},
		{name: "the job off beats the device on", job: launch.PersistOff, device: true, want: false},
		{name: "an unreadable hierarchy", ancestryErr: errors.New("database down"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := newTestJobStore(t)
			bus := newCapturingBus()
			device := capableDevice("dev-1", "router", "10.0.0.1")
			if tc.device != nil {
				device.Props[engine.PersistConnectionsProperty] = tc.device
			}
			repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}, Ancestry: tc.ancestry, AncestryErr: tc.ancestryErr}
			worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

			fields := launch.Fields{}
			if tc.job != nil {
				fields[launch.PersistConnectionsField] = tc.job
			}
			job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "operator@example.com", Fields: fields}
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
			var payload wire.DispatchPayload
			if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.PersistConnections != tc.want {
				t.Errorf("PersistConnections = %v, want %v", payload.PersistConnections, tc.want)
			}
		})
	}
}
