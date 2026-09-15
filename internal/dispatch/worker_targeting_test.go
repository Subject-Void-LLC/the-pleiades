package dispatch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// This file covers what a job dispatches against.
//
// Before Phase 21 a job named a free-text group and the Worker streamed
// Selector{GroupName}. Now a job names an Inventory, and the Worker streams
// that inventory's membership: its groups plus the devices attached to it
// directly. The assertions here are mostly about the refusals, because
// every one of them has an alternative that is silently worse than an
// error, and the worst of them dispatches to the entire fleet.

// capturingRepository records the selector it was handed, which is the only
// way to prove the Worker asked for the right devices rather than for all
// of them.
type capturingRepository struct {
	*fakeRepository
	got pkginventory.Selector
}

func (r *capturingRepository) GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	r.got = sel
	return r.fakeRepository.GetGroup(ctx, sel)
}

// fakeSetStore resolves one Inventory, or refuses.
type fakeSetStore struct {
	set inventory.Set
	err error
}

func (s fakeSetStore) Get(context.Context, int) (inventory.Set, error) { return s.set, s.err }

func (s fakeSetStore) Create(context.Context, inventory.Set) (inventory.Set, error) {
	return inventory.Set{}, nil
}
func (s fakeSetStore) List(context.Context, inventory.SetQuery) ([]inventory.Set, error) {
	return nil, nil
}
func (s fakeSetStore) Update(context.Context, inventory.Set) error { return nil }
func (s fakeSetStore) Delete(context.Context, int) error           { return nil }
func (s fakeSetStore) SetsForDevice(context.Context, int) ([]int, error) {
	return nil, nil
}

// ListMembers is a form-chooser concern. Dispatch never asks.
func (s fakeSetStore) ListMembers(context.Context, int) (inventory.Members, error) {
	return inventory.Members{}, nil
}
func (s fakeSetStore) ListOrganizations(context.Context) ([]inventory.Organization, error) {
	return nil, nil
}

// targetedJob creates a job naming an inventory and returns its
// job.requested event.
func targetedJob(t *testing.T, ctx context.Context, store dispatch.JobStore, inventoryID int) (event.Event, string) {
	t.Helper()

	job := &dispatch.Job{
		RunbookID:      "pb-1",
		Actor:          "user@example.com",
		InventoryID:    inventoryID,
		TemplateID:     3,
		TemplateName:   "patch the edge routers",
		Kind:           "runbook",
		OrganizationID: 7,
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := json.Marshal(map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatalf("failed to marshal job.requested payload: %v", err)
	}
	return event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}, job.JobID
}

func oneDevice() []pkginventory.InventoryItem {
	return []pkginventory.InventoryItem{
		&inventorytest.Stub{
			StubID:    "dev-1",
			StubName:  "edge-01",
			StubState: pkginventory.StateActive,
			Caps:      []capability.Name{capability.NameCiscoIOS},
			Props:     map[string]pkginventory.PropertyValue{"host": "10.0.0.1"},
		},
	}
}

func TestWorker_StreamsTheInventoryMembershipAJobNames(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}

	sets := fakeSetStore{set: inventory.Set{ID: 4, GroupIDs: []int{11, 12}, DeviceIDs: []int{21}}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), newCapturingBus(), nil,
		dispatch.WithSetStore(sets))

	evt, jobID := targetedJob(t, ctx, store, 4)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}

	// The selector carries a membership, not a group name and not the zero
	// value. The zero value is the dangerous one: it streams every device
	// the platform manages.
	if repo.got.Membership == nil {
		t.Fatal("the Worker streamed an unrestricted selector for a job that named an inventory")
	}
	if len(repo.got.Membership.GroupIDs) != 2 || len(repo.got.Membership.DeviceIDs) != 1 {
		t.Errorf("the Worker streamed membership %+v, want the inventory's own groups and devices", repo.got.Membership)
	}

	job, _, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.State != "completed" {
		t.Errorf("the job is %q, want completed: %s", job.State, job.FailureReason)
	}
}

func TestWorker_AJobNamingNoInventoryStillStreamsByGroup(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), newCapturingBus(), nil)

	// A pre-Phase-21 job. It streams by group name exactly as it always
	// did, so the change is additive rather than a break.
	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}

	if repo.got.Membership != nil {
		t.Error("a job naming no inventory was given a membership selector")
	}
	if repo.got.GroupName != "routers" {
		t.Errorf("the Worker streamed group %q, want routers", repo.got.GroupName)
	}
}

func TestWorker_RefusesAJobItCannotTargetRatherThanDispatchingToEverything(t *testing.T) {
	cases := map[string]struct {
		sets       []dispatch.WorkerOption
		wantReason string
	}{
		// A Worker built without the port cannot resolve an inventory.
		// Falling through to an unrestricted selector would dispatch this
		// job to every device the platform manages.
		"no set store wired": {
			sets:       nil,
			wantReason: "cannot resolve the inventory",
		},
		// Somebody deleted the set a template names.
		"the inventory is gone": {
			sets:       []dispatch.WorkerOption{dispatch.WithSetStore(fakeSetStore{err: inventory.ErrSetNotFound})},
			wantReason: "no longer exists",
		},
		// A fan-out that reaches zero devices is indistinguishable from
		// one that failed, so the job says which it was.
		"the inventory is empty": {
			sets:       []dispatch.WorkerOption{dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4}})},
			wantReason: "contains no devices",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			store := newTestJobStore(t)
			repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
			worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), newCapturingBus(), nil, tc.sets...)

			evt, jobID := targetedJob(t, ctx, store, 4)
			if err := worker.HandleJobRequested(evt); err != nil {
				t.Fatalf("HandleJobRequested: %v", err)
			}

			job, tasks, err := store.Get(ctx, jobID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if job.State != "failed" {
				t.Fatalf("the job is %q, want failed", job.State)
			}
			if len(tasks) != 0 {
				t.Errorf("a job that could not be targeted dispatched to %d devices", len(tasks))
			}
			if repo.got.Membership == nil && repo.got.GroupName == "" && len(tasks) > 0 {
				t.Error("the Worker streamed an unrestricted selector")
			}
			if reason := job.FailureReason; !strings.Contains(reason, tc.wantReason) {
				t.Errorf("the job says %q, want a reason containing %q", reason, tc.wantReason)
			}
		})
	}
}

func TestWorker_TheKindTravelsFromTheJobToEveryDeviceDispatch(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
	bus := newCapturingBus()

	sets := fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithSetStore(sets),
		// The playbook kind's own definition source, because the fan-out
		// prepares a job through the source its kind owns. This test used
		// to pass without one, and that was the recorded defect: the
		// worker rammed every job through the runbook source, so a
		// playbook job only survived fan-out by the accident of its id
		// naming a runbook too.
		dispatch.WithDefinitionSource("playbook",
			dispatch.NewPlaybookDefinitionSource(staticPlaybooks{"pb-1"})))

	// A job launched from a playbook template. The kind is what the Runner
	// routes on, so it has to reach the wire; a Runner that had to infer it
	// would be inferring from a set it was compiled with rather than
	// reading a value it was given, which is the closed-enum cost the open
	// registry exists to avoid.
	job := &dispatch.Job{
		RunbookID:   "pb-1",
		Actor:       "user@example.com",
		InventoryID: 4,
		TemplateID:  3,
		Kind:        "playbook",
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := json.Marshal(map[string]string{"job_id": job.JobID})
	if err != nil {
		t.Fatalf("marshalling the payload: %v", err)
	}
	if err := worker.HandleJobRequested(event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}

	var dispatched wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &dispatched); err != nil {
		t.Fatalf("decoding the dispatch payload: %v", err)
	}
	if dispatched.Kind != "playbook" {
		t.Errorf("the dispatch carries kind %q, want the job's playbook", dispatched.Kind)
	}
}

func TestJobStore_ListForTemplateAnswersWhatOneTemplateHasRun(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	// Three jobs from two templates, plus one from none at all. The last is
	// the control: a job launched before templates existed must not be
	// returned as though some particular template had run it.
	for _, j := range []*dispatch.Job{
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 7, TemplateName: "patch"},
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 7, TemplateName: "patch"},
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 9, TemplateName: "reboot"},
		{RunbookID: "pb-1", Actor: "a@example.com", GroupName: "routers"},
	} {
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	found, err := store.ListForTemplate(ctx, 7, 50)
	if err != nil {
		t.Fatalf("ListForTemplate: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("template 7 has run %d jobs, want 2", len(found))
	}
	for _, j := range found {
		if j.TemplateID != 7 {
			t.Errorf("a job from template %d was returned for template 7", j.TemplateID)
		}
	}

	// Newest first, on the job id, which is a UUIDv7 and therefore time
	// ordered: a detail page's Completed Jobs section shows the most recent
	// run at the top without a second index.
	if found[0].JobID < found[1].JobID {
		t.Errorf("jobs came back oldest first: %q before %q", found[0].JobID, found[1].JobID)
	}

	// The bound is honoured, so a template that has run ten thousand times
	// does not put ten thousand rows on one page.
	if limited, err := store.ListForTemplate(ctx, 7, 1); err != nil || len(limited) != 1 {
		t.Errorf("ListForTemplate with limit 1 returned %d jobs, %v", len(limited), err)
	}

	// A template that has never run is an ordinary state, not a missing
	// record: this port does not know whether a template exists.
	if none, err := store.ListForTemplate(ctx, 4242, 50); err != nil || len(none) != 0 {
		t.Errorf("ListForTemplate of a template that has run nothing returned %d jobs, %v", len(none), err)
	}

	// Zero is refused rather than treated as "no template", which would
	// return every job created before templates existed as though one
	// template had launched them all.
	if _, err := store.ListForTemplate(ctx, 0, 50); err == nil {
		t.Error("ListForTemplate accepted template id 0")
	}
	if _, err := store.ListForTemplate(ctx, 7, 0); err == nil {
		t.Error("ListForTemplate accepted limit 0")
	}
}

// TestJobStore_RecentForTemplatesBatchesAcrossManyTemplates is
// RecentForTemplates: the list page's own reason ListForTemplate exists
// for one record, batched across many, so a page of templates costs one
// query rather than one per row.
func TestJobStore_RecentForTemplatesBatchesAcrossManyTemplates(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	// Three jobs from template 7, one from template 9, and one from
	// neither -- the same "job launched before templates existed" control
	// ListForTemplate's own test uses.
	for _, j := range []*dispatch.Job{
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 7, TemplateName: "patch"},
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 7, TemplateName: "patch"},
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 7, TemplateName: "patch"},
		{RunbookID: "pb-1", Actor: "a@example.com", TemplateID: 9, TemplateName: "reboot"},
		{RunbookID: "pb-1", Actor: "a@example.com", GroupName: "routers"},
	} {
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	found, err := store.RecentForTemplates(ctx, []int{7, 9, 4242}, 2)
	if err != nil {
		t.Fatalf("RecentForTemplates: %v", err)
	}

	// The bound is honoured per template, not across the whole call: three
	// jobs from template 7 come back as two, not zero and not three.
	if len(found[7]) != 2 {
		t.Fatalf("template 7 returned %d jobs, want 2 (perTemplate bound)", len(found[7]))
	}
	for _, j := range found[7] {
		if j.TemplateID != 7 {
			t.Errorf("a job from template %d was returned under template 7", j.TemplateID)
		}
	}
	if found[7][0].JobID < found[7][1].JobID {
		t.Errorf("template 7's jobs came back oldest first: %q before %q", found[7][0].JobID, found[7][1].JobID)
	}

	if len(found[9]) != 1 {
		t.Fatalf("template 9 returned %d jobs, want 1", len(found[9]))
	}

	// A requested template with no jobs is absent from the map entirely,
	// not present with an empty slice, so a caller's membership check is
	// one map lookup.
	if _, ok := found[4242]; ok {
		t.Error("a template that has never run has an entry in the map")
	}

	// And a template nobody asked about (the groupless job's template,
	// which is a zero) never appears, even though a row for it exists.
	if _, ok := found[0]; ok {
		t.Error("RecentForTemplates returned an entry for a template id nobody asked for")
	}

	if empty, err := store.RecentForTemplates(ctx, nil, 2); err != nil || len(empty) != 0 {
		t.Errorf("RecentForTemplates with no template ids returned %v, %v, want an empty map and no error", empty, err)
	}
	if _, err := store.RecentForTemplates(ctx, []int{7}, 0); err == nil {
		t.Error("RecentForTemplates accepted perTemplate 0")
	}
}
