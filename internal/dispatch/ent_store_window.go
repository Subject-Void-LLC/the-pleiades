// Package dispatch: entJobStore's writes for a windowed job, one whose
// forks launch field limits how many of its devices run at once
// (window.go).
//
// Every write here is a conditional update on the state it moves a row
// from, because several Controller replicas may pump one job at the same
// moment: results land on whichever replica's consumer pulled them, and
// the leader's sweep pumps too. Nothing here holds a lock. The forks bound
// itself is the unique index on (job, slot) (internal/ent/schema/job_task.go),
// so the database refuses the second claim on a slot rather than this code
// counting and hoping.
package dispatch

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
)

// jobRow resolves jobID to its row, mapping "no such job" onto
// ErrJobNotFound the way every other method in this store does.
func (s *entJobStore) jobRow(ctx context.Context, jobID string) (*ent.Job, error) {
	row, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
		}
		return nil, fmt.Errorf("failed to read job %s: %w", jobID, err)
	}
	return row, nil
}

// Lookup returns jobID's record without its tasks. See JobStore.Lookup.
func (s *entJobStore) Lookup(ctx context.Context, jobID string) (*Job, error) {
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return toJob(row), nil
}

// QueuedTasks returns up to limit of jobID's queued tasks, oldest first.
// See JobStore.QueuedTasks.
func (s *entJobStore) QueuedTasks(ctx context.Context, jobID string, limit int) ([]JobTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.client.JobTask.Query().
		Where(
			jobtask.HasJobWith(job.JobIDEQ(jobID)),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.WaitingEQ(true),
		).
		// The id is insertion order, which is admission order: devices
		// start in the order the fan-out walked them.
		Order(ent.Asc(jobtask.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read queued devices on job %s: %w", jobID, err)
	}
	tasks := make([]JobTask, 0, len(rows))
	for _, r := range rows {
		tasks = append(tasks, JobTask{DeviceID: r.DeviceID, DeviceName: r.DeviceName, Outcome: OutcomeQueued})
	}
	return tasks, nil
}

// HeldSlots returns the slots jobID's running devices hold. See
// JobStore.HeldSlots.
func (s *entJobStore) HeldSlots(ctx context.Context, jobID string) ([]int, error) {
	rows, err := s.client.JobTask.Query().
		Where(
			jobtask.HasJobWith(job.JobIDEQ(jobID)),
			jobtask.SlotNotNil(),
		).
		Select(jobtask.FieldSlot).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the slots held on job %s: %w", jobID, err)
	}
	slots := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.Slot != nil {
			slots = append(slots, *r.Slot)
		}
	}
	return slots, nil
}

// ClaimQueued moves one queued task to dispatched, holding slot. See
// JobStore.ClaimQueued.
func (s *entJobStore) ClaimQueued(ctx context.Context, jobID, deviceID string, slot int) (ClaimResult, error) {
	if slot < 0 {
		return ClaimGone, fmt.Errorf("slot %d on job %s is not a window position", slot, jobID)
	}
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return ClaimGone, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return ClaimGone, fmt.Errorf("failed to begin claiming a slot on job %s: %w", jobID, err)
	}
	// The job's state is part of the same statement, so a cancel that
	// commits before this one is seen, and a device is not dispatched
	// into a job somebody has already stopped.
	affected, err := tx.JobTask.Update().
		Where(
			jobtask.HasJobWith(job.IDEQ(row.ID), job.StateEQ(job.StateRunning)),
			jobtask.DeviceIDEQ(deviceID),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.WaitingEQ(true),
		).
		SetWaiting(false).
		SetSlot(slot).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			// Another device already holds this slot: the (job, slot)
			// index is what just said so.
			return ClaimSlotTaken, nil
		}
		return ClaimGone, fmt.Errorf("failed to claim slot %d for device %s on job %s: %w", slot, deviceID, jobID, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return ClaimGone, nil
	}
	if err := tx.Job.UpdateOneID(row.ID).AddDispatchedCount(1).Exec(ctx); err != nil {
		return ClaimGone, rollback(tx, fmt.Errorf("failed to count the dispatch of device %s on job %s: %w", deviceID, jobID, err))
	}
	if err := tx.Commit(); err != nil {
		return ClaimGone, fmt.Errorf("failed to commit the claim of device %s on job %s: %w", deviceID, jobID, err)
	}
	return ClaimMade, nil
}

// ResolveQueued moves one queued task to skipped or failed. See
// JobStore.ResolveQueued.
func (s *entJobStore) ResolveQueued(ctx context.Context, jobID, deviceID string, outcome Outcome, reason string) (bool, error) {
	if outcome != OutcomeSkipped && outcome != OutcomeFailed {
		return false, fmt.Errorf("a queued device can only be skipped or failed, not %q", outcome)
	}
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return false, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin resolving device %s on job %s: %w", deviceID, jobID, err)
	}
	affected, err := tx.JobTask.Update().
		Where(
			jobtask.HasJobWith(job.IDEQ(row.ID)),
			jobtask.DeviceIDEQ(deviceID),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.WaitingEQ(true),
		).
		SetOutcome(jobtask.Outcome(outcome)).
		SetWaiting(false).
		SetReason(reason).
		Save(ctx)
	if err != nil {
		return false, rollback(tx, fmt.Errorf("failed to resolve device %s on job %s: %w", deviceID, jobID, err))
	}
	if affected == 0 {
		_ = tx.Rollback()
		return false, nil
	}
	tally := tx.Job.UpdateOneID(row.ID)
	if outcome == OutcomeSkipped {
		tally = tally.AddSkippedCount(1)
	} else {
		tally = tally.AddFailedCount(1)
	}
	if err := tally.Exec(ctx); err != nil {
		return false, rollback(tx, fmt.Errorf("failed to count device %s on job %s: %w", deviceID, jobID, err))
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit resolving device %s on job %s: %w", deviceID, jobID, err)
	}
	return true, nil
}

// ReleaseClaim fails a claimed device whose dispatch was never published.
// See JobStore.ReleaseClaim.
func (s *entJobStore) ReleaseClaim(ctx context.Context, jobID, deviceID, reason string) error {
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin releasing device %s on job %s: %w", deviceID, jobID, err)
	}
	// Only a claim: dispatched, holding a slot, and with no result. A
	// device that already reported ran, and its row is not this call's to
	// rewrite.
	affected, err := tx.JobTask.Update().
		Where(
			jobtask.HasJobWith(job.IDEQ(row.ID)),
			jobtask.DeviceIDEQ(deviceID),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.SlotNotNil(),
			jobtask.ResultIsNil(),
		).
		SetOutcome(jobtask.OutcomeFailed).
		SetReason(reason).
		ClearSlot().
		Save(ctx)
	if err != nil {
		return rollback(tx, fmt.Errorf("failed to release device %s on job %s: %w", deviceID, jobID, err))
	}
	if affected == 0 {
		_ = tx.Rollback()
		return nil
	}
	if err := tx.Job.UpdateOneID(row.ID).AddDispatchedCount(-1).AddFailedCount(1).Exec(ctx); err != nil {
		return rollback(tx, fmt.Errorf("failed to recount device %s on job %s: %w", deviceID, jobID, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit releasing device %s on job %s: %w", deviceID, jobID, err)
	}
	return nil
}

// ListQueuedJobs returns the running jobs that still have queued tasks.
// See JobStore.ListQueuedJobs.
func (s *entJobStore) ListQueuedJobs(ctx context.Context) ([]string, error) {
	ids, err := s.client.Job.Query().
		Where(
			job.StateEQ(job.StateRunning),
			job.HasTasksWith(jobtask.OutcomeEQ(jobtask.OutcomeDispatched), jobtask.WaitingEQ(true)),
		).
		Select(job.FieldJobID).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs with queued devices: %w", err)
	}
	return ids, nil
}

// CompleteIfDone ends a running job with nothing left to run. See
// JobStore.CompleteIfDone.
func (s *entJobStore) CompleteIfDone(ctx context.Context, jobID string) error {
	outstanding, err := s.outstandingDevices(ctx, jobID)
	if err != nil {
		return err
	}
	if outstanding > 0 {
		return nil
	}
	return s.CompleteRunning(ctx, jobID)
}

// rollback abandons tx and returns cause, joined with the rollback's own
// failure when there is one, so neither is lost.
func rollback(tx *ent.Tx, cause error) error {
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, err)
	}
	return cause
}

// DispatchState says whether jobID's device was handed to a Runner. See
// JobStore.DispatchState.
func (s *entJobStore) DispatchState(ctx context.Context, jobID, deviceID string) (DispatchState, error) {
	row, err := s.client.JobTask.Query().
		Where(
			jobtask.HasJobWith(job.JobIDEQ(jobID)),
			jobtask.DeviceIDEQ(deviceID),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return DispatchUnrecorded, nil
		}
		return DispatchUnrecorded, fmt.Errorf("failed to read device %s on job %s: %w", deviceID, jobID, err)
	}
	if row.Outcome == jobtask.OutcomeDispatched && !row.Waiting {
		return DispatchSent, nil
	}
	return DispatchNotSent, nil
}
