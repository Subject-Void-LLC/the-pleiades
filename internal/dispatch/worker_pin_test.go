// Tests that the fan-out pins the compiled runbook a job was prepared from
// (its version and its nodes), which the Controller's facts and journal
// consumers resolve each node's method from, that it is pinned once, and that
// one job runs one graph: a reclaim or a pump that prepares a runbook edited
// since fails the devices it has left rather than dispatching them.
package dispatch_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	entjob "github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// payloadVersion is the pinned graph a published dispatch carries, as
// scheme@digest, empty for none.
func payloadGraph(t *testing.T, evt event.Event) string {
	t.Helper()
	var p wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &p); err != nil {
		t.Fatalf("decode dispatch: %v", err)
	}
	return p.GraphScheme + "@" + p.GraphDigest
}

// pinRunbook is the runbook the pin tests dispatch: one step needing the
// capability capableDevice declares, and a parallel group whose marker the
// graph records.
const pinRunbook = "id: pb-1\ntasks:\n  - name: step\n    ios_backup: {}\n    rollback:\n      - name: undo\n        noop: {}\n  - name: both\n    parallel:\n      - name: a\n        ios_backup: {}\n      - name: b\n        ios_backup: {}\n"

// pinSource is a real runbook directory holding pinRunbook as pb-1, and
// the source over it.
type pinSource struct {
	dir string
	src runbook.Source
}

// newPinSource writes pinRunbook and opens the source.
func newPinSource(t *testing.T) *pinSource {
	t.Helper()
	p := &pinSource{dir: t.TempDir()}
	p.write(t, pinRunbook)
	src, err := runbook.NewDirSource(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	p.src = src
	return p
}

// write replaces pb-1 with body, a minute later than the file it replaces,
// so the source compiles it again.
func (p *pinSource) write(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(p.dir, "pb-1.yaml")
	later := time.Now()
	if info, err := os.Stat(path); err == nil {
		later = info.ModTime().Add(time.Minute)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// edited is pinRunbook with its first step's method changed.
var edited = strings.Replace(pinRunbook, "  - name: step\n    ios_backup: {}", "  - name: step\n    noop: {}", 1)

// rollbackEdited is pinRunbook with a second step added to its first task's
// rollback: list and nothing else: the runbook's version, which leaves
// rollback: lists out, is the same, and its graph is not.
var rollbackEdited = strings.Replace(pinRunbook, "      - name: undo\n        noop: {}\n", "      - name: undo\n        noop: {}\n      - name: undo again\n        noop: {}\n", 1)

// TestWorker_HandleJobRequested_PinsTheRunbookGraph: a job the real worker
// fans out over a real runbook source and a real store records the version
// of the runbook it compiled and what each node runs (its method, its
// rollback: list, the parallel marker), before its device is dispatched.
func TestWorker_HandleJobRequested_PinsTheRunbookGraph(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	p := newPinSource(t)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{capableDevice("dev-1", "router", "10.0.0.9")}}
	bus := newCapturingBus()
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)
	if before, err := store.Lookup(ctx, jobID); err != nil || before.DAGVersion != "" || before.Graph != nil {
		t.Fatalf("a job not yet fanned out = %+v, %v; want no graph", before, err)
	}
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	rb, err := p.src.Get(ctx, "pb-1")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Lookup(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.DAGVersion != rb.Version || job.Graph == nil || job.Graph.Version != rb.Version {
		t.Fatalf("the job pinned %q, %+v; want the compiled runbook's %q", job.DAGVersion, job.Graph, rb.Version)
	}
	if !reflect.DeepEqual(job.Graph.Nodes, rb.Nodes) {
		t.Errorf("pinned nodes %+v, want the compiled runbook's %+v", job.Graph.Nodes, rb.Nodes)
	}
	step := job.Graph.Nodes["tasks[0]"]
	if step.Method != "ios_backup" || !step.Authored || !reflect.DeepEqual(step.Rollback, []string{"noop"}) {
		t.Errorf("tasks[0] pinned as %+v", step)
	}
	if v := payloadGraph(t, bus.last()); v != runbook.GraphScheme+"@"+rb.GraphDigest {
		t.Errorf("the dispatch carried graph %q, want the pinned %s@%s", v, runbook.GraphScheme, rb.GraphDigest)
	}
	var sent wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &sent); err != nil || sent.GraphDigest != rb.GraphDigest || job.Graph.Digest != rb.GraphDigest || sent.PayloadVersion != wire.PinnedGraphPayloadVersion {
		t.Errorf("the dispatch carried digest %q at payload version %d and the job pinned %q (%v), want the compiled runbook's %q at version %d",
			sent.GraphDigest, sent.PayloadVersion, job.Graph.Digest, err, rb.GraphDigest, wire.PinnedGraphPayloadVersion)
	}
	markers := 0
	for _, n := range job.Graph.Nodes {
		if n.Marker {
			markers++
		}
	}
	if markers == 0 {
		t.Errorf("no parallel marker pinned: %+v", job.Graph.Nodes)
	}
}

// TestEntJobStore_PinGraphPinsOnce: a graph is written once, under the
// caller's fence; a second graph keeps the first, a superseded claim is
// fenced, and a graph with no version writes nothing.
func TestEntJobStore_PinGraphPinsOnce(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	job := &dispatch.Job{RunbookID: "pb-1", Actor: "user@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	graphOf := func(version, method string) *dispatch.NodeGraph {
		nodes := map[string]runbook.Node{"tasks[0]": {Method: method}}
		return &dispatch.NodeGraph{Version: version, Scheme: runbook.GraphScheme, Digest: runbook.GraphDigest(nodes), Nodes: nodes}
	}
	one, two := graphOf("v1", "file.copy"), graphOf("v2", "file.remove")
	for _, empty := range []*dispatch.NodeGraph{nil, {}, {Version: "v1"}} {
		if err := store.PinGraph(ctx, job.JobID, fence, empty); err != nil {
			t.Fatalf("an empty graph: %v", err)
		}
	}
	if got, _ := store.Lookup(ctx, job.JobID); got.DAGVersion != "" || got.Graph != nil {
		t.Fatalf("an empty graph wrote %+v", got)
	}
	if err := store.PinGraph(ctx, job.JobID, fence, one); err != nil {
		t.Fatal(err)
	}
	if err := store.PinGraph(ctx, job.JobID, fence, two); err != nil {
		t.Fatalf("a second pin under the same claim: %v", err)
	}
	if got, _ := store.Lookup(ctx, job.JobID); got.DAGVersion != "v1" || !reflect.DeepEqual(got.Graph, one) {
		t.Errorf("the job holds %q, %+v; want the first graph", got.DAGVersion, got.Graph)
	}
	if err := store.PinGraph(ctx, job.JobID, fence+1, two); !errors.Is(err, dispatch.ErrFenced) {
		t.Errorf("a superseded claim: %v, want ErrFenced", err)
	}
	if err := store.PinGraph(ctx, "no-such-job", fence, one); !errors.Is(err, dispatch.ErrJobNotFound) {
		t.Errorf("an unknown job: %v, want ErrJobNotFound", err)
	}
}

// TestWorker_AReclaimRunsOnlyThePinnedGraph: a fan-out that died after
// dispatching one device is reclaimed. Against the runbook it pinned, the
// reclaim dispatches the rest; against the runbook edited since, it
// dispatches none of them and fails each with a reason naming the runbook,
// and the graph pinned stays the first one.
func TestWorker_AReclaimRunsOnlyThePinnedGraph(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   string
		reason string
	}{
		{name: "the same runbook"},
		{name: "an edited runbook", edit: edited, reason: `runbook "pb-1" changed after this job started`},
		{name: "a rollback: list edited", edit: rollbackEdited, reason: `runbook "pb-1" changed after this job started`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store, client := newTestStore(t)
			p := newPinSource(t)
			bus := newCapturingBus()
			repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
				capableDevice("dev-1", "router-1", "10.0.0.1"), capableDevice("dev-2", "router-2", "10.0.0.2")}}
			worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
			evt := requestJob(t, ctx, store, "pb-1", "routers")
			jobID := jobIDFromEvent(t, evt)

			// The first fan-out: claimed, pinned and one device dispatched,
			// then the process died.
			_, fence, err := store.BeginFanOut(ctx, jobID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			rb, err := p.src.Get(ctx, "pb-1")
			if err != nil {
				t.Fatal(err)
			}
			pinned := &dispatch.NodeGraph{Version: rb.Version, Scheme: runbook.GraphScheme, Digest: rb.GraphDigest, Nodes: rb.Nodes}
			if err := store.PinGraph(ctx, jobID, fence, pinned); err != nil {
				t.Fatal(err)
			}
			if err := store.RecordTask(ctx, jobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Job.Update().Where(entjob.JobIDEQ(jobID)).SetUpdatedAt(time.Now().Add(-24 * time.Hour)).Save(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.edit != "" {
				p.write(t, tc.edit)
			}

			if err := worker.HandleJobRequested(evt); err != nil {
				t.Fatal(err)
			}
			job, tasks, err := store.Get(ctx, jobID)
			if err != nil {
				t.Fatal(err)
			}
			byDevice := map[string]dispatch.JobTask{}
			for _, task := range tasks {
				byDevice[task.DeviceID] = task
			}
			second := byDevice["dev-2"]
			switch {
			case tc.reason == "" && (second.Outcome != dispatch.OutcomeDispatched || bus.count() != 1 || payloadGraph(t, bus.last()) != pinned.Scheme+"@"+pinned.Digest):
				t.Errorf("against the same runbook: dev-2 %+v, %d dispatches; want it dispatched carrying the pinned version", second, bus.count())
			case tc.reason != "" && (second.Outcome != dispatch.OutcomeFailed || !strings.Contains(second.Reason, tc.reason) || bus.count() != 0):
				t.Errorf("against an edited runbook: dev-2 %+v, %d dispatches; want it failed naming the change and nothing dispatched", second, bus.count())
			}
			if byDevice["dev-1"].Outcome != dispatch.OutcomeDispatched || len(tasks) != 2 {
				t.Errorf("tasks %+v, want dev-1 left as the first fan-out recorded it", tasks)
			}
			if !reflect.DeepEqual(job.Graph, pinned) {
				t.Errorf("the reclaim moved the pinned graph to %+v", job.Graph)
			}
		})
	}
}

// TestWorker_AReclaimOfAnUnpinnedFanOutPinsNothing: a fan-out that recorded
// a device without pinning a graph (one by an earlier build) is reclaimed
// without pinning one, since its first device ran a graph nobody recorded,
// and its remaining devices are dispatched naming no graph.
func TestWorker_AReclaimOfAnUnpinnedFanOutPinsNothing(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	p := newPinSource(t)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		capableDevice("dev-1", "router-1", "10.0.0.1"), capableDevice("dev-2", "router-2", "10.0.0.2")}}
	bus := newCapturingBus()
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
	evt := requestJob(t, ctx, store, "pb-1", "routers")
	jobID := jobIDFromEvent(t, evt)
	_, fence, err := store.BeginFanOut(ctx, jobID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTask(ctx, jobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Job.Update().Where(entjob.JobIDEQ(jobID)).SetUpdatedAt(time.Now().Add(-24 * time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	if job, err := store.Lookup(ctx, jobID); err != nil || job.DAGVersion != "" || job.Graph != nil {
		t.Errorf("the reclaim pinned %+v, %v; want no graph", job, err)
	}
	// Its dispatch names no graph either: what this fan-out prepared is not
	// what the job's first device ran, so its Runner runs dev-2 as it runs an
	// earlier Controller's dispatch, at payload version 1.
	if bus.count() != 1 {
		t.Fatalf("dispatched %d devices, want dev-2", bus.count())
	}
	var sent wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &sent); err != nil || sent.GraphScheme != "" || sent.GraphDigest != "" || sent.PayloadVersion != 1 {
		t.Errorf("the unpinned job's dispatch carried graph %q@%q at version %d (%v), want none at version 1", sent.GraphScheme, sent.GraphDigest, sent.PayloadVersion, err)
	}
}

// TestWorker_APumpRunsOnlyThePinnedGraph: a windowed job's queued devices,
// pumped after its runbook was edited, are failed with the reason, never
// dispatched against the new version.
func TestWorker_APumpRunsOnlyThePinnedGraph(t *testing.T) {
	for name, edit := range map[string]string{"an edited runbook": edited, "a rollback: list edited": rollbackEdited} {
		t.Run(name, func(t *testing.T) { pumpAfterEdit(t, edit) })
	}
}

// pumpAfterEdit fans out a windowed job of three devices, edits its runbook
// to edit, and reports the first device's result: the pump fails the other
// two, naming the change, and dispatches neither.
func pumpAfterEdit(t *testing.T, edit string) {
	t.Helper()
	ctx := t.Context()
	store := newTestJobStore(t)
	p := newPinSource(t)
	bus := newCapturingBus()
	repo := &fakeRepository{}
	for i := 0; i < 3; i++ {
		repo.Devices = append(repo.Devices, capableDevice(fmt.Sprintf("id-%d", i), fmt.Sprintf("dev-%d", i), "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
	evt := requestJobWithLaunchFields(t, ctx, store, "pb-1", "routers", launch.Fields{dispatch.ForksField: 1}, nil)
	jobID := jobIDFromEvent(t, evt)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	if bus.count() != 1 {
		t.Fatalf("the fan-out dispatched %d devices, want the one its window holds", bus.count())
	}
	p.write(t, edit)
	consumer := dispatch.NewResultConsumer(store, quietLogger(), dispatch.WithResultPump(worker.Pump))
	if err := consumer.Handle(resultEvent(t, jobID, "id-0", "completed", "")); err != nil {
		t.Fatal(err)
	}
	if bus.count() != 1 {
		t.Errorf("the pump dispatched %d devices in all against an edited runbook, want only the first", bus.count())
	}
	job, tasks, err := store.Get(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, task := range tasks {
		if task.Outcome == dispatch.OutcomeFailed && strings.Contains(task.Reason, `runbook "pb-1" changed after this job started`) {
			failed++
		}
	}
	if failed != 2 || job.State != "completed" {
		t.Errorf("tasks %+v in a %s job, want the two queued devices failed naming the change and the job completed", tasks, job.State)
	}
}

// TestWorker_APumpCarriesThePinnedGraph: every device of a windowed job, the
// fan-out's and the pump's, is dispatched carrying the version its job
// pinned, which its Runner holds its own copy of the runbook to.
func TestWorker_APumpCarriesThePinnedGraph(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	p := newPinSource(t)
	bus := newCapturingBus()
	repo := &fakeRepository{}
	for i := 0; i < 2; i++ {
		repo.Devices = append(repo.Devices, capableDevice(fmt.Sprintf("id-%d", i), fmt.Sprintf("dev-%d", i), "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
	evt := requestJobWithLaunchFields(t, ctx, store, "pb-1", "routers", launch.Fields{dispatch.ForksField: 1}, nil)
	jobID := jobIDFromEvent(t, evt)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	consumer := dispatch.NewResultConsumer(store, quietLogger(), dispatch.WithResultPump(worker.Pump))
	if err := consumer.Handle(resultEvent(t, jobID, "id-0", "completed", "")); err != nil {
		t.Fatal(err)
	}
	job, err := store.Lookup(ctx, jobID)
	if err != nil || job.DAGVersion == "" {
		t.Fatalf("job %+v, %v; want a pinned graph", job, err)
	}
	bus.mu.Lock()
	published := append([]event.Event(nil), bus.published...)
	bus.mu.Unlock()
	if len(published) != 2 {
		t.Fatalf("dispatched %d devices, want both", len(published))
	}
	for i, evt := range published {
		if v := payloadGraph(t, evt); v != job.Graph.Scheme+"@"+job.Graph.Digest {
			t.Errorf("dispatch %d carried graph %q, want the pinned %s@%s", i, v, job.Graph.Scheme, job.Graph.Digest)
		}
	}
}

// TestNodeGraph_ARollbackListEditKeepsTheVersionAndChangesTheGraph is the
// control for the rollback: list cases above: the edit leaves the runbook's
// version as it was, so only the graph's digest tells the two apart.
func TestNodeGraph_ARollbackListEditKeepsTheVersionAndChangesTheGraph(t *testing.T) {
	ctx := t.Context()
	p := newPinSource(t)
	before, err := p.src.Get(ctx, "pb-1")
	if err != nil {
		t.Fatal(err)
	}
	p.write(t, rollbackEdited)
	after, err := p.src.Get(ctx, "pb-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version {
		t.Fatalf("a rollback: list edit moved the version from %q to %q, so the version alone would catch it", before.Version, after.Version)
	}
	if before.GraphDigest == after.GraphDigest || after.GraphDigest != runbook.GraphDigest(after.Nodes) {
		t.Errorf("digests %q and %q, want them to differ, each its own nodes'", before.GraphDigest, after.GraphDigest)
	}
}

// rollbackJob creates a rollback job of pb-1 undoing job-0 on dev-1 and
// dev-2, and returns it with the event that requests its fan-out.
func rollbackJob(t *testing.T, store dispatch.JobStore, rb *runbook.Runbook) (*dispatch.Job, event.Event) {
	t.Helper()
	step := wire.RollbackStep{Node: "tasks[0]", Emitter: "ios_backup", Method: "noop", Name: "undo", Source: "authored"}
	plan := &dispatch.RollbackPlan{DAGVersion: rb.Version, Devices: []dispatch.RollbackDevice{
		{DeviceID: "dev-1", DeviceName: "router-1", Steps: []wire.RollbackStep{step}},
		{DeviceID: "dev-2", DeviceName: "router-2", Steps: []wire.RollbackStep{step}}}}
	job := &dispatch.Job{RunbookID: "pb-1", Actor: "user@example.com", RollbackOf: "job-0", Rollback: plan}
	if err := store.Create(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	return job, event.Event{ID: "requested", Type: "job.requested", Data: []byte(`{"job_id":"` + job.JobID + `"}`)}
}

// checkRollbackPayload holds a rollback dispatch to its plan's version, with
// no pinned graph beside it.
func checkRollbackPayload(t *testing.T, evt event.Event, version string) {
	t.Helper()
	var payload wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Rollback == nil || payload.Rollback.DAGVersion != version || payload.GraphScheme != "" || payload.GraphDigest != "" || payload.PayloadVersion != 1 {
		t.Errorf("the rollback dispatch carried rollback %+v, graph %q@%q at payload version %d; want the plan's version, no pinned graph, version 1",
			payload.Rollback, payload.GraphScheme, payload.GraphDigest, payload.PayloadVersion)
	}
}

// TestWorker_ARollbackJobPinsNoGraph: the real worker's fan-out of a
// rollback job pins no graph, since the job runs its plan's steps held to
// the version it undoes, and each dispatch carries that version alone.
func TestWorker_ARollbackJobPinsNoGraph(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	p := newPinSource(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		capableDevice("dev-1", "router-1", "10.0.0.1"), capableDevice("dev-2", "router-2", "10.0.0.2")}}
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
	rb, err := p.src.Get(ctx, "pb-1")
	if err != nil {
		t.Fatal(err)
	}
	job, evt := rollbackJob(t, store, rb)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Lookup(ctx, job.JobID); got.Graph != nil || got.DAGVersion != "" {
		t.Errorf("the rollback job pinned %q, %+v", got.DAGVersion, got.Graph)
	}
	if bus.count() != 2 {
		t.Fatalf("dispatched %d devices, want both the plan names", bus.count())
	}
	checkRollbackPayload(t, bus.last(), rb.Version)
}

// TestWorker_ARollbackJobsReclaimIsHeldToItsPlan: a rollback job's fan-out
// that died after one device, reclaimed after its runbook was edited, still
// dispatches the device its plan names that it had not reached, carrying the
// plan's version: it is held to its plan, never to a graph.
func TestWorker_ARollbackJobsReclaimIsHeldToItsPlan(t *testing.T) {
	ctx := t.Context()
	store, client := newTestStore(t)
	p := newPinSource(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		capableDevice("dev-1", "router-1", "10.0.0.1"), capableDevice("dev-2", "router-2", "10.0.0.2")}}
	worker := dispatch.NewWorker(store, repo, p.src, bus, nil)
	rb, err := p.src.Get(ctx, "pb-1")
	if err != nil {
		t.Fatal(err)
	}
	job, evt := rollbackJob(t, store, rb)
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Job.Update().Where(entjob.JobIDEQ(job.JobID)).SetUpdatedAt(time.Now().Add(-24 * time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	p.write(t, rollbackEdited)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var second dispatch.JobTask
	for _, task := range tasks {
		if task.DeviceID == "dev-2" {
			second = task
		}
	}
	if second.Outcome != dispatch.OutcomeDispatched || bus.count() != 1 {
		t.Fatalf("dev-2 %+v, %d dispatches; want it dispatched from the plan", second, bus.count())
	}
	checkRollbackPayload(t, bus.last(), rb.Version)
}
