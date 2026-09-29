// Tests for a rollback job's fan-out (rollback.go): the real Worker, the
// real ent store over SQLite and the real ResultConsumer, with the
// inventory the one stand-in.
package dispatch_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// rollbackPlan undoes one step on id-0, two on id-2, and one on id-9,
// which the inventory does not hold.
func rollbackPlan() *dispatch.RollbackPlan {
	step := func(node, method string, index int) wire.RollbackStep {
		return wire.RollbackStep{Node: node, Index: index, Emitter: "ios_backup", Method: method, Params: map[string]any{"path": "/tmp/" + node}, Name: "undo " + node, Source: "recorded"}
	}
	return &dispatch.RollbackPlan{DAGVersion: "sha256:" + strings.Repeat("a", 64), Devices: []dispatch.RollbackDevice{
		{DeviceID: "id-0", DeviceName: "dev-0", Steps: []wire.RollbackStep{step("tasks[0]", "file.remove", 0)}},
		{DeviceID: "id-2", DeviceName: "dev-2", Steps: []wire.RollbackStep{step("tasks[1]", "file.remove", 0), step("tasks[0]", "file.remove", 0)}},
		{DeviceID: "id-9", DeviceName: "dev-9", Steps: []wire.RollbackStep{step("tasks[0]", "file.remove", 0)}},
	}}
}

// requestRollback creates a rollback job of job-x over three devices and
// fans it out.
func requestRollback(t *testing.T, fields launch.Fields) (*windowFixture, event.Event) {
	t.Helper()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	for i := 0; i < 3; i++ {
		repo.Devices = append(repo.Devices, capableDevice(fmt.Sprintf("id-%d", i), fmt.Sprintf("dev-%d", i), "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)
	job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "user@example.com", Fields: fields,
		RollbackOf: "job-x", Rollback: rollbackPlan()}
	if err := store.Create(t.Context(), job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, _ := json.Marshal(map[string]string{"job_id": job.JobID})
	evt := event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	return &windowFixture{store: store, bus: bus, repo: repo, worker: worker, jobID: job.JobID}, evt
}

// TestRollbackJob_DispatchesItsPlanOnTheRollbackSubject is the fan-out's
// whole contract: only planned devices, each with its own steps, all on
// the rollback subject, and a planned device the inventory lost recorded
// as failed. With and without a forks window, since the pump builds its
// dispatches through the same two functions.
func TestRollbackJob_DispatchesItsPlanOnTheRollbackSubject(t *testing.T) {
	for _, forks := range []int{0, 1} {
		t.Run(fmt.Sprintf("forks %d", forks), func(t *testing.T) {
			var fields launch.Fields
			if forks > 0 {
				fields = launch.Fields{dispatch.ForksField: forks}
			}
			f, _ := requestRollback(t, fields)
			consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
			for reported := 0; reported < 2; reported++ {
				sent := f.dispatchedDevices(t)
				if reported < len(sent) {
					f.report(t, consumer, sent[reported])
				}
			}

			plan := rollbackPlan()
			f.bus.mu.Lock()
			defer f.bus.mu.Unlock()
			if len(f.bus.published) != 2 {
				t.Fatalf("published %d dispatches, want one for each planned device the inventory holds", len(f.bus.published))
			}
			for i, evt := range f.bus.published {
				var p wire.DispatchPayload
				if err := json.Unmarshal(evt.Data, &p); err != nil {
					t.Fatal(err)
				}
				if want := topology.RollbackSubject(p.DeviceID); f.bus.topics[i] != want {
					t.Errorf("device %s went to %s, want %s", p.DeviceID, f.bus.topics[i], want)
				}
				want, _ := plan.StepsFor(p.DeviceID)
				if p.Rollback == nil || p.Rollback.Of != "job-x" || p.Rollback.DAGVersion != plan.DAGVersion || !reflect.DeepEqual(p.Rollback.Steps, want) {
					t.Errorf("device %s carries %+v, want its own steps %+v", p.DeviceID, p.Rollback, want)
				}
			}

			job, tasks, err := f.store.Get(t.Context(), f.jobID)
			if err != nil {
				t.Fatal(err)
			}
			byDevice := map[string]dispatch.JobTask{}
			for _, task := range tasks {
				byDevice[task.DeviceID] = task
			}
			if len(byDevice) != 3 || byDevice["id-1"].DeviceID != "" {
				t.Errorf("task rows %+v, want id-0, id-2 and id-9 only", tasks)
			}
			if missing := byDevice["id-9"]; missing.Outcome != dispatch.OutcomeFailed || !strings.Contains(missing.Reason, "no longer in this job's inventory") {
				t.Errorf("the planned device the inventory lost: %+v", missing)
			}
			if job.RollbackOf != "job-x" || job.Rollback == nil || job.State != "completed" || job.FailedCount != 1 {
				t.Errorf("job %+v, want a completed rollback of job-x with one failed device", job)
			}
		})
	}
}

// TestRollbackJob_AnUnreadablePlanFailsTheJob proves a rollback job whose
// stored plan does not decode is failed, and never run as the ordinary job
// its runbook id names.
func TestRollbackJob_AnUnreadablePlanFailsTheJob(t *testing.T) {
	store, client := newTestStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	repo.Devices = append(repo.Devices, capableDevice("id-0", "dev-0", "10.0.0.1"))
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)

	row, err := client.Job.Create().SetRunbookID("pb-1").SetGroupName("routers").SetActor("user@example.com").
		SetRollbackOf("job-x").SetRollback(json.RawMessage(`[1, 2]`)).Save(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"job_id": row.JobID})
	if err := worker.HandleJobRequested(event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	if bus.count() != 0 {
		t.Fatalf("a rollback with an unreadable plan published %d dispatch(es)", bus.count())
	}
	job, _, err := store.Get(t.Context(), row.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || !strings.Contains(job.FailureReason, "cannot be read") {
		t.Errorf("job state %q, reason %q, want failed saying the plan cannot be read", job.State, job.FailureReason)
	}

	// And a rollback with no plan at all is never stored.
	if err := store.Create(t.Context(), &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "a", RollbackOf: "job-x"}); err == nil {
		t.Error("a rollback job with no plan was stored")
	}
}
