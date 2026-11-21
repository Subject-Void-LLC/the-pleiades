// Tests that the Controller refuses a runbook whose condition reads stored
// facts at admission, before publishing anything (facts.go), through the
// real Worker, the real ent store and a real runbook directory, and that a
// rollback job of the same runbook is never refused by it.
package dispatch_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factsRunbookSource is a runbook directory holding pb-1, which reads no
// fact, and pb-facts, whose second task decides on a stored fact.
func factsRunbookSource(t *testing.T) runbook.Source {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"pb-1":     "id: pb-1\ntasks:\n  - name: step\n    fqcn: ios_backup\n",
		"pb-facts": "id: pb-facts\ntasks:\n  - name: step\n    fqcn: ios_backup\n  - name: on seventeen\n    when: facts[\"os.version\"] == \"17.3\"\n    fqcn: ios_backup\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestFanOut_ARunbookReadingStoredFactsIsRefusedBeforeContact: every device
// of a job whose runbook reads a stored fact is failed at admission, naming
// the task and the key and saying nothing ran and what works on this tier,
// and no dispatch is published, so no Runner ever reaches a device.
func TestFanOut_ARunbookReadingStoredFactsIsRefusedBeforeContact(t *testing.T) {
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	repo.Devices = append(repo.Devices, capableDevice("id-0", "dev-0", "10.0.0.1"), capableDevice("id-1", "dev-1", "10.0.0.2"))
	worker := dispatch.NewWorker(store, repo, factsRunbookSource(t), bus, nil)
	evt := requestJobWithLaunchFields(t, t.Context(), store, "pb-facts", "routers", nil, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	bus.mu.Lock()
	published := len(bus.published)
	bus.mu.Unlock()
	if published != 0 {
		t.Fatalf("published %d dispatches for a runbook reading stored facts, want none", published)
	}
	job, tasks, err := store.Get(t.Context(), jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task rows %+v, want one per device", tasks)
	}
	for _, task := range tasks {
		if task.Outcome != dispatch.OutcomeFailed {
			t.Errorf("%s: outcome %s, want failed", task.DeviceName, task.Outcome)
		}
		for _, want := range []string{"refused before contact", `tasks[1] (name "on seventeen")`, `facts["os.version"]`, "Runner cannot read stored facts in this release", "nothing ran"} {
			if !strings.Contains(task.Reason, want) {
				t.Errorf("%s: reason %q does not say %q", task.DeviceName, task.Reason, want)
			}
		}
	}
	if job.FailedCount != 2 || job.DispatchedCount != 0 {
		t.Errorf("job %+v, want both devices failed and none dispatched", job)
	}
}

// TestFanOut_ARollbackOfARunbookReadingFactsIsNotRefusedByIt: a rollback job
// runs its plan's steps, which read no fact, so the runbook it undoes reading
// one does not stop it.
func TestFanOut_ARollbackOfARunbookReadingFactsIsNotRefusedByIt(t *testing.T) {
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	repo.Devices = append(repo.Devices, capableDevice("id-0", "dev-0", "10.0.0.1"))
	worker := dispatch.NewWorker(store, repo, factsRunbookSource(t), bus, nil)
	plan := &dispatch.RollbackPlan{DAGVersion: "sha256:" + strings.Repeat("a", 64), Devices: []dispatch.RollbackDevice{
		{DeviceID: "id-0", DeviceName: "dev-0", Steps: []wire.RollbackStep{{Node: "tasks[0]", Emitter: "ios_backup", Method: "file.remove", Params: map[string]any{"path": "/tmp/x"}, Name: "undo", Source: "recorded"}}},
	}}
	job := &dispatch.Job{RunbookID: "pb-facts", GroupName: "routers", Actor: "user@example.com", RollbackOf: "job-x", Rollback: plan}
	if err := store.Create(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"job_id": job.JobID})
	if err := worker.HandleJobRequested(event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}); err != nil {
		t.Fatal(err)
	}
	bus.mu.Lock()
	published := len(bus.published)
	bus.mu.Unlock()
	if published != 1 {
		_, tasks, _ := store.Get(t.Context(), job.JobID)
		t.Fatalf("a rollback of a runbook reading facts published %d dispatches, want its one step's device: %+v", published, tasks)
	}
}

// TestRefuseFactReads_NamesEveryTaskAndWrapsItsError: the refusal names each
// reading task and its keys, wraps ErrFactReads, and is nil for none.
func TestRefuseFactReads_NamesEveryTaskAndWrapsItsError(t *testing.T) {
	if err := dispatch.RefuseFactReads(nil); err != nil {
		t.Fatalf("no reads: %v", err)
	}
	err := dispatch.RefuseFactReads([]engine.FactRead{{Node: "tasks[0]", Label: "tasks[0]", Keys: []string{"a.b"}}, {Node: "tasks[2]", Label: `tasks[2] (name "x")`, Keys: []string{"c.d", "e.f"}}})
	if !errors.Is(err, dispatch.ErrFactReads) || !strings.Contains(err.Error(), `facts["a.b"]`) || !strings.Contains(err.Error(), `facts["c.d"], facts["e.f"]`) {
		t.Fatalf("refusal = %v", err)
	}
}
