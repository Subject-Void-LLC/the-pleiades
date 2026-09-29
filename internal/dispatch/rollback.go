// Package dispatch: a rollback job's fan-out (Phase 40).
//
// A rollback job is an ordinary job over the inventory the undone job ran
// on, with a plan the Controller made and checked when it was asked for
// (RollbackPlan). Its fan-out differs in three places, all here or beside
// the calls that use them: only the devices the plan names are
// dispatched, each carrying its own steps (dispatchPayload); every
// dispatch goes to the rollback subject (publishDispatch); and a planned
// device the inventory no longer holds is recorded as failed, since the
// plan undoes a change on it that is now left in place.
package dispatch

import (
	"context"
	"fmt"
)

// rollbackFanOut tracks a rollback job's planned devices through its
// fan-out.
type rollbackFanOut struct {
	plan *RollbackPlan
	seen map[string]bool
}

// newRollbackFanOut returns the tracker for job, nil for a job that is
// not a rollback, and an error for a rollback whose plan cannot be read:
// such a job fails rather than runs as the ordinary job its runbook id
// names.
func newRollbackFanOut(job *Job) (*rollbackFanOut, error) {
	if job.RollbackOf == "" {
		return nil, nil
	}
	if job.Rollback == nil {
		return nil, fmt.Errorf("the plan of this rollback of job %s cannot be read, so nothing was run", job.RollbackOf)
	}
	return &rollbackFanOut{plan: job.Rollback, seen: map[string]bool{}}, nil
}

// admits reports whether the fan-out dispatches deviceID: every device
// for an ordinary job, and only a planned one for a rollback.
func (r *rollbackFanOut) admits(deviceID string) bool {
	if r == nil {
		return true
	}
	if _, planned := r.plan.StepsFor(deviceID); !planned {
		return false
	}
	r.seen[deviceID] = true
	return true
}

// recordMissing records every planned device the fan-out never reached as
// failed, returning how many.
func (w *Worker) recordMissing(ctx context.Context, job *Job, fence int64, r *rollbackFanOut) (int, error) {
	if r == nil {
		return 0, nil
	}
	missing := 0
	for _, d := range r.plan.Devices {
		if r.seen[d.DeviceID] {
			continue
		}
		if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID: d.DeviceID, DeviceName: d.DeviceName, Outcome: OutcomeFailed,
			Reason: fmt.Sprintf("device %q is no longer in this job's inventory, so its changes were not undone", d.DeviceName),
		}); err != nil {
			return missing, fmt.Errorf("failed to record device %s as missing on job %s: %w", d.DeviceID, job.JobID, err)
		}
		missing++
	}
	return missing, nil
}
