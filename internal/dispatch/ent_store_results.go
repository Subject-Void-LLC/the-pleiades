// Package dispatch: entJobStore's result-aggregation writes, the half of a
// job's life that happens after its fan-out has finished.
//
// These live in their own sibling file rather than beside Complete and
// Fail, because they answer a different question. ent_store_terminal.go's
// writes are made by the worker that owns the fan-out, present a fence to
// prove it, and end the Controller's own work. These are made by a
// consumer reading results off the mesh, arrive one device at a time and
// out of order, hold no claim on anything, and end the RUN.
package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
)

// SettleRunning ends a fan-out that handed work to at least one Runner.
// See JobStore.SettleRunning.
func (s *entJobStore) SettleRunning(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error {
	// The identical state-plus-fence guard Complete takes, for the
	// identical reasons: the caller must still own the fan-out, and a job
	// that has already left "fanning_out" (because somebody cancelled it,
	// most usually) must not be dragged back into a live state by a
	// worker finishing its loop a moment later.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.StateEQ(job.StateFanningOut),
			job.FenceEQ(fence),
		).
		SetState(job.StateRunning).
		SetDispatchedCount(dispatched).
		SetSkippedCount(skipped).
		SetFailedCount(failed).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to settle job %s as running: %w", jobID, err)
	}
	if affected == 0 {
		return s.terminalWriteRejected(ctx, jobID)
	}

	// The lost wakeup, closed here because this is the only place that can
	// close it.
	//
	// A result can arrive and be recorded while the fan-out loop is still
	// running, because the task row is written inside the loop and this
	// runs after it. If the LAST outstanding result lands in that window,
	// RecordResult correctly reports the job is waiting on nothing, and
	// CompleteRunning then matches no row because the job is still
	// "fanning_out" -- which is not an error and is deliberately treated
	// as one of the ordinary no-op endings. The line above then parks the
	// job in "running" with zero outstanding tasks and nothing left in the
	// system that would ever end it. Nothing sweeps "running", so that is
	// permanent.
	//
	// It is not a narrow window in practice: a runbook that finishes in
	// microseconds against a device that answers immediately reports back
	// well before a fan-out over the rest of its group has finished
	// walking.
	//
	// Re-asking rather than tracking, for the reason RecordResult counts
	// rows instead of keeping a counter: two parties may reach this
	// conclusion at once, and CompleteRunning's own "running" guard makes
	// the second one a no-op rather than a double write.
	outstanding, err := s.outstandingDevices(ctx, jobID)
	if err != nil {
		return err
	}
	if outstanding > 0 {
		return nil
	}
	return s.CompleteRunning(ctx, jobID)
}

// outstandingDevices counts the devices this job still has to hear from:
// those it handed to a Runner that have not reported back, and those a
// windowed job admitted and has not yet dispatched.
//
// Dispatched tasks with no result, which is deliberately not "every task":
// a skipped device never ran and a device whose dispatch failed never
// reached a Runner, so neither will ever report and counting either would
// leave the job waiting forever on something that cannot arrive. A
// windowed job's waiting device is stored as dispatched with no result
// (internal/ent/schema/job_task.go), so it is counted too, and must be:
// otherwise a job whose first window drained would read as finished while
// most of its devices were still waiting for their turn. That it needs no
// clause of its own here is the point of storing it that way, since a
// Controller from before the window runs this same query.
func (s *entJobStore) outstandingDevices(ctx context.Context, jobID string) (int, error) {
	outstanding, err := s.client.JobTask.Query().
		Where(
			jobtask.HasJobWith(job.JobIDEQ(jobID)),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.ResultIsNil(),
		).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count outstanding devices on job %s: %w", jobID, err)
	}
	return outstanding, nil
}

// RecordResult records one device's execution outcome and reports whether
// the job is now waiting on nothing. See JobStore.RecordResult.
func (s *entJobStore) RecordResult(ctx context.Context, jobID, deviceID string, result Result, reason string, unchecked int) (bool, error) {
	if unchecked < 0 {
		return false, fmt.Errorf("device %s on job %s reported %d unchecked tasks, which is not a count", deviceID, jobID, unchecked)
	}
	row, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
		}
		return false, fmt.Errorf("failed to resolve internal id for job %s: %w", jobID, err)
	}

	// Only a task this job actually dispatched to can carry a result. A
	// skipped device never ran, and a device whose dispatch failed never
	// reached a Runner, so a result naming either is a message about
	// something that did not happen.
	affected, err := s.client.JobTask.Update().
		Where(
			jobtask.HasJobWith(job.IDEQ(row.ID)),
			jobtask.DeviceIDEQ(deviceID),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			// A waiting device was never handed to a Runner, so a result
			// naming it is about something that did not happen.
			jobtask.WaitingEQ(false),
		).
		SetResult(jobtask.Result(result)).
		SetResultReason(reason).
		SetUnchecked(unchecked).
		SetFinishedAt(time.Now()).
		// A device that has reported frees its place in a windowed job's
		// forks window (window.go); for any other job it holds none, and
		// clearing an empty slot changes nothing.
		ClearSlot().
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to record result for device %s on job %s: %w", deviceID, jobID, err)
	}
	if affected == 0 {
		// Either the device is not one this job dispatched to, or the job
		// has no such task at all. Both are "there is nothing here to
		// record", and both are permanent, so they share ErrJobNotFound
		// and the consumer terminates the message rather than retrying it
		// forever.
		return false, fmt.Errorf("job %s has no dispatched task for device %s: %w", jobID, deviceID, ErrJobNotFound)
	}

	// Counted from the rows rather than tracked in a counter, which is
	// what makes a redelivered result harmless: writing the same result
	// twice leaves this count unchanged, where a decrementing counter
	// would run past zero and end the job early.
	//
	// The same question SettleRunning asks, through the same helper, so
	// the two cannot drift into disagreeing about what "outstanding"
	// means.
	outstanding, err := s.outstandingDevices(ctx, jobID)
	if err != nil {
		return false, err
	}
	return outstanding == 0, nil
}

// CompleteRunning ends a job every device has now reported on. See
// JobStore.CompleteRunning.
func (s *entJobStore) CompleteRunning(ctx context.Context, jobID string) error {
	// Guarded on "running" alone, with no fence. The fan-out's claim ended
	// when it settled the job into this state, and the party writing here
	// is a result consumer that never held one.
	//
	// The guard is what makes a race between two final-looking results
	// safe: both may call this, and the second matches no row and does
	// nothing. It is also what stops a result arriving after somebody
	// cancelled the job from quietly reviving it as completed, which
	// matters because a cancel does not wait for in-flight work to stop
	// and those results genuinely do still arrive.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.StateEQ(job.StateRunning),
		).
		SetState(job.StateCompleted).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete running job %s: %w", jobID, err)
	}
	if affected > 0 {
		return nil
	}
	// Nothing was updated, which is an ordinary outcome here rather than
	// an error: another result got there first, or the job was cancelled.
	// Only a job that does not exist at all is worth reporting.
	exists, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to check job %s exists: %w", jobID, err)
	}
	if !exists {
		return fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
	}
	return nil
}
