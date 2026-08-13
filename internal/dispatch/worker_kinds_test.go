package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// This file covers the fan-out's kind seam: a job is prepared through the
// definition source its kind owns, and a kind with no source fails the job
// with a reason naming the kind.
//
// The seam exists because the worker used to resolve every job through the
// runbook source unconditionally. A playbook-kind job then failed inside
// the Controller with "runbook not found", one of the four independent
// walls that made the playbook kind a facade: undiscoverable in the form,
// undispatchable here, unexecutable in the runner, and named by a grammar
// its resolver rejected. Each wall stood behind the others, so each
// package's tests stayed green.

// staticPlaybooks is a PlaybookGetter over a fixed id set.
type staticPlaybooks []string

func (s staticPlaybooks) Get(_ context.Context, id string) ([]byte, error) {
	for _, known := range s {
		if known == id {
			return []byte("---\n- hosts: all\n"), nil
		}
	}
	return nil, fmt.Errorf("playbook: not found: %q", id)
}

// launchPlaybookJob creates a playbook-kind job targeting the fake
// inventory and hands its job.requested event to worker.
func launchPlaybookJob(t *testing.T, store dispatch.JobStore, worker *dispatch.Worker, definition string) *dispatch.Job {
	t.Helper()
	ctx := context.Background()

	job := &dispatch.Job{
		RunbookID:   definition,
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
	return job
}

func TestWorker_APlaybookJobIsPreparedThroughItsOwnSource(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
	bus := newCapturingBus()

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}),
		dispatch.WithDefinitionSource("playbook",
			dispatch.NewPlaybookDefinitionSource(staticPlaybooks{"site"})))

	job := launchPlaybookJob(t, store, worker, "site")

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("job state = %q (reason %q), want completed", got.State, got.FailureReason)
	}
	if got.DispatchedCount != 1 {
		t.Errorf("dispatched %d devices, want 1", got.DispatchedCount)
	}

	// The dispatch itself carries the kind and no capability gate kept the
	// device out: a playbook's requirements are deliberately empty because
	// the platform never parses one, so admission is lifecycle plus
	// addressability and the per-host verdict is Ansible's own.
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("decoding the dispatch payload: %v", err)
	}
	if payload.Kind != "playbook" {
		t.Errorf("payload kind = %q, want playbook", payload.Kind)
	}
	if payload.Interruptible {
		t.Error("a playbook dispatch claims to be interruptible, which nothing here can promise")
	}
}

func TestWorker_APlaybookJobFailsWhenItsPlaybookIsGone(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), newCapturingBus(), nil,
		dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}),
		dispatch.WithDefinitionSource("playbook",
			dispatch.NewPlaybookDefinitionSource(staticPlaybooks{})))

	job := launchPlaybookJob(t, store, worker, "vanished")

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("job state = %q, want failed", got.State)
	}
	// The reason names the playbook, not a runbook: what the old
	// unconditional resolution reported here ("runbook \"vanished\" not
	// found") described an object the job never involved.
	if !strings.Contains(got.FailureReason, "playbook") || !strings.Contains(got.FailureReason, "vanished") {
		t.Errorf("failure reason = %q, want it to name the missing playbook", got.FailureReason)
	}
	if strings.Contains(got.FailureReason, "runbook") {
		t.Errorf("failure reason = %q blames a runbook, which this job never involved", got.FailureReason)
	}
}

func TestWorker_AKindWithNoSourceFailsTheJobByName(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
	bus := newCapturingBus()

	// No WithDefinitionSource: this controller can prepare runbook jobs
	// and nothing else, which is exactly what a deployment that never
	// configured a playbook directory looks like.
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}))

	job := launchPlaybookJob(t, store, worker, "site")

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("job state = %q, want failed", got.State)
	}
	if !strings.Contains(got.FailureReason, `"playbook"`) {
		t.Errorf("failure reason = %q, want it to name the kind this controller cannot prepare", got.FailureReason)
	}
	if bus.count() != 0 {
		t.Errorf("published %d dispatches for an unpreparable job, want 0", bus.count())
	}
}

func TestWorker_AnEmptyKindStillMeansRunbook(t *testing.T) {
	// The additive-field rule: a job created before Kind existed carries
	// none, and must reach the resolution path it was always going to
	// reach. This is the same default routing.Resolve applies on the
	// Runner side, stated once in launch.ResolveKind.
	ctx := t.Context()
	store := newTestJobStore(t)
	repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
	bus := newCapturingBus()

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}))

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "user@example.com", InventoryID: 4}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, _ := json.Marshal(map[string]string{"job_id": job.JobID})
	if err := worker.HandleJobRequested(event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}

	got, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("job state = %q (reason %q), want completed", got.State, got.FailureReason)
	}
}
