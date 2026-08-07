package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/dispatch"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/google/uuid"
)

// FuzzDispatchRunbook drives arbitrary group and runbook query values
// through the real DispatchRunbook handler.
//
// This target deliberately does not wrap the request in a recover(). The
// previous version of this test built its request URL by raw string
// concatenation ("/dispatch?group="+group+"&runbook="+runbook) and needed
// a blanket recover() to survive httptest.NewRequest panicking on a
// resulting malformed URI, which is exactly the shape
// internal/api/hateoas_fuzz_test.go's own comment documents as the house
// fix: build the URL through net/url instead, which removes the need for
// recover() entirely, because url.Values.Encode() always produces a
// syntactically valid query string no matter what bytes the fuzzer hands
// it.
func FuzzDispatchRunbook(f *testing.F) {
	runbooks := newTestRunbookSource(f, "pb-1")
	jobs := newTestJobStore(f)
	bus := event.NewInProcessBus()
	dispatcher := api.NewDispatcher(runbooks, jobs, bus)

	f.Add("group1", "runbook1")
	f.Add("", "")
	f.Add("malformed!@#$", "pb-2")
	f.Add("routers", "pb-1")
	f.Add("../../etc/passwd", "pb-1")

	f.Fuzz(func(t *testing.T, group, runbook string) {
		values := url.Values{}
		values.Set("group", group)
		values.Set("runbook", runbook)
		req := httptest.NewRequest(http.MethodPost, "/dispatch?"+values.Encode(), nil)
		req = req.WithContext(contextWithIdentity(req, dispatchTestIdentity))

		rr := httptest.NewRecorder()
		dispatcher.DispatchRunbook(rr, req)

		// A missing parameter is always 400, regardless of what the other
		// one contains: this is the one invariant this handler promises
		// before it ever looks at runbooks or jobs.
		if group == "" || runbook == "" {
			if rr.Code != http.StatusBadRequest {
				t.Errorf("group=%q runbook=%q: status = %d, want %d", group, runbook, rr.Code, http.StatusBadRequest)
			}
			return
		}

		// Otherwise the only honest invariant is "the response is a
		// well-formed JSON body carrying one of the status codes this
		// handler is documented to return." The exact code depends on
		// whether the fuzzed runbook string happens to name "pb-1", which
		// is not something this target controls or needs to predict.
		switch rr.Code {
		case http.StatusAccepted, http.StatusNotFound, http.StatusBadRequest, http.StatusInternalServerError:
		default:
			t.Errorf("group=%q runbook=%q: unexpected status %d", group, runbook, rr.Code)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("group=%q runbook=%q: response is not valid JSON: %v: %q", group, runbook, err, rr.Body.String())
		}
	})
}

// fuzzSingleDeviceRepository is an inventory.Repository double that yields
// exactly one device (Device) from GetGroup, ignoring the selector
// entirely: FuzzWorkerDeviceProperties's subject is one device's own
// admission properties (its lifecycle state and the presence of a "host"
// property), not group filtering, which dispatcher_selector_test.go
// already covers end to end against a real entRepository. Every other
// Repository method fails loudly rather than returning a zero value,
// mirroring internal/dispatch/worker_test.go's own fakeRepository: this
// fuzz target has no legitimate reason to reach them, and a silent zero
// value would hide a test that started depending on one instead.
type fuzzSingleDeviceRepository struct {
	device pkginventory.InventoryItem
}

func (r fuzzSingleDeviceRepository) GetGroup(_ context.Context, _ pkginventory.Selector) (inventory.Iterator, error) {
	return &fuzzSingleDeviceIterator{device: r.device}, nil
}

func (r fuzzSingleDeviceRepository) GetByName(_ context.Context, _ string) (pkginventory.InventoryItem, error) {
	return nil, errors.New("fuzzSingleDeviceRepository.GetByName is not implemented for this fuzz target")
}

func (r fuzzSingleDeviceRepository) Create(_ context.Context, _ pkginventory.InventoryItem) error {
	return errors.New("fuzzSingleDeviceRepository.Create is not implemented for this fuzz target")
}

func (r fuzzSingleDeviceRepository) Save(_ context.Context, _ pkginventory.InventoryItem) error {
	return errors.New("fuzzSingleDeviceRepository.Save is not implemented for this fuzz target")
}

func (r fuzzSingleDeviceRepository) Retire(_ context.Context, _ string) error {
	return errors.New("fuzzSingleDeviceRepository.Retire is not implemented for this fuzz target")
}

// fuzzSingleDeviceIterator yields device exactly once, the same
// Next/Item/Error/Close shape every other Iterator double in this module
// implements.
type fuzzSingleDeviceIterator struct {
	device  pkginventory.InventoryItem
	yielded bool
}

func (i *fuzzSingleDeviceIterator) Next(_ context.Context) bool {
	if i.yielded {
		return false
	}
	i.yielded = true
	return true
}

func (i *fuzzSingleDeviceIterator) Item() pkginventory.InventoryItem { return i.device }
func (i *fuzzSingleDeviceIterator) Error() error                     { return nil }
func (i *fuzzSingleDeviceIterator) Close() error                     { return nil }

// FuzzWorkerDeviceProperties varies exactly the two device-level admission
// facts internal/dispatch.Worker's own fan-out loop branches on: whether
// the device's lifecycle state is Active, and whether its "host" property
// is present, absent, or present-but-empty. It routes each fuzzed
// combination through the real DirSource-plus-Worker path (a real
// runbook.Source, a real dispatch.JobStore over SQLite, and the real
// Worker.HandleJobRequested), so the missing-host-property skip branch is
// actually reachable by the fuzzer.
//
// Before this phase, that branch was unreachable to any fuzzer targeting
// this package: the old mock device iterator this package's own dispatch
// tests used (MockIterator, since deleted) always handed every device a
// hardcoded "ip" property regardless of fuzz input, so no fuzzed input
// could ever exercise "device has no host property." Building the fuzzed
// device through pkg/inventory/inventorytest.Stub, with Props populated or
// not based on hasHost, is what makes that branch reachable now.
func FuzzWorkerDeviceProperties(f *testing.F) {
	runbooks := newTestRunbookSource(f, "pb-1")

	f.Add(true, true, "10.0.0.1")
	f.Add(true, false, "")
	f.Add(false, true, "10.0.0.1")
	f.Add(false, false, "")
	f.Add(true, true, "") // present but empty, distinct from absent

	f.Fuzz(func(t *testing.T, active, hasHost bool, host string) {
		state := pkginventory.StateActive
		if !active {
			state = pkginventory.StateQuarantined
		}
		props := map[string]pkginventory.PropertyValue{}
		if hasHost {
			props["host"] = host
		}
		device := &inventorytest.Stub{
			StubID:    "fuzz-dev",
			StubName:  "fuzz-device",
			StubState: state,
			Props:     props,
		}

		store := newTestJobStore(t)
		bus := event.NewInProcessBus()
		worker := dispatch.NewWorker(store, fuzzSingleDeviceRepository{device: device}, runbooks, bus)

		ctx := context.Background()
		job := &dispatch.Job{RunbookID: "pb-1", GroupName: "fuzz-group", Actor: "fuzz"}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("Create: %v", err)
		}
		data, err := json.Marshal(map[string]string{"job_id": job.JobID})
		if err != nil {
			t.Fatalf("marshal job.requested payload: %v", err)
		}
		evt := event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}

		if err := worker.HandleJobRequested(evt); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}

		got, tasks, err := store.Get(ctx, job.JobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != "completed" {
			t.Fatalf("job State = %q, want %q", got.State, "completed")
		}
		if len(tasks) != 1 {
			t.Fatalf("expected exactly 1 task, got %d: %+v", len(tasks), tasks)
		}

		switch {
		case !active:
			if tasks[0].Outcome != dispatch.OutcomeSkipped {
				t.Errorf("inactive device: outcome = %v, want %v", tasks[0].Outcome, dispatch.OutcomeSkipped)
			}
		case !hasHost:
			if tasks[0].Outcome != dispatch.OutcomeSkipped {
				t.Errorf("device with no host property: outcome = %v, want %v", tasks[0].Outcome, dispatch.OutcomeSkipped)
			}
		default:
			// Active, and a "host" key is present, even if its value is
			// the empty string: Properties().String("host") reports
			// (value, true) for an empty stored string, distinct from
			// (\"\", false) for an absent key, so this case dispatches.
			if tasks[0].Outcome != dispatch.OutcomeDispatched {
				t.Errorf("active device with a present host property (value %q): outcome = %v, want %v", host, tasks[0].Outcome, dispatch.OutcomeDispatched)
			}
		}
	})
}
