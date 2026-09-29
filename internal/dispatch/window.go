// Package dispatch: the forks window, which limits how many of one job's
// devices run at once (Phase 110).
//
// A job launched with forks set does not publish every device's dispatch
// during its fan-out. The fan-out admits each device exactly as it always
// has and records the admitted ones as queued; the pump then dispatches
// them, at most forks at a time. It runs at the end of the fan-out, after
// each device's result lands (which frees that device's place), and from
// the leader's sweep, which catches a pump that should have followed a
// result and never ran.
//
// Several Controller replicas may pump one job at once, since results land
// on whichever replica pulled them. Nothing here counts and hopes: a
// dispatched device holds a numbered slot, 0 up to forks less one, and the
// database's unique index on (job, slot) refuses a second claim on a slot
// that is taken (ClaimQueued). A row is claimed before its dispatch is
// published, so a result never arrives for a row that does not yet say
// dispatched.
//
// What the window cannot do, stated here as in docs/09: a device whose
// result is lost holds its slot, so the job's remaining devices wait for
// it, and a job already never completes in that case; and a Runner that
// reports a failure and then retries the dispatch has freed its slot on
// the first report, so the retries run beside the next device. Phases 103b
// (the lost-result sweep) and 103c (one result per dispatch) close those.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// ForksField is the launch field that sets a job's window: how many of its
// devices run at once. Absent or zero means no window, every admitted
// device dispatched during the fan-out, which is how every job ran before
// the window existed.
const ForksField = "forks"

// windowOf returns job's forks window, zero for none.
func windowOf(job *Job) int {
	if n := job.Fields.Int(ForksField); n > 0 {
		return n
	}
	return 0
}

// freeSlots returns the slots 0 to forks-1 that held does not name, lowest
// first.
func freeSlots(forks int, held []int) []int {
	taken := make(map[int]bool, len(held))
	for _, s := range held {
		taken[s] = true
	}
	free := make([]int, 0, forks)
	for s := 0; s < forks; s++ {
		if !taken[s] {
			free = append(free, s)
		}
	}
	return free
}

// pumpDefinition is what a pump needs once per call and only if it
// dispatches something: the job's prepared definition and its rendered
// credentials. reason is set, and the rest empty, when either could not be
// had; every device the pump reaches is then failed with it.
type pumpDefinition struct {
	prepared PreparedDefinition
	injected credtype.Artifact
	reason   string
}

// Pump dispatches as many of jobID's queued devices as its forks window
// has room for, and ends the job if nothing is left to run. It is safe to
// call for any job, at any time, from any replica: an unwindowed job, a
// job that is not running and a job with no room all return at once.
func (w *Worker) Pump(ctx context.Context, jobID string) error {
	job, err := w.store.Lookup(ctx, jobID)
	if err != nil {
		if errors.Is(err, ErrJobNotFound) {
			return nil
		}
		return err
	}
	forks := windowOf(job)
	if forks == 0 {
		return nil
	}
	held, err := w.store.HeldSlots(ctx, jobID)
	if err != nil {
		return err
	}
	free := freeSlots(forks, held)

	var def *pumpDefinition
	for len(free) > 0 {
		queued, err := w.store.QueuedTasks(ctx, jobID, len(free))
		if err != nil {
			return err
		}
		if len(queued) == 0 {
			break
		}
		moved := false
		for _, task := range queued {
			if len(free) == 0 {
				break
			}
			if def == nil {
				def = w.pumpDefinitionFor(ctx, job)
			}
			var did bool
			if free, did, err = w.pumpOne(ctx, job, task, def, free); err != nil {
				return err
			}
			moved = moved || did
		}
		// A batch in which this pump moved nothing means every row was
		// another pump's, or the job stopped with rows still reading
		// queued; asking again would return the same rows forever.
		if !moved {
			break
		}
	}
	return w.store.CompleteIfDone(ctx, jobID)
}

// pumpDefinitionFor prepares job's definition and renders its credentials,
// as the fan-out did. A failure is not the pump's to retry: the definition
// or a credential went away while devices waited, and each of them is
// failed with the same sanitised sentence the fan-out would have recorded.
func (w *Worker) pumpDefinitionFor(ctx context.Context, job *Job) *pumpDefinition {
	kind := launch.ResolveKind(job.Kind)
	source, ok := w.definitions[kind]
	if !ok {
		return &pumpDefinition{reason: fmt.Sprintf("this controller has no definition source for %q jobs", kind)}
	}
	prepared, reason, err := source.Prepare(ctx, job.RunbookID)
	if err != nil {
		slog.Error("a windowed job's definition could not be prepared for its next devices",
			slog.String("job_id", job.JobID),
			slog.String("definition", job.RunbookID),
			slog.String("error", err.Error()))
		return &pumpDefinition{reason: reason}
	}
	injected, reason, err := w.injectFor(ctx, job)
	if err != nil {
		slog.Error("a windowed job's credentials could not be injected for its next devices",
			slog.String("job_id", job.JobID),
			slog.String("error", err.Error()))
		return &pumpDefinition{reason: reason}
	}
	return &pumpDefinition{prepared: prepared, injected: injected}
}

// pumpOne takes one queued task to its end state for this pump: dispatched
// into one of free's slots, or resolved as skipped or failed, or left to
// the pump that claimed it first. It returns free less any slot it used or
// found taken, and whether this pump moved the row.
func (w *Worker) pumpOne(ctx context.Context, job *Job, task JobTask, def *pumpDefinition, free []int) ([]int, bool, error) {
	if def.reason != "" {
		moved, err := w.store.ResolveQueued(ctx, job.JobID, task.DeviceID, OutcomeFailed, def.reason)
		return free, moved, err
	}

	// The device is read again rather than remembered, since it may have
	// changed, been retired or been removed while it waited: admission is
	// asked about the device as it is when its turn comes.
	device, err := w.repo.GetByName(ctx, task.DeviceName)
	switch {
	case errors.Is(err, inventory.ErrItemNotFound):
		device = nil
	case err != nil:
		return free, false, fmt.Errorf("failed to read device %s for job %s: %w", task.DeviceName, job.JobID, err)
	}
	if device == nil || string(device.ID()) != task.DeviceID {
		reason := fmt.Sprintf("device %q left the inventory, or another device took its name, before its turn", task.DeviceName)
		moved, err := w.store.ResolveQueued(ctx, job.JobID, task.DeviceID, OutcomeFailed, reason)
		return free, moved, err
	}

	a := w.admit(job, def.prepared, device)
	if a.outcome != "" {
		moved, err := w.store.ResolveQueued(ctx, job.JobID, task.DeviceID, a.outcome, a.reason)
		return free, moved, err
	}
	return w.claimAndPublish(ctx, job, device, a, def, free)
}

// claimAndPublish claims the first free slot that is still free for an
// admitted device, then publishes its dispatch; a publish that fails
// releases the claim, failing the device and freeing the slot again. It
// reports whether this pump moved the row.
func (w *Worker) claimAndPublish(ctx context.Context, job *Job, device pkginventory.InventoryItem, a admission, def *pumpDefinition, free []int) ([]int, bool, error) {
	deviceID := string(device.ID())
	for len(free) > 0 {
		slot := free[0]
		free = free[1:]
		result, err := w.store.ClaimQueued(ctx, job.JobID, deviceID, slot)
		if err != nil {
			return append([]int{slot}, free...), false, err
		}
		switch result {
		case ClaimSlotTaken:
			// Another replica's pump took this slot a moment ago; the
			// next one may still be free.
			continue
		case ClaimGone:
			// Another pump dispatched or resolved this device, or the job
			// stopped. The slot was not used, so it stays on offer.
			return append([]int{slot}, free...), false, nil
		}
		payload := w.dispatchPayload(ctx, job, def.prepared, def.injected, device, a)
		if reason, ok := w.publishDispatch(ctx, job, "", device, a.mode, payload); !ok {
			if err := w.store.ReleaseClaim(ctx, job.JobID, deviceID, reason); err != nil {
				return free, true, err
			}
			return append([]int{slot}, free...), true, nil
		}
		return free, true, nil
	}
	return free, false, nil
}
