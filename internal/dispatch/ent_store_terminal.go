// Package dispatch: entJobStore's two terminal writes, Complete and Fail,
// split into their own sibling file, following job.go/worker.go/
// worker_devices.go's existing file-per-concern shape, once ent_store.go
// itself grew past AGENTS.md's ~300-line soft cap on logic files (the
// fencing-token fix added a state-plus-fence guard, shared via
// terminalWriteRejected, to both of these methods).
package dispatch

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
)

// Complete transitions jobID to "completed" and stamps its final tallies.
// See JobStore.Complete.
func (s *entJobStore) Complete(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error {
	// Conditioned on both state = fanning_out (the belt-and-suspenders
	// guard: a job already "completed" or "failed" can never match this
	// WHERE clause a second time, closing the double-Complete/Fail
	// finding even independent of fencing) and fence = fence (the caller
	// must still hold the current claim). Either guard failing alone is
	// enough to make affected == 0.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.StateEQ(job.StateFanningOut),
			job.FenceEQ(fence),
		).
		SetState(job.StateCompleted).
		SetDispatchedCount(dispatched).
		SetSkippedCount(skipped).
		SetFailedCount(failed).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete job %s: %w", jobID, err)
	}
	if affected > 0 {
		return nil
	}
	return s.terminalWriteRejected(ctx, jobID)
}

// Fail transitions jobID to "failed" and stamps reason. See JobStore.Fail.
func (s *entJobStore) Fail(ctx context.Context, jobID string, fence int64, reason string) error {
	// See Complete's own comment: the identical state-plus-fence guard.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.StateEQ(job.StateFanningOut),
			job.FenceEQ(fence),
		).
		SetState(job.StateFailed).
		SetFailureReason(reason).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to fail job %s: %w", jobID, err)
	}
	if affected > 0 {
		return nil
	}
	return s.terminalWriteRejected(ctx, jobID)
}

// terminalWriteRejected is called by Complete and Fail when their own
// state-plus-fence-conditioned update matches no row, to distinguish the
// two different reasons that can happen for: jobID names no job at all
// (ErrJobNotFound), versus jobID names a real job whose state is no longer
// "fanning_out" or whose fence no longer matches (ErrFenced), which
// happens both when this same claim already reached a terminal state via
// an earlier Complete or Fail call, and when a later BeginFanOut reclaim
// has superseded this claim entirely. Both of those "real job, wrong
// state/fence" cases mean the same thing to the caller: stop, do not
// retry, so they share one sentinel rather than needing to be told apart.
func (s *entJobStore) terminalWriteRejected(ctx context.Context, jobID string) error {
	exists, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to check job %s exists: %w", jobID, err)
	}
	if !exists {
		return fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
	}
	return fmt.Errorf("job %s: %w", jobID, ErrFenced)
}
