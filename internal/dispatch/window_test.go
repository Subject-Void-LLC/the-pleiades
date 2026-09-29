// Tests for the forks window (window.go): a windowed job queues its
// admitted devices and dispatches at most forks of them at a time, the
// next one as each result lands.
//
// Everything here runs the real Worker, the real ent store over SQLite and
// the real ResultConsumer; the one stand-in is the inventory, which has to
// answer GetByName for the pump's second look at a device. The claim that
// concurrent pumps on two databases never exceed forks is proved against
// SQLite and PostgreSQL both, in internal/ent's window conformance test,
// because the bound is the schema's own unique index.
package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// namedRepository is fakeRepository with a GetByName that answers from
// Devices, guarded so a test can change the inventory while a job waits.
type namedRepository struct {
	fakeRepository
	mu sync.Mutex
}

// GetByName returns the device named name, or inventory.ErrItemNotFound.
func (r *namedRepository) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.Devices {
		if d.Name() == name {
			return d, nil
		}
	}
	return nil, fmt.Errorf("no device %q: %w", name, inventory.ErrItemNotFound)
}

// remove takes the device named name out of the inventory.
func (r *namedRepository) remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.Devices[:0]
	for _, d := range r.Devices {
		if d.Name() != name {
			kept = append(kept, d)
		}
	}
	r.Devices = kept
}

// windowFixture is a windowed job over n capable devices, fanned out.
type windowFixture struct {
	store  dispatch.JobStore
	bus    *capturingBus
	repo   *namedRepository
	worker *dispatch.Worker
	jobID  string
}

// newWindowFixture fans out a job with forks set over n devices named
// dev-0 to dev-(n-1).
func newWindowFixture(t testing.TB, n, forks int) *windowFixture {
	t.Helper()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &namedRepository{}
	for i := 0; i < n; i++ {
		repo.Devices = append(repo.Devices, capableDevice(fmt.Sprintf("id-%d", i), fmt.Sprintf("dev-%d", i), "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)
	evt := requestJobWithLaunchFields(t, t.Context(), store, "pb-1", "routers", launch.Fields{dispatch.ForksField: forks}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	return &windowFixture{store: store, bus: bus, repo: repo, worker: worker, jobID: jobIDFromEvent(t, evt)}
}

// dispatchedDevices returns the device ids the bus has carried a dispatch
// for, in publish order.
func (f *windowFixture) dispatchedDevices(t testing.TB) []string {
	t.Helper()
	f.bus.mu.Lock()
	defer f.bus.mu.Unlock()
	var ids []string
	for _, evt := range f.bus.published {
		var p wire.DispatchPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			t.Fatalf("decode dispatch: %v", err)
		}
		ids = append(ids, p.DeviceID)
	}
	return ids
}

// outcomes counts the job's task rows by outcome.
func (f *windowFixture) outcomes(t *testing.T) (map[dispatch.Outcome]int, *dispatch.Job) {
	t.Helper()
	job, tasks, err := f.store.Get(t.Context(), f.jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	counts := map[dispatch.Outcome]int{}
	for _, task := range tasks {
		counts[task.Outcome]++
	}
	return counts, job
}

// report delivers a completed result for deviceID through consumer.
func (f *windowFixture) report(t testing.TB, consumer *dispatch.ResultConsumer, deviceID string) {
	t.Helper()
	if err := consumer.Handle(resultEvent(t, f.jobID, deviceID, "completed", "")); err != nil {
		t.Fatalf("Handle(result %s): %v", deviceID, err)
	}
}

// TestWindow_FanOutDispatchesOnlyForksAndQueuesTheRest is the whole idea:
// five devices, forks two, and only two dispatches leave the Controller.
func TestWindow_FanOutDispatchesOnlyForksAndQueuesTheRest(t *testing.T) {
	f := newWindowFixture(t, 5, 2)

	if got := f.dispatchedDevices(t); len(got) != 2 || got[0] != "id-0" || got[1] != "id-1" {
		t.Fatalf("dispatched %v, want the first two devices only", got)
	}
	counts, job := f.outcomes(t)
	if counts[dispatch.OutcomeDispatched] != 2 || counts[dispatch.OutcomeQueued] != 3 {
		t.Errorf("outcomes %v, want 2 dispatched and 3 queued", counts)
	}
	if job.State != "running" || job.DispatchedCount != 2 {
		t.Errorf("job state %q dispatched %d, want running with 2 counted", job.State, job.DispatchedCount)
	}
}

// TestWindow_EachResultStartsTheNextDeviceUntilTheJobEnds drives the
// window through the real result consumer, one result at a time, and
// holds the number in flight at forks the whole way.
func TestWindow_EachResultStartsTheNextDeviceUntilTheJobEnds(t *testing.T) {
	f := newWindowFixture(t, 5, 2)
	consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))

	reported := 0
	for reported < 5 {
		sent := f.dispatchedDevices(t)
		if inFlight := len(sent) - reported; inFlight > 2 || inFlight < 1 {
			t.Fatalf("after %d results, %d devices are in flight, want 1 or 2", reported, inFlight)
		}
		f.report(t, consumer, sent[reported])
		reported++
	}

	if got := f.dispatchedDevices(t); len(got) != 5 {
		t.Fatalf("dispatched %d devices in all, want every one of 5: %v", len(got), got)
	}
	counts, job := f.outcomes(t)
	if counts[dispatch.OutcomeDispatched] != 5 || counts[dispatch.OutcomeQueued] != 0 {
		t.Errorf("outcomes %v, want all 5 dispatched", counts)
	}
	if job.State != "completed" || job.DispatchedCount != 5 {
		t.Errorf("job state %q dispatched %d, want completed with 5", job.State, job.DispatchedCount)
	}
}

// TestWindow_AJobIsNotCompleteWhileDevicesWait proves a drained window
// does not end the job: the queued devices are work still to come.
func TestWindow_AJobIsNotCompleteWhileDevicesWait(t *testing.T) {
	f := newWindowFixture(t, 3, 1)
	// No pump: the result frees the slot and nothing takes it.
	consumer := dispatch.NewResultConsumer(f.store, quietLogger())
	f.report(t, consumer, "id-0")

	_, job := f.outcomes(t)
	if job.State != "running" {
		t.Fatalf("job state %q after its only running device reported, want running: two devices still wait", job.State)
	}

	// The leader's sweep is the backstop for exactly this: a result whose
	// pump never ran.
	dispatch.NewReaper(f.store, f.bus, 0, dispatch.WithReaperPump(f.worker.Pump)).Sweep(t.Context())
	if got := f.dispatchedDevices(t); len(got) != 2 || got[1] != "id-1" {
		t.Errorf("after the sweep dispatched %v, want the next device started", got)
	}
}

// TestWindow_ADeviceIsAdmittedAgainWhenItsTurnComes proves the pump asks
// about a device as it is when its turn comes: one removed from the
// inventory, and one quarantined, while they waited.
func TestWindow_ADeviceIsAdmittedAgainWhenItsTurnComes(t *testing.T) {
	f := newWindowFixture(t, 4, 1)
	f.repo.remove("dev-1")
	f.repo.mu.Lock()
	for _, d := range f.repo.Devices {
		if d.Name() == "dev-2" {
			d.(*inventorytest.Stub).StubState = pkginventory.StateQuarantined
		}
	}
	f.repo.mu.Unlock()

	consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
	f.report(t, consumer, "id-0")

	if got := f.dispatchedDevices(t); len(got) != 2 || got[1] != "id-3" {
		t.Fatalf("dispatched %v, want dev-3 next: dev-1 is gone and dev-2 is quarantined", got)
	}
	_, tasks, err := f.store.Get(t.Context(), f.jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, task := range tasks {
		switch task.DeviceID {
		case "id-1":
			if task.Outcome != dispatch.OutcomeFailed {
				t.Errorf("removed device %+v, want failed", task)
			}
		case "id-2":
			if task.Outcome != dispatch.OutcomeSkipped {
				t.Errorf("quarantined device %+v, want skipped", task)
			}
		}
	}
	_, job := f.outcomes(t)
	if job.FailedCount != 1 || job.SkippedCount != 1 || job.DispatchedCount != 2 {
		t.Errorf("tallies dispatched %d skipped %d failed %d, want 2, 1 and 1", job.DispatchedCount, job.SkippedCount, job.FailedCount)
	}
}

// TestWindow_CancelSkipsEveryQueuedDevice proves a canceled windowed job
// never dispatches another device and says why the rest never ran.
func TestWindow_CancelSkipsEveryQueuedDevice(t *testing.T) {
	f := newWindowFixture(t, 4, 1)
	if err := f.store.Cancel(t.Context(), f.jobID, "operator"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := f.worker.Pump(t.Context(), f.jobID); err != nil {
		t.Fatalf("Pump after cancel: %v", err)
	}
	if got := f.dispatchedDevices(t); len(got) != 1 {
		t.Errorf("dispatched %v after a cancel, want only the one sent before it", got)
	}
	counts, job := f.outcomes(t)
	if counts[dispatch.OutcomeQueued] != 0 || counts[dispatch.OutcomeSkipped] != 3 || job.SkippedCount != 3 {
		t.Errorf("outcomes %v skipped tally %d, want the 3 waiting devices skipped", counts, job.SkippedCount)
	}
}

// TestWindow_APublishThatFailsFreesTheSlotForTheNextDevice proves a
// device whose dispatch could not be published is failed and releases its
// place rather than holding it forever.
func TestWindow_APublishThatFailsFreesTheSlotForTheNextDevice(t *testing.T) {
	store := newTestJobStore(t)
	bus := &failingOnceBus{capturingBus: newCapturingBus(), failDevice: "id-1"}
	repo := &namedRepository{}
	for i := 0; i < 3; i++ {
		repo.Devices = append(repo.Devices, capableDevice(fmt.Sprintf("id-%d", i), fmt.Sprintf("dev-%d", i), "10.0.0.1"))
	}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil)
	evt := requestJobWithLaunchFields(t, t.Context(), store, "pb-1", "routers", launch.Fields{dispatch.ForksField: 2}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested: %v", err)
	}
	jobID := jobIDFromEvent(t, evt)

	job, tasks, err := store.Get(t.Context(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	outcome := map[string]dispatch.Outcome{}
	for _, task := range tasks {
		outcome[task.DeviceID] = task.Outcome
	}
	if outcome["id-0"] != dispatch.OutcomeDispatched || outcome["id-1"] != dispatch.OutcomeFailed || outcome["id-2"] != dispatch.OutcomeDispatched {
		t.Errorf("outcomes %v, want id-1 failed and id-2 taking its place", outcome)
	}
	if job.DispatchedCount != 2 || job.FailedCount != 1 {
		t.Errorf("tallies dispatched %d failed %d, want 2 and 1", job.DispatchedCount, job.FailedCount)
	}
}

// failingOnceBus refuses the first dispatch naming failDevice.
type failingOnceBus struct {
	*capturingBus
	failDevice string
	failed     bool
}

// Publish fails the first dispatch for failDevice and passes everything
// else to the capturing bus.
func (b *failingOnceBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	var p wire.DispatchPayload
	if json.Unmarshal(evt.Data, &p) == nil && p.DeviceID == b.failDevice && !b.failed {
		b.failed = true
		return errors.New("the bus refused this dispatch")
	}
	return b.capturingBus.Publish(ctx, topic, evt)
}

// TestWindow_AnUnwindowedJobIsUnchanged is the control: no forks field,
// and every device is dispatched during the fan-out exactly as before.
func TestWindow_AnUnwindowedJobIsUnchanged(t *testing.T) {
	f := newWindowFixture(t, 4, 0)
	if got := f.dispatchedDevices(t); len(got) != 4 {
		t.Fatalf("dispatched %v, want every device during the fan-out", got)
	}
	counts, _ := f.outcomes(t)
	if counts[dispatch.OutcomeQueued] != 0 {
		t.Errorf("an unwindowed job queued %d devices", counts[dispatch.OutcomeQueued])
	}
	if err := f.worker.Pump(t.Context(), f.jobID); err != nil {
		t.Errorf("Pump on an unwindowed job: %v", err)
	}
	if got := f.dispatchedDevices(t); len(got) != 4 {
		t.Errorf("a pump on an unwindowed job dispatched again: %v", got)
	}
}

// TestWindow_ConcurrentPumpsInOneProcessNeverExceedForks races many pumps
// of one job, the in-process half of the bound (the database half is
// internal/ent's window conformance test).
func TestWindow_ConcurrentPumpsInOneProcessNeverExceedForks(t *testing.T) {
	f := newWindowFixture(t, 12, 3)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A failure here would be a lock timeout on the shared
			// in-memory database; the assertion below is the point.
			_ = f.worker.Pump(context.Background(), f.jobID)
		}()
	}
	wg.Wait()
	if got := f.dispatchedDevices(t); len(got) != 3 {
		t.Fatalf("eight concurrent pumps dispatched %d devices, want forks (3): %v", len(got), got)
	}
	held, err := f.store.HeldSlots(t.Context(), f.jobID)
	if err != nil {
		t.Fatalf("HeldSlots: %v", err)
	}
	if len(held) != 3 {
		t.Errorf("held slots %v, want three", held)
	}
}

// TestDispatchState_SaysWhetherARunnerWasHandedTheDevice covers the answer
// the run journal's consumer stores a batch on: a device the fan-out sent
// is sent, one it skipped or still holds in a window is not, and a job or
// device the record has no row for is unrecorded.
func TestDispatchState_SaysWhetherARunnerWasHandedTheDevice(t *testing.T) {
	f := newWindowFixture(t, 3, 1)
	ctx := t.Context()
	for device, want := range map[string]dispatch.DispatchState{
		"id-0":       dispatch.DispatchSent,
		"id-1":       dispatch.DispatchNotSent,
		"no-such-id": dispatch.DispatchUnrecorded,
	} {
		if got, err := f.store.DispatchState(ctx, f.jobID, device); err != nil || got != want {
			t.Errorf("DispatchState(%s) = %v, %v; want %v", device, got, err, want)
		}
	}
	if got, err := f.store.DispatchState(ctx, "no-such-job", "id-0"); err != nil || got != dispatch.DispatchUnrecorded {
		t.Errorf("DispatchState(no such job) = %v, %v; want unrecorded", got, err)
	}
}

// BenchmarkWindow_PumpPerResult measures what a forks window adds to each
// result: recording it, and the pump that claims the freed slot and
// dispatches the next device, through the real ent store. Each iteration
// drains a 200-device job with forks 10 one result at a time; the time is
// reported per result.
func BenchmarkWindow_PumpPerResult(b *testing.B) {
	const devices, forks = 200, 10
	for b.Loop() {
		b.StopTimer()
		f := newWindowFixture(b, devices, forks)
		consumer := dispatch.NewResultConsumer(f.store, quietLogger(), dispatch.WithResultPump(f.worker.Pump))
		b.StartTimer()
		for reported := 0; reported < devices; reported++ {
			f.report(b, consumer, f.dispatchedDevices(b)[reported])
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*devices), "ns/result")
}
