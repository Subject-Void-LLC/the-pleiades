// Package dispatch: entJobStore's terminal writes, Complete, Fail and
// Cancel, split into their own sibling file, following job.go/worker.go/
// worker_devices.go's existing file-per-concern shape, once ent_store.go
// itself grew past AGENTS.md's ~300-line soft cap on logic files (the
// fencing-token fix added a state-plus-fence guard, shared via
// terminalWriteRejected, to Complete and Fail).
//
// Cancel sits here because it writes a terminal state like the other two,
// but it is not their shape: it presents no fence, because the person
// stopping a job holds no fan-out claim. See its own doc comment.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
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

// Cancel transitions jobID to "canceled". See JobStore.Cancel.
func (s *entJobStore) Cancel(ctx context.Context, jobID string, canceledBy string) error {
	// Conditioned on the state being one a job can still be stopped from,
	// named positively rather than as "not completed and not failed". The
	// positive list is what makes this safe to widen: a state added later
	// is not cancelable until somebody decides it is, where a negative
	// list would silently make every future state cancelable, including
	// ones that are terminal. internal/ui/resources/jobs's own
	// terminalStates map is written the same way round for the same
	// reason.
	//
	// No fence guard, unlike Complete and Fail immediately above. Those
	// two are written by the worker that owns the fan-out and must prove
	// it still does; this is written by a person, who never held a claim.
	// Requiring one here would mean a job could only be stopped by the
	// very worker that is busy running it.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.Or(
				job.StateEQ(job.StatePending),
				job.StateEQ(job.StateFanningOut),
				job.StateEQ(job.StateRunning),
			),
		).
		SetState(job.StateCanceled).
		SetCanceledAt(time.Now()).
		SetCanceledBy(canceledBy).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to cancel job %s: %w", jobID, err)
	}
	if affected > 0 {
		// The cancel has taken effect whatever happens next, so a failure
		// to settle the queued rows is logged rather than returned: an
		// error here would tell the operator the cancel failed. Those rows
		// can never be dispatched either way, since a pump claims only into
		// a running job.
		if err := s.skipQueued(ctx, jobID); err != nil {
			slog.Error("a canceled job's queued devices still read as queued",
				slog.String("job_id", jobID),
				slog.String("error", err.Error()))
		}
		return nil
	}
	return s.cancelRejected(ctx, jobID)
}

// skipQueued settles a canceled windowed job's queued devices as skipped,
// so none of them is ever dispatched and the job's record says why they
// never ran. The pump already refuses to claim into a job that is not
// running (ClaimQueued), so this is about the record rather than about
// stopping anything; left undone, those rows would read as still waiting.
func (s *entJobStore) skipQueued(ctx context.Context, jobID string) error {
	row, err := s.jobRow(ctx, jobID)
	if err != nil {
		return err
	}
	skipped, err := s.client.JobTask.Update().
		Where(
			jobtask.HasJobWith(job.IDEQ(row.ID)),
			jobtask.OutcomeEQ(jobtask.OutcomeDispatched),
			jobtask.WaitingEQ(true),
		).
		SetOutcome(jobtask.OutcomeSkipped).
		SetWaiting(false).
		SetReason("the job was canceled before this device's turn").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to skip the queued devices of canceled job %s: %w", jobID, err)
	}
	if skipped == 0 {
		return nil
	}
	if err := s.client.Job.UpdateOneID(row.ID).AddSkippedCount(skipped).Exec(ctx); err != nil {
		return fmt.Errorf("failed to count the skipped devices of canceled job %s: %w", jobID, err)
	}
	return nil
}

// cancelRejected is Cancel's own counterpart to terminalWriteRejected,
// telling apart the two reasons its conditioned update can match no row:
// jobID names no job at all (ErrJobNotFound), or it names one that has
// already stopped (ErrNotCancelable).
//
// It is separate from terminalWriteRejected rather than shared, because
// that function's "real job, wrong state" answer is ErrFenced, which would
// be a lie here: nothing has reclaimed anything, the job simply already
// finished. Telling a caller its claim was superseded when what actually
// happened is that the run completed a second earlier would send an
// operator looking for a second worker that does not exist.
func (s *entJobStore) cancelRejected(ctx context.Context, jobID string) error {
	exists, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to check job %s exists: %w", jobID, err)
	}
	if !exists {
		return fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
	}
	return fmt.Errorf("job %s: %w", jobID, ErrNotCancelable)
}

// SettleCanceled stamps a stopped fan-out's tallies onto an already
// canceled job. See JobStore.SettleCanceled.
func (s *entJobStore) SettleCanceled(ctx context.Context, jobID string, fence int64, dispatched, skipped, failed int) error {
	// No SetState call: this write exists only to make the numbers on a
	// canceled job's record true, and a method that could also move the
	// state would be one more way for a late worker to overwrite what a
	// person decided.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.StateEQ(job.StateCanceled),
			job.FenceEQ(fence),
		).
		SetDispatchedCount(dispatched).
		SetSkippedCount(skipped).
		SetFailedCount(failed).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to settle canceled job %s: %w", jobID, err)
	}
	if affected > 0 {
		return nil
	}
	return s.cancelRejected(ctx, jobID)
}
