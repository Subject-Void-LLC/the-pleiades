package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	// Aliased entjob: this file's existing tests already name local
	// variables "job" (e.g. requestJob's own *dispatch.Job), so importing
	// internal/ent/job under its default name would shadow those instead
	// of the other way around.
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// stateNote explains, in the one place these tests all point at, why a
// successful fan-out no longer ends in "completed".
//
// Several assertions in this package used to read State == "completed" as
// "the fan-out worked". That reading was only ever true because nothing
// tracked what happened after a dispatch: "completed" meant the Controller
// had stopped working, not that the run had ended. It now means every
// dispatched device has reported back, so a fan-out that handed work to a
// Runner ends in "running" instead.
const stateNote = "a fan-out that dispatched to a device ends in running, not completed: the Controller has finished, the device has not"

// fakeRepository is a small local Repository test double, mirroring
// internal/api/dispatcher_test.go's own MockRepository/MockIterator shape
// for consistency, but defined fresh here: that one lives in package
// api_test, which internal/dispatch cannot import (an unrelated test
// package, and internal/dispatch must not depend on internal/api anyway).
// It yields exactly the devices in Devices, in order, from GetGroup
// regardless of the Selector passed in: these tests only ever dispatch
// against one group per case, so a selector-aware fake would be
// unexercised complexity.
type fakeRepository struct {
	Devices []pkginventory.InventoryItem
	// GetGroupErr, when non-nil, is returned by GetGroup instead of a
	// working iterator, for the inventory-query-failure path.
	GetGroupErr error
	// Ancestry and AncestryErr are what GroupAncestry answers for every
	// device.
	Ancestry    []inventory.HierarchyLayer
	AncestryErr error
}

func (r *fakeRepository) GetGroup(_ context.Context, _ pkginventory.Selector) (inventory.Iterator, error) {
	if r.GetGroupErr != nil {
		return nil, r.GetGroupErr
	}
	return &fakeIterator{devices: r.Devices}, nil
}

// GetByName answers from Devices, as a real repository would: a windowed
// job's pump reads each device again by name when its turn comes.
func (r *fakeRepository) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	for _, d := range r.Devices {
		if d.Name() == name {
			return d, nil
		}
	}
	return nil, fmt.Errorf("no device %q: %w", name, inventory.ErrItemNotFound)
}

func (r *fakeRepository) Create(_ context.Context, _ pkginventory.InventoryItem) error {
	return errors.New("fakeRepository.Create is not implemented for these tests")
}

func (r *fakeRepository) Save(_ context.Context, _ pkginventory.InventoryItem) error {
	return errors.New("fakeRepository.Save is not implemented for these tests")
}

func (r *fakeRepository) Retire(_ context.Context, _ string) error {
	return errors.New("fakeRepository.Retire is not implemented for these tests")
}

func (r *fakeRepository) GroupAncestry(_ context.Context, _ string) ([]inventory.HierarchyLayer, error) {
	return r.Ancestry, r.AncestryErr
}

// fakeIterator streams Devices in order, the same Next/Item/Error/Close
// shape internal/inventory.Iterator requires and
// internal/api/dispatcher_test.go's MockIterator already demonstrates.
type fakeIterator struct {
	devices []pkginventory.InventoryItem
	index   int
}

func (i *fakeIterator) Next(_ context.Context) bool {
	if i.index < len(i.devices) {
		i.index++
		return true
	}
	return false
}

func (i *fakeIterator) Item() pkginventory.InventoryItem { return i.devices[i.index-1] }
func (i *fakeIterator) Error() error                     { return nil }
func (i *fakeIterator) Close() error                     { return nil }

// newTestRunbookSource builds a real runbook.Source (RULE 0: no hand-
// rolled fake Source, DirSource is cheap to point at a temp directory)
// over a fixture directory containing two runbooks, both requiring
// capability.NameCiscoIOS via the real "ios_backup" fqcn
// (engine.ActionCapability's own binding): "pb-1", with no metadata
// section (Interruptible defaults to true), and "pb-no-abort", declaring
// metadata.interruptible: false, so tests can dispatch against either to
// prove that value actually reaches the published wire.DispatchPayload
// (worker_devices.go).
func newTestRunbookSource(t testing.TB) runbook.Source {
	t.Helper()

	dir := t.TempDir()
	content := "id: pb-1\ntasks:\n  - name: step\n    fqcn: ios_backup\n"
	path := filepath.Join(dir, "pb-1.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture: %v", err)
	}

	noAbortContent := "id: pb-no-abort\nmetadata:\n  interruptible: false\ntasks:\n  - name: step\n    fqcn: ios_backup\n"
	noAbortPath := filepath.Join(dir, "pb-no-abort.yaml")
	if err := os.WriteFile(noAbortPath, []byte(noAbortContent), 0o644); err != nil {
		t.Fatalf("failed to write runbook fixture: %v", err)
	}

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}
	return src
}

// newTestJobStore spins up a real in-memory SQLite-backed JobStore, per
// RULE 0.
func newTestJobStore(t testing.TB) dispatch.JobStore {
	t.Helper()
	store, _ := newTestStore(t)
	return store
}

// capableDevice builds an inventorytest.Stub in StateActive, declaring
// capability.NameCiscoIOS (so it clears CapabilityAdmits for the "pb-1"
// fixture runbook above), with the given host property.
func capableDevice(id, name, host string) *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    pkginventory.DeviceID(id),
		StubName:  name,
		StubState: pkginventory.StateActive,
		Caps:      []capability.Name{capability.NameCiscoIOS},
		Props:     map[string]pkginventory.PropertyValue{"host": host},
	}
}

// sshStub embeds *inventorytest.Stub and adds the SSHHost/SSHPort
// accessors capability.SSHTransportCapable requires, which the shared
// Stub type deliberately does not implement itself (its own doc comment:
// a minimal, overridable double, not a stand-in for every capability
// interface a real device type might satisfy).
type sshStub struct {
	*inventorytest.Stub
	Host string
	Port int
}

func (s *sshStub) SSHHost() string { return s.Host }
func (s *sshStub) SSHPort() int    { return s.Port }

// sshCapableDevice builds a device declaring both capability.NameCiscoIOS
// (so it clears CapabilityAdmits for the "pb-1" fixture runbook, exactly
// like capableDevice) and capability.NameSSHTransport, and structurally
// implementing capability.SSHTransportCapable, so tests can assert
// admitAndDispatchDevice populates wire.DispatchPayload.SSHPort and
// Capabilities correctly.
func sshCapableDevice(id, name, host string, port int) *sshStub {
	return &sshStub{
		Stub: &inventorytest.Stub{
			StubID:    pkginventory.DeviceID(id),
			StubName:  name,
			StubState: pkginventory.StateActive,
			Caps:      []capability.Name{capability.NameCiscoIOS, capability.NameSSHTransport},
			Props:     map[string]pkginventory.PropertyValue{"host": host},
		},
		Host: host,
		Port: port,
	}
}

// capturingBus wraps a real event.NewInProcessBus (a genuine adapter, per
// RULE 0, not a mock) and additionally records every published event, so
// tests can assert on payload contents and total publish counts, which
// the in-process bus's own Subscribe fan-out alone does not expose.
type capturingBus struct {
	event.Bus
	mu        sync.Mutex
	published []event.Event
	topics    []string
}

func newCapturingBus() *capturingBus {
	return &capturingBus{Bus: event.NewInProcessBus()}
}

func (b *capturingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	b.mu.Lock()
	b.published = append(b.published, evt)
	b.topics = append(b.topics, topic)
	b.mu.Unlock()
	return b.Bus.Publish(ctx, topic, evt)
}

func (b *capturingBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func (b *capturingBus) last() event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published[len(b.published)-1]
}

func (b *capturingBus) lastTopic() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.topics[len(b.topics)-1]
}

// requestJob creates a pending job via store and returns the
// job.requested Event a launcher would publish for it (this package's own
// jobRequestedPayload is unexported, so the test builds the identical
// wire shape, {"job_id": "..."}, by hand rather than reaching into it).
func requestJob(t *testing.T, ctx context.Context, store dispatch.JobStore, runbookID, groupName string) event.Event {
	t.Helper()

	job := &dispatch.Job{RunbookID: runbookID, GroupName: groupName, Actor: "user@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := json.Marshal(map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatalf("failed to marshal job.requested payload: %v", err)
	}
	return event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}
}

// requestJobWithLaunchFields is requestJob, plus a job.Fields/ExtraVars the
// caller supplies, for tests proving admitAndDispatchDevice carries a job's
// resolved launch fields onto the wire (AWX_PARITY_ROADMAP.md Section
// 3b.1's second hop). A separate helper rather than widening requestJob's
// own signature: requestJob has more than a dozen call sites that have no
// reason to know about launch fields at all.
func requestJobWithLaunchFields(t testing.TB, ctx context.Context, store dispatch.JobStore, runbookID, groupName string, fields launch.Fields, extraVars map[string]any) event.Event {
	t.Helper()

	job := &dispatch.Job{RunbookID: runbookID, GroupName: groupName, Actor: "user@example.com", Fields: fields, ExtraVars: extraVars}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := json.Marshal(map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatalf("failed to marshal job.requested payload: %v", err)
	}
	return event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}
}

// TestWorker_HandleJobRequested_SkipsMissingHost proves a device with no
// "host" property is skipped, with a reason naming it, never dispatched
// with an empty address.
func TestWorker_HandleJobRequested_SkipsMissingHost(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		&inventorytest.Stub{
			StubID:    "dev-1",
			StubName:  "no-host-router",
			StubState: pkginventory.StateActive,
			Caps:      []capability.Name{capability.NameCiscoIOS},
			Props:     map[string]pkginventory.PropertyValue{},
		},
	}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.State != "completed" {
		t.Fatalf("job State = %q, want %q", job.State, "completed")
	}
	if job.SkippedCount != 1 || job.DispatchedCount != 0 || job.FailedCount != 0 {
		t.Fatalf("tallies = dispatched=%d skipped=%d failed=%d, want 0/1/0",
			job.DispatchedCount, job.SkippedCount, job.FailedCount)
	}
	if len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeSkipped {
		t.Fatalf("tasks = %+v, want one OutcomeSkipped task", tasks)
	}
	if tasks[0].Reason == "" {
		t.Fatal("skip reason is empty, want a reason naming the device")
	}
	if want := "no-host-router"; !strings.Contains(tasks[0].Reason, want) {
		t.Errorf("skip reason %q does not name the device %q", tasks[0].Reason, want)
	}
	if bus.count() != 0 {
		t.Errorf("Publish was called %d times, want 0 for a skipped device", bus.count())
	}
}

// TestWorker_HandleJobRequested_SkipsNonActiveLifecycle proves a device in
// a non-Active lifecycle state is skipped, citing LifecycleAdmits' own
// reason text.
func TestWorker_HandleJobRequested_SkipsNonActiveLifecycle(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-1", "quarantined-router", "10.0.0.1")
	device.StubState = pkginventory.StateQuarantined
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	_, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeSkipped {
		t.Fatalf("tasks = %+v, want one OutcomeSkipped task", tasks)
	}
	wantReason := fmt.Sprintf("device %q is %s, not active", "quarantined-router", pkginventory.StateQuarantined)
	if tasks[0].Reason != wantReason {
		t.Errorf("skip reason = %q, want %q (LifecycleAdmits' own wording)", tasks[0].Reason, wantReason)
	}
	if bus.count() != 0 {
		t.Errorf("Publish was called %d times, want 0 for a skipped device", bus.count())
	}
}

// TestWorker_HandleJobRequested_SkipsMissingCapability proves a device
// missing a capability the runbook requires is skipped, citing
// CapabilityAdmits' own reason text.
func TestWorker_HandleJobRequested_SkipsMissingCapability(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := &inventorytest.Stub{
		StubID:    "dev-1",
		StubName:  "linux-box",
		StubState: pkginventory.StateActive,
		Caps:      nil, // deliberately does not declare capability.NameCiscoIOS
		Props:     map[string]pkginventory.PropertyValue{"host": "10.0.0.1"},
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	_, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeSkipped {
		t.Fatalf("tasks = %+v, want one OutcomeSkipped task", tasks)
	}
	wantReason := fmt.Sprintf("device %q does not have capability %s", "linux-box", capability.NameCiscoIOS)
	if tasks[0].Reason != wantReason {
		t.Errorf("skip reason = %q, want %q (CapabilityAdmits' own wording)", tasks[0].Reason, wantReason)
	}
	if bus.count() != 0 {
		t.Errorf("Publish was called %d times, want 0 for a skipped device", bus.count())
	}
}

// TestWorker_HandleJobRequested_DispatchesHealthyDevice is the direct
// regression test for the original ID-into-Name bug pkg/wire.DispatchPayload's
// own doc comment documents: it asserts the published payload's DeviceID
// and DeviceName are both correct AND distinct from each other.
func TestWorker_HandleJobRequested_DispatchesHealthyDevice(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-123", "router-display-name", "10.0.0.9")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.State != "running" || job.DispatchedCount != 1 {
		t.Fatalf("job = %+v, want running with DispatchedCount=1 (%s)", job, stateNote)
	}
	if len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeDispatched {
		t.Fatalf("tasks = %+v, want one OutcomeDispatched task", tasks)
	}

	if bus.count() != 1 {
		t.Fatalf("Publish was called %d times, want 1", bus.count())
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	if payload.DeviceID != "dev-id-123" {
		t.Errorf("DeviceID = %q, want %q", payload.DeviceID, "dev-id-123")
	}
	if payload.DeviceName != "router-display-name" {
		t.Errorf("DeviceName = %q, want %q", payload.DeviceName, "router-display-name")
	}
	if payload.DeviceID == payload.DeviceName {
		t.Errorf("DeviceID and DeviceName are identical (%q); the original bug populated DeviceName from device.ID()", payload.DeviceID)
	}
	if payload.DeviceHost != "10.0.0.9" {
		t.Errorf("DeviceHost = %q, want %q", payload.DeviceHost, "10.0.0.9")
	}
	if payload.JobID != jobID || payload.RunbookID != "pb-1" {
		t.Errorf("payload JobID/RunbookID = %q/%q, want %q/%q", payload.JobID, payload.RunbookID, jobID, "pb-1")
	}
	if !payload.Interruptible {
		t.Error("payload.Interruptible = false, want true (pb-1 declares no metadata section, so the safe default applies)")
	}
	if got, want := bus.lastTopic(), topology.DispatchSubject("dev-id-123"); got != want {
		t.Errorf("published topic = %q, want %q", got, want)
	}
}

// TestWorker_HandleJobRequested_AttachesCapabilitiesAndSSHPort proves
// admitAndDispatchDevice (worker_devices.go) populates
// wire.DispatchPayload.Capabilities from the real device's own
// Capabilities() and SSHPort from a real capability.SSHTransportCapable
// type assertion, not left at their Go zero values, since the Runner has
// no inventory backend of its own to re-derive either from (Phase 16,
// Native Go Execution Adapter).
func TestWorker_HandleJobRequested_AttachesCapabilitiesAndSSHPort(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 2222)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	if payload.SSHPort != 2222 {
		t.Errorf("SSHPort = %d, want 2222", payload.SSHPort)
	}
	wantCaps := []capability.Name{capability.NameCiscoIOS, capability.NameSSHTransport}
	if !reflect.DeepEqual(payload.Capabilities, wantCaps) {
		t.Errorf("Capabilities = %+v, want %+v", payload.Capabilities, wantCaps)
	}
}

// TestWorker_HandleJobRequested_AttachesTags proves admitAndDispatchDevice
// populates wire.DispatchPayload.Tags from the real device's own Tags(),
// not left at its Go zero value. This is what internal/adapters/legacy
// (Phase 17, Legacy Ansible Adapter) reads to build inventory.json's
// Ansible group membership; the Runner has no inventory backend of its
// own to re-derive it from, the same reasoning
// TestWorker_HandleJobRequested_AttachesCapabilitiesAndSSHPort already
// established for Capabilities and SSHPort.
func TestWorker_HandleJobRequested_AttachesTags(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-tagged", "core-switch-2", "10.0.0.9")
	device.StubTags = []pkginventory.Tag{"catalyst_lab", "prod"}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	wantTags := []string{"catalyst_lab", "prod"}
	if !reflect.DeepEqual(payload.Tags, wantTags) {
		t.Errorf("Tags = %+v, want %+v", payload.Tags, wantTags)
	}
}

// TestWorker_HandleJobRequested_UntaggedDeviceOmitsTags proves an
// untagged device (Tags() returning nil, the Stub's own zero value)
// produces a nil Tags field rather than an empty-but-non-nil slice, so
// the wire form stays free of a bare "tags":[] key (DispatchPayload.Tags'
// own omitempty).
func TestWorker_HandleJobRequested_UntaggedDeviceOmitsTags(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-untagged", "core-switch-3", "10.0.0.9")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	if strings.Contains(string(bus.last().Data), `"tags"`) {
		t.Errorf("published payload contains a \"tags\" key for an untagged device: %s", bus.last().Data)
	}
}

// TestWorker_HandleJobRequested_AttachesFieldsAndExtraVars proves
// admitAndDispatchDevice carries job's own resolved launch.Fields and
// ExtraVars onto wire.DispatchPayload.Fields/ExtraVars.
// AWX_PARITY_ROADMAP.md Section 3b.1's own diagnosis of this defect: a
// job's Fields/ExtraVars were captured on the record and never referenced
// again anywhere in the codebase, so every execution field the launch
// form let an author set was inert. This is the second of the two wire
// hops that closes: worker_devices.go's payload literal now reads job.
// Fields/job.ExtraVars, not just job.RunbookID/job.Kind.
func TestWorker_HandleJobRequested_AttachesFieldsAndExtraVars(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-fields", "core-switch-4", "10.0.0.9")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	fields := launch.Fields{"forks": 3, "limit": "core-switch-4"}
	extraVars := map[string]any{"deploy_env": "prod"}
	evt := requestJobWithLaunchFields(t, ctx, store, "pb-1", "routers", fields, extraVars)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	// The published payload went through a real JSON encode/decode
	// (capturingBus wraps a real event.Bus), so "forks" comes back as
	// float64, not int: encoding/json's own untyped-number rule for
	// map[string]any, the same reason launch.Fields.Int accepts float64 as
	// one of its input shapes.
	wantFields := map[string]any{"forks": float64(3), "limit": "core-switch-4"}
	if !reflect.DeepEqual(payload.Fields, wantFields) {
		t.Errorf("Fields = %#v, want %#v", payload.Fields, wantFields)
	}
	if !reflect.DeepEqual(payload.ExtraVars, extraVars) {
		t.Errorf("ExtraVars = %#v, want %#v", payload.ExtraVars, extraVars)
	}
}

// TestWorker_HandleJobRequested_NoLaunchFieldsOmitsThemFromTheWire proves a
// job created with no Fields/ExtraVars (the shape every pre-3b.1 launch
// path still produces, and requestJob's own default) publishes a payload
// with neither key, matching DispatchPayload.Fields/ExtraVars' own
// omitempty contract.
func TestWorker_HandleJobRequested_NoLaunchFieldsOmitsThemFromTheWire(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-nofields", "core-switch-5", "10.0.0.9")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	if strings.Contains(string(bus.last().Data), `"fields"`) || strings.Contains(string(bus.last().Data), `"extra_vars"`) {
		t.Errorf("published payload contains a \"fields\" or \"extra_vars\" key for a job with neither: %s", bus.last().Data)
	}
}

// TestWorker_HandleJobRequested_AttachesStoredCredential proves the
// Controller resolves a device's credential at fan-out time and attaches
// it to the payload as the flattened secret map, using a real
// credential.NewLazyFileStore (RULE 0: the real Crawl-tier-shared adapter,
// not a fake), so the JIT-delivery design this phase chose is exercised
// through its own real code, not asserted only against a mock.
func TestWorker_HandleJobRequested_AttachesStoredCredential(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}

	credDir := t.TempDir()
	key, err := credential.ResolveMasterKey(credDir)
	if err != nil {
		t.Fatalf("failed to resolve master key: %v", err)
	}
	if err := credential.SaveFileStore(credDir, key, "core-switch-1", credential.Credential{Username: "admin", Password: "hunter2"}); err != nil {
		t.Fatalf("failed to save fixture credential: %v", err)
	}
	credentials := credential.NewLazyFileStore(credDir)

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, credentials)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	wantSecrets := map[string]string{credential.SecretUsername: "admin", credential.SecretPassword: "hunter2"}
	if !reflect.DeepEqual(payload.Secrets, wantSecrets) {
		t.Errorf("Secrets = %+v, want %+v", payload.Secrets, wantSecrets)
	}
}

// TestWorker_HandleJobRequested_NoStoredCredentialStillDispatches proves a
// device with no stored credential is dispatched normally, with an empty
// Secrets map, rather than being skipped or failed: only a task that
// actually needs a secret should fail downstream, the same place a
// missing credential already fails at the Crawl tier.
func TestWorker_HandleJobRequested_NoStoredCredentialStillDispatches(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	credentials := credential.NewLazyFileStore(t.TempDir())
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, credentials)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.DispatchedCount != 1 || len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeDispatched {
		t.Fatalf("job/tasks = %+v/%+v, want one OutcomeDispatched task despite no stored credential", job, tasks)
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	if len(payload.Secrets) != 0 {
		t.Errorf("Secrets = %+v, want empty", payload.Secrets)
	}
}

// TestWorker_HandleJobRequested_DispatchesInterruptibleFalse proves
// runbook.Runbook.Interruptible (itself resolved from
// engine.Metadata.IsInterruptible(), internal/runbook/dir_source.go)
// actually reaches the published wire.DispatchPayload
// (worker_devices.go's admitAndDispatchDevice), not just that the field
// exists on the wire type: dispatching pb-no-abort (metadata.
// interruptible: false in its own fixture YAML, newTestRunbookSource)
// must publish a payload with Interruptible=false, the opposite of the
// default proven above.
func TestWorker_HandleJobRequested_DispatchesInterruptibleFalse(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-id-456", "router-2", "10.0.0.10")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-no-abort", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	if bus.count() != 1 {
		t.Fatalf("Publish was called %d times, want 1", bus.count())
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	if payload.Interruptible {
		t.Error("payload.Interruptible = true, want false (pb-no-abort declares metadata.interruptible: false)")
	}
}

// TestWorker_HandleJobRequested_RedeliveryDoesNotDoubleDispatch is the
// direct proof of the idempotency guard end to end, through Worker rather
// than JobStore.BeginFanOut alone: redelivering the identical job.requested
// event a second time must not double the total publish count.
func TestWorker_HandleJobRequested_RedeliveryDoesNotDoubleDispatch(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-1", "router-1", "10.0.0.1")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("first HandleJobRequested returned unexpected error: %v", err)
	}
	firstCount := bus.count()
	if firstCount != 1 {
		t.Fatalf("publish count after first delivery = %d, want 1", firstCount)
	}

	// Redeliver the identical event, as at-least-once delivery can.
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("second (redelivered) HandleJobRequested returned unexpected error: %v", err)
	}

	secondCount := bus.count()
	if secondCount != firstCount {
		t.Errorf("total publish count across two deliveries of the same job = %d, want %d (unchanged, not doubled)", secondCount, firstCount)
	}
}

// TestWorker_HandleJobRequested_StaleFanOutIsReclaimed is the direct
// regression test for the crash-then-restart finding: a Worker that claims
// a job's fan-out (BeginFanOut succeeds) and then dies, is OOM-killed, or
// is restarted before recording a single device would otherwise leave the
// job's state frozen in "fanning_out" forever, since every future
// redelivery of the same job.requested message would see began == false
// and skip without doing any work. Once that claim is stale past the
// lease, a redelivered job.requested handed to a fresh Worker instance
// must finish the job instead.
func TestWorker_HandleJobRequested_StaleFanOutIsReclaimed(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	bus := newCapturingBus()
	device := capableDevice("dev-1", "router-1", "10.0.0.1")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	// Simulate the crash: claim the fan-out exactly as HandleJobRequested's
	// own first step would, then stop, mirroring a process that dies
	// immediately after BeginFanOut succeeds and before a single device is
	// recorded.
	began, _, err := store.BeginFanOut(ctx, jobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut returned unexpected error: %v", err)
	}
	if !began {
		t.Fatal("BeginFanOut on a pending job returned began=false, want true")
	}

	// Backdate the row's updated_at past any realistic lease, standing in
	// for real wall-clock time passing while the crashed process never
	// comes back. Direct client access, not through JobStore, since no
	// JobStore method exists to set a timestamp: this is test setup for a
	// scenario, not the behavior under test.
	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(jobID)).
		SetUpdatedAt(time.Now().Add(-24 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job updated_at: %v", err)
	}

	// A fresh Worker instance handling the redelivered job.requested event
	// (NATS JetStream redelivering an unacked message is the exact
	// scenario a durable, at-least-once design exists to handle) must
	// finish the job rather than silently no-oping forever.
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested on the reclaimed job returned unexpected error: %v", err)
	}

	gotJob, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	// What this proves is that the reclaim finished the fan-out rather
	// than leaving it stuck in "fanning_out". It dispatched to a device,
	// so the run itself continues.
	if gotJob.State != "running" {
		t.Fatalf("job State after reclaim = %q, want %q (not stuck in fanning_out)", gotJob.State, "running")
	}
	if gotJob.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount after reclaim = %d, want 1", gotJob.DispatchedCount)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks after reclaim = %+v, want exactly 1 (no duplicate)", tasks)
	}
}

// TestWorker_HandleJobRequested_StaleFanOutReclaimSkipsAlreadyRecordedDevices
// covers the partial-progress case
// TestWorker_HandleJobRequested_StaleFanOutIsReclaimed does not: a crashed
// Worker that finished one device before dying and a second device it
// never reached. The reclaim must not redo admission, republish, or
// re-record the device already recorded by the superseded attempt, while
// still reaching the device it never got to.
func TestWorker_HandleJobRequested_StaleFanOutReclaimSkipsAlreadyRecordedDevices(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	bus := newCapturingBus()
	deviceA := capableDevice("dev-a", "router-a", "10.0.0.1")
	deviceB := capableDevice("dev-b", "router-b", "10.0.0.2")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{deviceA, deviceB}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	began, fence, err := store.BeginFanOut(ctx, jobID, time.Hour)
	if err != nil || !began {
		t.Fatalf("BeginFanOut = (%v, %v), want (true, nil)", began, err)
	}

	// Simulate the crashed attempt having gotten as far as recording
	// deviceA before dying, never reaching deviceB. This is the same
	// direct-store write RecordTask itself performs; the point under test
	// is what the reclaim does with a task list that already has one
	// entry, not RecordTask's own write path (already covered elsewhere).
	if err := store.RecordTask(ctx, jobID, fence, dispatch.JobTask{
		DeviceID:   "dev-a",
		DeviceName: "router-a",
		Outcome:    dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("failed to seed the pre-crash task for dev-a: %v", err)
	}

	if _, err := client.Job.Update().
		Where(entjob.JobIDEQ(jobID)).
		SetUpdatedAt(time.Now().Add(-24 * time.Hour)).
		Save(ctx); err != nil {
		t.Fatalf("failed to backdate job updated_at: %v", err)
	}

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested on the reclaimed job returned unexpected error: %v", err)
	}

	gotJob, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if gotJob.State != "running" {
		t.Fatalf("job State after reclaim = %q, want %q (%s)", gotJob.State, "running", stateNote)
	}
	if gotJob.DispatchedCount != 2 {
		t.Fatalf("DispatchedCount after reclaim = %d, want 2 (1 pre-crash + 1 from the reclaim)", gotJob.DispatchedCount)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks after reclaim = %+v, want exactly 2, one per device (dev-a not re-recorded)", tasks)
	}

	// dev-a must not have been republished: only the reclaim's own
	// dispatch of dev-b should ever reach the bus in this test.
	if bus.count() != 1 {
		t.Fatalf("Publish was called %d times, want 1 (only dev-b, dev-a already handled pre-crash)", bus.count())
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	if payload.DeviceID != "dev-b" {
		t.Errorf("the one publish on reclaim was for DeviceID %q, want %q", payload.DeviceID, "dev-b")
	}
}

// TestWorker_HandleJobRequested_UnresolvableRunbookFailsJob proves a
// job.requested event naming a runbook this Controller cannot resolve
// leaves the job in a "failed" state, genuinely distinguishable from
// "completed", rather than silently completing with all-zero tallies.
func TestWorker_HandleJobRequested_UnresolvableRunbookFailsJob(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: nil}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	evt := requestJob(t, ctx, store, "does-not-exist", "routers")
	jobID := jobIDFromEvent(t, evt)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.State != "failed" {
		t.Fatalf("job State = %q, want %q", job.State, "failed")
	}
	if len(tasks) != 0 {
		t.Errorf("tasks = %+v, want none: fan-out never reached the device loop", tasks)
	}
	if bus.count() != 0 {
		t.Errorf("Publish was called %d times, want 0", bus.count())
	}
}

// blockingJobStore wraps a real JobStore (the same production entJobStore
// every other test in this file already exercises, per RULE 0) and makes
// its Get call block past whatever bound the caller's own context
// carries: real production collaborators (a DB driver, a NATS client) are
// themselves context-aware and abort a blocked operation the moment the
// caller's ctx is done, which is exactly what this stands in for, rather
// than a mock that would never observe a context deadline at all.
type blockingJobStore struct {
	dispatch.JobStore
	// blockFor is how long Get pretends to hang before it would otherwise
	// return, standing in for a genuinely stuck downstream dependency
	// (a stalled query, a wedged connection): chosen far longer than the
	// test's own short lease override so a passing test can only mean the
	// call was actually cut off early by ctx, never that it happened to
	// finish first.
	blockFor time.Duration
}

// Get blocks until either ctx is done (the realistic, context-aware
// behavior this fake models) or blockFor elapses, whichever comes first,
// then delegates to the wrapped real store. A genuinely unbounded ctx
// would make this indistinguishable from a real hang; a bounded one
// returns ctx.Err() well before blockFor ever elapses.
func (s *blockingJobStore) Get(ctx context.Context, jobID string) (*dispatch.Job, []dispatch.JobTask, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(s.blockFor):
		return s.JobStore.Get(ctx, jobID)
	}
}

// TestWorker_HandleJobRequested_ContextIsBounded is the direct proof of
// the fix for Finding 3: HandleJobRequested's own context must carry a
// real deadline (tied to the Worker's fan-out lease window, see
// WithFanOutLeaseTTL), so a hung downstream call is cut off and the
// handler returns instead of blocking forever. leaseTTL is overridden to
// a short, test-scoped duration (not the real, production ten-minute
// default) so this test proves the bound is genuinely in effect without
// making the suite slow.
func TestWorker_HandleJobRequested_ContextIsBounded(t *testing.T) {
	ctx := t.Context()
	realStore := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: nil}

	const leaseTTL = 50 * time.Millisecond
	// blockFor comfortably exceeds leaseTTL: if HandleJobRequested's own
	// context were still the old, unbounded context.Background(), Get
	// would run to completion at blockFor and the test below would
	// observe an elapsed time close to blockFor, not leaseTTL.
	const blockFor = 2 * time.Second
	store := &blockingJobStore{JobStore: realStore, blockFor: blockFor}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil, dispatch.WithFanOutLeaseTTL(leaseTTL))

	evt := requestJob(t, ctx, realStore, "pb-1", "routers")

	start := time.Now()
	err := worker.HandleJobRequested(evt)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("HandleJobRequested on a job whose Get call hangs past the lease returned nil error, want a context deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("HandleJobRequested error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	// The generous upper bound (well under blockFor, comfortably above
	// leaseTTL) is what distinguishes "the context actually bounded this
	// call" from "the call happened to finish some other way": a failure
	// here means the handler blocked for close to the full, unbounded
	// blockFor instead of being cut off at leaseTTL.
	if elapsed >= blockFor {
		t.Errorf("HandleJobRequested took %v to return, want well under blockFor (%v): the context did not bound the hung call", elapsed, blockFor)
	}
}

// reclaimAfterFirstRecordTask wraps a real JobStore (per RULE 0, the same
// production entJobStore newTestStore builds) and, right after its first
// successful RecordTask call, performs a genuine concurrent reclaim through
// the same client TestWorker_HandleJobRequested_StaleFanOutIsReclaimed
// already proves reclaims a job: backdate the row's updated_at past any
// realistic lease, then issue a fresh BeginFanOut. This stands in for a
// second worker's redelivery reclaiming the job mid-fan-out. The *next*
// RecordTask call the original, still-running HandleJobRequested makes,
// using the fence it obtained before the reclaim, then hits a genuine
// ErrFenced from the real store's own fence check (ent_store.go's
// RecordTask), never a mocked one.
type reclaimAfterFirstRecordTask struct {
	dispatch.JobStore
	t      *testing.T
	client *ent.Client
	jobID  string
	// reclaimed guards the simulated reclaim to fire exactly once: a second
	// device's RecordTask call must observe the already-reclaimed fence,
	// not trigger another reclaim of its own.
	reclaimed bool
}

// RecordTask delegates to the wrapped real store first, so the write this
// call represents genuinely lands exactly like production. Only once, on
// the first call that succeeds, it then reclaims jobID's fan-out out from
// under the caller, so every RecordTask call after this one is fenced.
func (s *reclaimAfterFirstRecordTask) RecordTask(ctx context.Context, jobID string, fence int64, task dispatch.JobTask) error {
	err := s.JobStore.RecordTask(ctx, jobID, fence, task)
	if err != nil || s.reclaimed {
		return err
	}
	s.reclaimed = true

	if _, uErr := s.client.Job.Update().
		Where(entjob.JobIDEQ(s.jobID)).
		SetUpdatedAt(time.Now().Add(-24 * time.Hour)).
		Save(ctx); uErr != nil {
		s.t.Fatalf("failed to backdate job updated_at for simulated reclaim: %v", uErr)
	}
	began, _, bErr := s.JobStore.BeginFanOut(ctx, s.jobID, time.Hour)
	if bErr != nil || !began {
		s.t.Fatalf("simulated reclaim BeginFanOut = (%v, %v), want (true, nil)", began, bErr)
	}
	return nil
}

// TestWorker_HandleJobRequested_FencedMidLoopStopsWithoutError is the direct
// proof of fenced's own contract (worker_devices.go): once this call's own
// fence is superseded mid-device-loop by another worker's reclaim,
// HandleJobRequested must stop immediately and return nil (acking the
// delivery) rather than surface the store's ErrFenced as an ordinary error,
// which would trigger a pointless redelivery-triggered retry a stale fence
// could never benefit from.
func TestWorker_HandleJobRequested_FencedMidLoopStopsWithoutError(t *testing.T) {
	ctx := t.Context()
	realStore, client := newTestStore(t)
	bus := newCapturingBus()
	deviceA := capableDevice("dev-a", "router-a", "10.0.0.1")
	deviceB := capableDevice("dev-b", "router-b", "10.0.0.2")
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{deviceA, deviceB}}

	evt := requestJob(t, ctx, realStore, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	store := &reclaimAfterFirstRecordTask{JobStore: realStore, t: t, client: client, jobID: jobID}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned %v, want nil (a mid-loop fenced write must be acked, not retried)", err)
	}

	if !store.reclaimed {
		t.Fatal("test setup did not reach the simulated reclaim: dev-a's RecordTask never succeeded")
	}

	gotJob, tasks, err := realStore.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	// The reclaim left the job in "fanning_out" under a new fence, owned by
	// the simulated second worker, not "completed": the original call
	// stopped the instant it was fenced, never reaching Complete.
	if gotJob.State != "fanning_out" {
		t.Errorf("job State after the original caller was fenced = %q, want %q", gotJob.State, "fanning_out")
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks after the original caller was fenced = %+v, want exactly 1 (only dev-a, recorded before the reclaim)", tasks)
	}
	if tasks[0].DeviceID != "dev-a" {
		t.Errorf("the one recorded task is for device %q, want %q", tasks[0].DeviceID, "dev-a")
	}
}

// jobIDFromEvent decodes the job_id this test package's own requestJob
// embedded into evt.Data.
func jobIDFromEvent(t testing.TB, evt event.Event) string {
	t.Helper()
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		t.Fatalf("failed to decode job_id from event: %v", err)
	}
	return payload.JobID
}

// cancelAfterFirstRecordTask cancels the job out from under the running
// fan-out, once, immediately after the first device's RecordTask write
// genuinely lands. It mirrors reclaimAfterFirstRecordTask's own shape and
// exists for the same reason: the cancel has to arrive DURING the loop,
// which no amount of arranging state before HandleJobRequested can
// reproduce.
type cancelAfterFirstRecordTask struct {
	dispatch.JobStore
	t     *testing.T
	jobID string
	// canceled guards the cancel to fire exactly once. A second call must
	// observe the already-canceled job rather than cancel it again, which
	// would be refused as ErrNotCancelable and mask what is being tested.
	canceled bool
}

// RecordTask delegates to the wrapped real store first, so the write this
// call represents lands exactly like production, then stops the job.
func (s *cancelAfterFirstRecordTask) RecordTask(ctx context.Context, jobID string, fence int64, task dispatch.JobTask) error {
	err := s.JobStore.RecordTask(ctx, jobID, fence, task)
	if err != nil || s.canceled {
		return err
	}
	s.canceled = true

	if cErr := s.JobStore.Cancel(ctx, s.jobID, "operator"); cErr != nil {
		s.t.Fatalf("simulated cancel returned unexpected error: %v", cErr)
	}
	return nil
}

// TestWorker_HandleJobRequested_CanceledMidLoopStopsWithoutError is the
// durable half of cancel proven at the real seam: a job stopped while its
// fan-out is running dispatches to no further device, acks its delivery
// rather than failing it, and leaves a record whose tallies say what the
// fan-out had actually reached.
//
// Three devices, not two, so "stopped" is distinguishable from "ran out of
// devices": with two, a loop that ignored the cancel entirely would produce
// the same task count as one that obeyed it on the last iteration.
func TestWorker_HandleJobRequested_CanceledMidLoopStopsWithoutError(t *testing.T) {
	ctx := t.Context()
	realStore, _ := newTestStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		capableDevice("dev-a", "router-a", "10.0.0.1"),
		capableDevice("dev-b", "router-b", "10.0.0.2"),
		capableDevice("dev-c", "router-c", "10.0.0.3"),
	}}

	evt := requestJob(t, ctx, realStore, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)

	store := &cancelAfterFirstRecordTask{JobStore: realStore, t: t, jobID: jobID}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned %v, want nil (a canceled job must be acked, not retried: redelivering it would achieve nothing)", err)
	}
	if !store.canceled {
		t.Fatal("test setup never reached the simulated cancel: dev-a's RecordTask never succeeded")
	}

	gotJob, tasks, err := realStore.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if gotJob.State != "canceled" {
		t.Errorf("job State after a mid-loop cancel = %q, want %q: the loop must not have driven it to a terminal state of its own", gotJob.State, "canceled")
	}
	if gotJob.CanceledBy != "operator" {
		t.Errorf("CanceledBy = %q, want %q", gotJob.CanceledBy, "operator")
	}

	// dev-c is the device that proves the loop stopped. It is never
	// published to and never recorded, which is the whole promise: work
	// the job has not yet reached does not happen.
	if len(tasks) != 1 {
		t.Fatalf("recorded tasks after the cancel = %d, want 1 (only dev-a, recorded before the cancel landed)", len(tasks))
	}
	if tasks[0].DeviceID != "dev-a" {
		t.Errorf("the one recorded task is for device %q, want %q", tasks[0].DeviceID, "dev-a")
	}
	if gotJob.DispatchedCount != 1 {
		t.Errorf("DispatchedCount on the canceled record = %d, want 1: a canceled job must report what its fan-out actually reached, not zero", gotJob.DispatchedCount)
	}

	// The known, bounded overshoot, asserted rather than left to be
	// discovered later. dev-b's dispatch was published BEFORE its
	// RecordTask refused, because the publish precedes the write, so two
	// devices were dispatched to while only one was recorded. This window
	// is inherent: a cancel can land between any publish and its write, so
	// no ordering removes it, and closing it would cost a second read of
	// the job per device in a loop built to stream ten thousand of them.
	// dev-c is what must never be published to.
	if bus.count() != 2 {
		t.Errorf("published dispatches = %d, want exactly 2: dev-a, plus dev-b already in flight when the cancel landed, and never dev-c", bus.count())
	}
	for _, evt := range bus.published {
		var payload wire.DispatchPayload
		if err := json.Unmarshal(evt.Data, &payload); err != nil {
			t.Fatalf("failed to decode a published dispatch: %v", err)
		}
		if payload.DeviceID == "dev-c" {
			t.Error("dev-c was dispatched to after the job was canceled: the fan-out did not stop")
		}
	}
}
