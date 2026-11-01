// Tests of serial: on the Controller: a job's devices dispatched a batch at a
// time in name order, the next batch only once every device of the one
// before has reported, and a failed batch stopping the rollout.
package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// newSerialFixture fans out a job running a runbook with serial: over five
// devices whose names run opposite to their ids (id-0 is n-e, id-4 is n-a),
// so a batch follows names, not ids. forks is the launch's window, 0 for
// none.
func newSerialFixture(t *testing.T, serial string, forks int) *windowFixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rolling.yaml"), []byte("id: rolling\nserial: "+serial+"\ntasks:\n  - name: step\n    fqcn: ios_backup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	for i, name := range []string{"n-e", "n-d", "n-c", "n-b", "n-a"} {
		repo.Devices = append(repo.Devices, capableDevice("id-"+string(rune('0'+i)), name, "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, src, bus, nil)
	fields := launch.Fields{}
	if forks > 0 {
		fields[dispatch.ForksField] = forks
	}
	evt := requestJobWithLaunchFields(t, t.Context(), store, "rolling", "routers", fields, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	return &windowFixture{store: store, bus: bus, repo: repo, worker: worker, jobID: jobIDFromEvent(t, evt)}
}

// TestSerial_BatchesRunOneAfterAnotherInNameOrder: serial: 2 over five
// devices dispatches n-a and n-b first, holds the next batch while one of
// them still runs, starts n-c and n-d once both have reported, then n-e, and
// the job ends complete.
func TestSerial_BatchesRunOneAfterAnotherInNameOrder(t *testing.T) {
	f := newSerialFixture(t, "2", 0)
	consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
	sent := func() []string { return f.dispatchedDevices(t) }

	if got := sent(); !slices.Equal(got, []string{"id-4", "id-3"}) {
		t.Fatalf("the fan-out dispatched %v, want the first batch by name: id-4 (n-a), id-3 (n-b)", got)
	}
	f.report(t, consumer, "id-4")
	if got := sent(); len(got) != 2 {
		t.Fatalf("the next batch started while n-b still ran: %v", got)
	}
	f.report(t, consumer, "id-3")
	if got := sent(); !slices.Equal(got[2:], []string{"id-2", "id-1"}) {
		t.Fatalf("after the first batch, dispatched %v, want n-c and n-d next", got)
	}
	f.report(t, consumer, "id-1")
	f.report(t, consumer, "id-2")
	if got := sent(); !slices.Equal(got[4:], []string{"id-0"}) {
		t.Fatalf("after the second batch, dispatched %v, want n-e last", got)
	}
	f.report(t, consumer, "id-0")
	if _, job := f.outcomes(t); job.State != "completed" || job.DispatchedCount != 5 {
		t.Errorf("job state %q dispatched %d, want completed with 5", job.State, job.DispatchedCount)
	}
}

// TestSerial_AFailedBatchStopsTheRollout: a failure in the first batch lets
// that batch finish, then skips every later device naming the batch, and the
// job ends.
func TestSerial_AFailedBatchStopsTheRollout(t *testing.T) {
	f := newSerialFixture(t, "2", 0)
	consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
	if err := consumer.Handle(resultEvent(t, f.jobID, "id-4", "failed", "the step failed")); err != nil {
		t.Fatal(err)
	}
	if got := f.dispatchedDevices(t); len(got) != 2 {
		t.Fatalf("a batch with a failure stopped before its own devices finished, or the next started: %v", got)
	}
	f.report(t, consumer, "id-3")
	if got := f.dispatchedDevices(t); len(got) != 2 {
		t.Errorf("the rollout went on after a failed batch: %v", got)
	}
	job, tasks, err := f.store.Get(t.Context(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	skipped := 0
	for _, task := range tasks {
		if task.Outcome == dispatch.OutcomeSkipped {
			skipped++
			if !strings.Contains(task.Reason, "serial: batch 1 had a failure") {
				t.Errorf("%s skipped with %q", task.DeviceName, task.Reason)
			}
		}
	}
	if skipped != 3 {
		t.Errorf("%d devices skipped, want the 3 of the later batches", skipped)
	}
	if job.State == "running" {
		t.Errorf("the job still reads running after the rollout stopped")
	}
}

// TestSerial_ForksStillBoundsABatch: with forks 1, a batch of two runs one
// device at a time, and the next batch still waits for both.
func TestSerial_ForksStillBoundsABatch(t *testing.T) {
	f := newSerialFixture(t, "2", 1)
	consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
	if got := f.dispatchedDevices(t); !slices.Equal(got, []string{"id-4"}) {
		t.Fatalf("dispatched %v, want only n-a under forks 1", got)
	}
	f.report(t, consumer, "id-4")
	if got := f.dispatchedDevices(t); !slices.Equal(got, []string{"id-4", "id-3"}) {
		t.Fatalf("dispatched %v, want n-b next, the rest of its batch", got)
	}
	f.report(t, consumer, "id-3")
	if got := f.dispatchedDevices(t); !slices.Equal(got[2:], []string{"id-2"}) {
		t.Fatalf("dispatched %v, want n-c to open the second batch", got)
	}
}

// TestSerial_BatchesAreAssignedOnce: assigning again after the first batch
// has started changes nothing, so a fan-out run twice cannot reshuffle a
// rollout already under way.
func TestSerial_BatchesAreAssignedOnce(t *testing.T) {
	f := newSerialFixture(t, "2", 0)
	before, err := f.store.SerialGate(t.Context(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignBatches(t.Context(), f.jobID, nil); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.SerialGate(t.Context(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Batched || before != after || before.Current != 0 || before.Size != 2 {
		t.Errorf("gate before %+v, after %+v", before, after)
	}
}

// failingAssign is a JobStore whose first AssignBatches fails, standing in
// for a database that stumbled between a serial job's fan-out and its batches.
type failingAssign struct {
	dispatch.JobStore
	failed bool
}

// AssignBatches fails once, then assigns as the real store does.
func (s *failingAssign) AssignBatches(ctx context.Context, jobID string, sizes engine.SerialList) error {
	if !s.failed {
		s.failed = true
		return errors.New("the database stumbled")
	}
	return s.JobStore.AssignBatches(ctx, jobID, sizes)
}

// newSerialFixtureOn is newSerialFixture over store and runbook source dir.
func newSerialFixtureOn(t *testing.T, store dispatch.JobStore, dir string, forks int) *windowFixture {
	t.Helper()
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := newCapturingBus()
	repo := &namedRepository{}
	for i, name := range []string{"n-e", "n-d", "n-c", "n-b", "n-a"} {
		repo.Devices = append(repo.Devices, capableDevice("id-"+string(rune('0'+i)), name, "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, src, bus, nil)
	fields := launch.Fields{}
	if forks > 0 {
		fields[dispatch.ForksField] = forks
	}
	evt := requestJobWithLaunchFields(t, t.Context(), store, "rolling", "routers", fields, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	return &windowFixture{store: store, bus: bus, repo: repo, worker: worker, jobID: jobIDFromEvent(t, evt)}
}

// serialRunbookDir writes the serial: 2 runbook into a fresh directory.
func serialRunbookDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rolling.yaml"), []byte("id: rolling\nserial: 2\ntasks:\n  - name: step\n    fqcn: ios_backup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSerial_AFailedAssignmentIsRepairedWhateverTheForks: when the fan-out
// could not assign a serial job's batches, the pump assigns them and runs
// the first batch only, with forks set as much as without it, rather than
// running every device unbatched.
func TestSerial_AFailedAssignmentIsRepairedWhateverTheForks(t *testing.T) {
	for _, forks := range []int{0, 2} {
		store := &failingAssign{JobStore: newTestJobStore(t)}
		f := newSerialFixtureOn(t, store, serialRunbookDir(t), forks)
		if !store.failed {
			t.Fatal("the fan-out never tried to assign batches")
		}
		if got := f.dispatchedDevices(t); !slices.Equal(got, []string{"id-4", "id-3"}) {
			t.Fatalf("forks %d: after a failed assignment the pump dispatched %v, want the first batch by name: id-4 (n-a), id-3 (n-b)", forks, got)
		}
		gate, err := f.store.SerialGate(t.Context(), f.jobID)
		if err != nil || !gate.Batched {
			t.Fatalf("forks %d: gate %+v, %v; want the batches assigned", forks, gate, err)
		}
	}
}

// TestSerial_ADefinitionGoneBeforeItsBatchesFailsTheQueuedDevices: a serial
// job whose batches were never assigned and whose runbook went away fails
// its queued devices with the reason and ends, rather than sitting in
// running while its pump finds nothing it may do.
func TestSerial_ADefinitionGoneBeforeItsBatchesFailsTheQueuedDevices(t *testing.T) {
	dir := serialRunbookDir(t)
	store := &failingAssign{JobStore: newTestJobStore(t)}
	// The fan-out's pump repairs at once, so this fixture fails the
	// assignment twice: once in the fan-out and once in its first pump.
	twice := &failingAssign{JobStore: store}
	f := newSerialFixtureOn(t, twice, dir, 0)
	if got := f.dispatchedDevices(t); len(got) != 0 {
		t.Fatalf("dispatched %v before the batches existed", got)
	}
	if err := os.Remove(filepath.Join(dir, "rolling.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Pump(t.Context(), f.jobID); err != nil {
		t.Fatalf("Pump: %v", err)
	}
	job, tasks, err := f.store.Get(t.Context(), f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Outcome != dispatch.OutcomeFailed {
			t.Errorf("%s is %q, want failed with the definition's reason", task.DeviceName, task.Outcome)
		}
	}
	if job.State == "running" {
		t.Error("the job still reads running with nothing it can run")
	}
}
