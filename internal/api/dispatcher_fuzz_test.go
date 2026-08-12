package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// FuzzLaunchTemplate drives an arbitrary template id and an arbitrary
// request body through the real launch handler.
//
// It replaces a target that fuzzed a group name and a runbook id in a query
// string, which is the launch surface this phase removed. The id is the
// interesting half: it arrives as a path parameter, is parsed into a
// primary-key lookup, and ends up concatenated into a Location header, so
// what this proves is that no byte sequence reaches a panic or escapes the
// handler's documented set of answers.
//
// It deliberately does not wrap the call in a recover(). The id travels
// through chi's route context rather than being concatenated into a URL,
// so httptest.NewRequest is never handed a value that could make it panic
// on a malformed URI, which is the house fix internal/api's own
// hateoas_fuzz_test.go documents.
func FuzzLaunchTemplate(f *testing.F) {
	jobs := newTestJobStore(f)
	configs := &recordingConfigs{}
	dispatcher := api.NewDispatcher(newTestRunbookSource(f, "pb-1"), jobs, event.NewInProcessBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}), api.WithLaunchConfigs(configs))

	handler := http.HandlerFunc(dispatcher.LaunchFromTemplate)

	f.Add("12", `{"overrides":{"limit":"edge-01"}}`)
	f.Add("12", "")
	f.Add("0", `{}`)
	f.Add("../../etc/passwd", `{}`)
	f.Add("12", `{"overrides":{"forks":"not a number"}}`)
	f.Add("12", `{"answers":{"version":"17.3"}}`)
	f.Add("99999999999999999999", `{}`)

	f.Fuzz(func(t *testing.T, id, body string) {
		// The id travels as a path parameter rather than being
		// concatenated into a URL, which is how the router delivers it and
		// what keeps this target about the handler rather than about
		// net/http's parsing.
		req := httptest.NewRequest(http.MethodPost, "/templates/x/launch", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// The honest invariant is that the response is a well-formed JSON
		// body carrying one of the status codes this handler documents.
		// Which one depends on whether the fuzzed id happens to be 12 and
		// whether the body happens to parse, neither of which this target
		// controls or needs to predict. What it does prove is that no
		// input reaches a panic, and that nothing escapes as a 200 or a
		// 500 with an empty body.
		switch rr.Code {
		case http.StatusAccepted, http.StatusBadRequest, http.StatusNotFound,
			http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity, http.StatusInternalServerError:
		default:
			t.Errorf("id=%q body=%q: unexpected status %d", id, body, rr.Code)
		}

		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("id=%q body=%q: response is not valid JSON: %v: %q", id, body, err, rr.Body.String())
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
		worker := dispatch.NewWorker(store, fuzzSingleDeviceRepository{device: device}, runbooks, bus, nil)

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
