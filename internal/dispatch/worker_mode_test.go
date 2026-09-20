// Package dispatch_test: tests of how fan-out admits, routes and records a
// check.
package dispatch_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWorker_DispatchesByMode proves the worker publishes a check to the
// check subject with the payload saying so, and admits a simulate-locked
// device to it, while a real run goes to the dispatch subject and a
// simulate-locked device is still skipped. A job whose recorded mode is
// not a mode is failed rather than dispatched as a real run.
func TestWorker_DispatchesByMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      any
		state     pkginventory.LifecycleState
		topic     string
		published bool
		outcome   dispatch.Outcome
	}{
		{name: "a check", mode: "check", state: pkginventory.StateActive, topic: topology.CheckSubject("dev-1"), published: true, outcome: dispatch.OutcomeDispatched},
		{name: "a check of a simulate-locked device", mode: "check", state: pkginventory.StateSimulateLocked, topic: topology.CheckSubject("dev-1"), published: true, outcome: dispatch.OutcomeDispatched},
		{name: "a real run", mode: "execute", state: pkginventory.StateActive, topic: topology.DispatchSubject("dev-1"), published: true, outcome: dispatch.OutcomeDispatched},
		{name: "no mode is a real run", mode: nil, state: pkginventory.StateActive, topic: topology.DispatchSubject("dev-1"), published: true, outcome: dispatch.OutcomeDispatched},
		{name: "a real run of a simulate-locked device", mode: "execute", state: pkginventory.StateSimulateLocked, outcome: dispatch.OutcomeSkipped},
		{name: "a mode that is not a mode", mode: "chekc", state: pkginventory.StateActive, outcome: dispatch.OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := newTestJobStore(t)
			bus := newCapturingBus()
			device := capableDevice("dev-1", "router", "10.0.0.1")
			device.StubState = tc.state
			worker := dispatch.NewWorker(store, &fakeRepository{Devices: []pkginventory.InventoryItem{device}}, newTestRunbookSource(t), bus, nil)

			fields := launch.Fields{}
			if tc.mode != nil {
				fields[launch.ModeField] = tc.mode
			}
			evt := requestJobWithLaunchFields(t, ctx, store, "pb-1", "routers", fields, nil)
			if err := worker.HandleJobRequested(evt); err != nil {
				t.Fatalf("HandleJobRequested: %v", err)
			}
			_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].Outcome != tc.outcome {
				t.Fatalf("tasks = %+v, want one %s", tasks, tc.outcome)
			}
			if !tc.published {
				if bus.count() != 0 {
					t.Errorf("published %d time(s) for a device that must not be dispatched", bus.count())
				}
				if tc.outcome == dispatch.OutcomeFailed && !strings.Contains(tasks[0].Reason, "not a mode") {
					t.Errorf("reason = %q, want it to say the mode is not a mode", tasks[0].Reason)
				}
				return
			}
			if got := bus.lastTopic(); got != tc.topic {
				t.Errorf("published to %q, want %q", got, tc.topic)
			}
			var payload wire.DispatchPayload
			if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
				t.Fatal(err)
			}
			if want := "execute"; tc.mode == "check" {
				if payload.Mode != "check" {
					t.Errorf("payload mode = %q, want check", payload.Mode)
				}
			} else if payload.Mode != want {
				t.Errorf("payload mode = %q, want %q", payload.Mode, want)
			}
		})
	}
}

// TestWorker_CarriesTheExternalChecksDecision proves fan-out hands every
// device's payload the job's own record of whether its launcher may run
// it for real, the one value the Runner reads to decide whether a check
// may run an external program's check, round-tripped through the real
// store; and that a job recorded without it dispatches with it off.
func TestWorker_CarriesTheExternalChecksDecision(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		worker := dispatch.NewWorker(store, &fakeRepository{Devices: []pkginventory.InventoryItem{capableDevice("dev-1", "router", "10.0.0.1")}}, newTestRunbookSource(t), bus, nil)

		job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "auditor@example.com",
			Fields: launch.Fields{launch.ModeField: "check"}, ExternalChecks: allowed}
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
		if payload.ExternalChecks != allowed {
			t.Errorf("a job recorded with ExternalChecks %v dispatched with %v", allowed, payload.ExternalChecks)
		}
	}
}
