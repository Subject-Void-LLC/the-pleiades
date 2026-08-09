// Package dispatch: entJobStore, the ent-backed JobStore implementation.
package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
)

// heartbeatRefreshInterval is the minimum real time RecordTask lets pass
// between two writes of a job's own updated_at heartbeat (see RecordTask's
// own doc comment). It must stay comfortably below fanOutLeaseTTL
// (worker.go) so a genuinely active fan-out's heartbeat is always refreshed
// well before BeginFanOut's staleAfter reclaim would consider the job
// abandoned.
const heartbeatRefreshInterval = time.Minute

// entJobStore is the JobStore implementation backed by *ent.Client. See
// NewEntJobStore.
type entJobStore struct {
	client *ent.Client
}

// NewEntJobStore builds a JobStore backed by client, following
// internal/inventory/ent_repository.go's own conventions for translating a
// not-found ent error into this package's own sentinel and for building
// domain structs from generated rows.
func NewEntJobStore(client *ent.Client) JobStore {
	return &entJobStore{client: client}
}

// Create persists a brand new job row. See JobStore.Create.
func (s *entJobStore) Create(ctx context.Context, j *Job) error {
	create := s.client.Job.Create().
		SetRunbookID(j.RunbookID).
		SetGroupName(j.GroupName).
		SetActor(j.Actor)
	// job_id has a DefaultFunc (newJobID, internal/ent/schema/job.go), but
	// a caller-supplied JobID is honored when present, mirroring
	// device.go's own optional-override-of-a-generated-default pattern:
	// a caller that already minted an id (e.g. to embed it in a response
	// before the row exists) is not forced to discard it.
	if j.JobID != "" {
		create = create.SetJobID(j.JobID)
	}

	row, err := create.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create job for runbook %s: %w", j.RunbookID, err)
	}

	// Reflect the store-assigned fields (a generated JobID, State,
	// CreatedAt) back onto the caller's struct, the same "hydrate what the
	// database actually decided" step ent_save.go's Save performs for a
	// device's Version after a write.
	j.JobID = row.JobID
	j.State = row.State.String()
	j.CreatedAt = row.CreatedAt
	return nil
}

// Get loads job jobID and every JobTask recorded against it. See
// JobStore.Get.
func (s *entJobStore) Get(ctx context.Context, jobID string) (*Job, []JobTask, error) {
	row, err := s.client.Job.Query().
		Where(job.JobIDEQ(jobID)).
		WithTasks().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil, fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
		}
		return nil, nil, fmt.Errorf("failed to load job %s: %w", jobID, err)
	}

	tasks := make([]JobTask, 0, len(row.Edges.Tasks))
	for _, t := range row.Edges.Tasks {
		outcome, err := ParseOutcome(t.Outcome.String())
		if err != nil {
			// A row this store itself wrote is now unreadable back: this
			// is a genuine data-integrity error, not a caller mistake, so
			// it is surfaced rather than silently dropping the task from
			// the returned slice.
			return nil, nil, fmt.Errorf("job %s: %w", jobID, err)
		}
		tasks = append(tasks, JobTask{
			DeviceID:   t.DeviceID,
			DeviceName: t.DeviceName,
			Outcome:    outcome,
			Reason:     t.Reason,
		})
	}

	return toJob(row), tasks, nil
}

// toJob converts a generated *ent.Job row into this package's own domain
// Job, the one place ent's shape meets the domain model for this store,
// mirroring internal/inventory/ent_repository.go's toRecord.
func toJob(row *ent.Job) *Job {
	return &Job{
		JobID:           row.JobID,
		RunbookID:       row.RunbookID,
		GroupName:       row.GroupName,
		Actor:           row.Actor,
		State:           row.State.String(),
		DispatchedCount: row.DispatchedCount,
		SkippedCount:    row.SkippedCount,
		FailedCount:     row.FailedCount,
		CreatedAt:       row.CreatedAt,
	}
}

// BeginFanOut atomically claims jobID's fan-out, either the normal pending
// claim or a stale-fanning_out reclaim, and bumps the job's fencing token
// as part of that same atomic write. See JobStore.BeginFanOut.
func (s *entJobStore) BeginFanOut(ctx context.Context, jobID string, staleAfter time.Duration) (bool, int64, error) {
	// A "fanning_out" job whose updated_at is at or before cutoff has had
	// no RecordTask heartbeat in at least staleAfter, so it is treated as
	// abandoned (most likely a Worker that crashed or was killed after
	// claiming it) rather than still actively being worked on.
	cutoff := time.Now().Add(-staleAfter)

	// The one WHERE-guarded conditional bulk update this method's own
	// contract requires: state = pending -> fanning_out, OR a stale
	// fanning_out row reclaimed the same way. This mirrors
	// internal/inventory/ent_save.go's Save method exactly (its own
	// "client.Device.Update().Where(device.DeviceIDEQ(...),
	// device.VersionEQ(baseVersion))...Save(ctx)" idiom): the decision of
	// whether this call is the one that gets to transition the row is
	// made entirely by the database evaluating the WHERE clause, never by
	// a prior read this goroutine performed and could race against.
	//
	// AddFence(1) rides along on this exact same conditional write, in
	// both the pending-claim and the stale-reclaim branch: it is an
	// atomic increment (SET fence = fence + 1 in the generated SQL), not
	// a read-modify-write pair, so two callers racing for the same claim
	// can never both compute the same "next" fence value from a stale
	// read. Whichever caller's WHERE clause actually matches the row is
	// the only one whose AddFence(1) ever applies.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.Or(
				job.StateEQ(job.StatePending),
				job.And(job.StateEQ(job.StateFanningOut), job.UpdatedAtLTE(cutoff)),
			),
		).
		SetState(job.StateFanningOut).
		AddFence(1).
		Save(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("failed to begin fan-out for job %s: %w", jobID, err)
	}
	if affected > 0 {
		// The Update/Save call above reports only the affected row count,
		// not the post-increment field values, so the fence actually
		// stored (this claim's fencing token) is read back with a
		// follow-up query. This only runs on the already-rare
		// claim/reclaim path, never on the hot per-device loop, so the
		// extra round trip costs nothing that matters.
		row, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Only(ctx)
		if err != nil {
			return false, 0, fmt.Errorf("failed to read back fence for job %s after claiming fan-out: %w", jobID, err)
		}
		return true, row.Fence, nil
	}

	// affected == 0 means the conditional update matched no row, which is
	// ambiguous on its own: either jobID names no job at all, or it names
	// a real job that is "completed", "failed", or "fanning_out" but not
	// yet stale. This existence check runs only on that already-unusual
	// path, never on the hot, successful-claim path above, so it does not
	// reopen the read-then-write race this method's own contract forbids
	// for the transition decision itself.
	exists, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Exist(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("failed to check job %s exists: %w", jobID, err)
	}
	if !exists {
		return false, 0, fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
	}
	return false, 0, nil
}

// ListStaleFanOuts returns every job's JobID currently "fanning_out" whose
// updated_at is at or before cutoff, the exact same predicate BeginFanOut's
// own reclaim branch evaluates (see that method's own comment on cutoff),
// as a read-only scan rather than a claim. See JobStore.ListStaleFanOuts.
func (s *entJobStore) ListStaleFanOuts(ctx context.Context, staleAfter time.Duration) ([]string, error) {
	cutoff := time.Now().Add(-staleAfter)

	ids, err := s.client.Job.Query().
		Where(job.StateEQ(job.StateFanningOut), job.UpdatedAtLTE(cutoff)).
		Select(job.FieldJobID).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list stale fanning_out jobs: %w", err)
	}
	return ids, nil
}

// RecordTask persists task as jobID's outcome for one device and refreshes
// jobID's fan-out heartbeat. See JobStore.RecordTask.
//
// This does not itself guard against being called twice for the same
// device (e.g. once by a crashed attempt and again by the reclaim that
// supersedes it): that would cost every caller, including the overwhelming
// majority that are never reclaimed, an extra existence check per device.
// Worker.HandleJobRequested carries that guard instead, using the task
// list its own Get call already returns to skip a device already recorded
// by a superseded attempt before ever reaching RecordTask, which is why
// this call is documented as "regardless of outcome" rather than
// idempotent: by the time this runs, the caller has already established
// the device is new.
func (s *entJobStore) RecordTask(ctx context.Context, jobID string, fence int64, task JobTask) error {
	// JobTask.job is a required edge keyed on the Job row's internal
	// integer id, not the opaque job_id string this method's own caller
	// deals in; one lookup resolves it, mirroring ent_save.go's identical
	// "resolve the internal id for the Revision edge" step. The same read
	// also carries the job's currently stored fence, checked below before
	// any write happens, so a caller already superseded by a later
	// reclaim (see BeginFanOut) is rejected up front rather than writing
	// a task row under a claim it no longer holds.
	row, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
		}
		return fmt.Errorf("failed to resolve internal id for job %s: %w", jobID, err)
	}
	if row.Fence != fence {
		return fmt.Errorf("job %s: %w", jobID, ErrFenced)
	}

	create := s.client.JobTask.Create().
		SetJobID(row.ID).
		SetDeviceID(task.DeviceID).
		SetDeviceName(task.DeviceName).
		SetOutcome(jobtask.Outcome(task.Outcome))
	if task.Reason != "" {
		create = create.SetReason(task.Reason)
	}
	if _, err := create.Save(ctx); err != nil {
		return fmt.Errorf("failed to record task for device %s on job %s: %w", task.DeviceID, jobID, err)
	}

	// Refresh the job's own updated_at as a heartbeat, but only when the
	// last refresh is more than heartbeatRefreshInterval old. BeginFanOut's
	// staleAfter reclaim only reclaims a "fanning_out" job whose updated_at
	// has not moved in a while (see fanOutLeaseTTL, worker.go), so a Worker
	// genuinely still, actively working through this fan-out must keep
	// pushing updated_at forward often enough to stay well under that
	// threshold, or its own still-live job would eventually look abandoned
	// to another delivery. It does not need to do that on every single
	// call to be effective, though: row.UpdatedAt was already loaded by
	// the query above, so checking it here costs nothing extra, and
	// skipping the write when it is still fresh keeps a fast, healthy,
	// many-thousand-device fan-out (this package's own 10,000-device
	// Release Gate) from paying an extra write per device for a heartbeat
	// only a genuinely slow or stalled run would ever need to observe.
	// The heartbeat refresh itself is also conditioned on job.FenceEQ(fence)
	// (in addition to the staleness-based skip above, unchanged from
	// before): this is the "own ent Update().Where(...)" this method
	// conditions on the caller's fence, catching the narrow race where
	// another delivery's reclaim bumps the stored fence in between the
	// read above and this write. affected == 0 here can only mean that
	// race (the job row itself is never deleted), so it is reported as
	// ErrFenced, not ErrJobNotFound.
	if time.Since(row.UpdatedAt) >= heartbeatRefreshInterval {
		affected, err := s.client.Job.Update().
			Where(job.JobIDEQ(jobID), job.FenceEQ(fence)).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to refresh fan-out heartbeat for job %s: %w", jobID, err)
		}
		if affected == 0 {
			return fmt.Errorf("job %s: %w", jobID, ErrFenced)
		}
	}
	return nil
}
