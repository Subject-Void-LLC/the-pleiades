// Package dispatch: entJobStore, the ent-backed JobStore implementation.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/jobtask"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
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
		SetActor(j.Actor).
		SetTemplateName(j.TemplateName).
		SetKind(j.Kind)

	// The four denormalized references, each written only when it has a
	// value: the columns are Optional and Nillable, and a stored zero would
	// claim a record with that id rather than saying there is none.
	//
	// This is the line that gives Job.organization_id its first writer. The
	// value originates on the template, derived from its inventory's
	// required organization edge when the template was saved, and travels
	// here through launch.Resolved. Before Phase 21 a dispatch named a
	// free-text group, which has no tenant to inherit, so the column had
	// existed since Phase 14 with nothing ever setting it.
	if j.InventoryID > 0 {
		create = create.SetInventoryID(j.InventoryID)
	}
	if j.TemplateID > 0 {
		create = create.SetTemplateID(j.TemplateID)
	}
	if j.OrganizationID > 0 {
		create = create.SetOrganizationID(j.OrganizationID)
	}
	if j.LaunchConfigID > 0 {
		create = create.SetLaunchConfigID(j.LaunchConfigID)
	}
	// Set only when non-empty, the same "absent means not supplied" rule
	// the JSON columns carry everywhere else this platform stores a launch
	// field map (launch.Fields.Has's own doc comment): a nil map and an
	// empty one both marshal the same way but a caller reading j.Fields
	// back should not have to tell an ordinary launch with nothing
	// promptable apart from one this store forgot to persist.
	if len(j.Fields) > 0 {
		create = create.SetFields(j.Fields)
	}
	if len(j.ExtraVars) > 0 {
		create = create.SetExtraVars(j.ExtraVars)
	}
	if len(j.CredentialIDs) > 0 {
		create = create.SetCredentialIds(j.CredentialIDs)
	}
	create = create.SetExternalChecks(j.ExternalChecks)
	if j.RollbackOf != "" {
		if j.Rollback == nil {
			return fmt.Errorf("job %s undoes job %s and carries no plan", j.JobID, j.RollbackOf)
		}
		plan, err := json.Marshal(j.Rollback)
		if err != nil {
			return fmt.Errorf("failed to encode the rollback plan of job %s: %w", j.JobID, err)
		}
		create = create.SetRollbackOf(j.RollbackOf).SetRollback(plan)
	}
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

// List returns up to limit jobs, newest first, resuming after the cursor.
// See JobStore.List.
func (s *entJobStore) List(ctx context.Context, after string, limit int) ([]*Job, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("job list limit must be positive, got %d", limit)
	}

	query := s.client.Job.Query().
		Order(ent.Desc(job.FieldJobID)).
		Limit(limit)
	if after != "" {
		query = query.Where(job.JobIDLT(after))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	jobs := make([]*Job, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, toJob(row))
	}
	return jobs, nil
}

// ListForTemplate returns the jobs one template launched, newest first.
// See JobStore.ListForTemplate.
func (s *entJobStore) ListForTemplate(ctx context.Context, templateID, limit int) ([]*Job, error) {
	if templateID <= 0 {
		// Refused rather than treated as "no template", which would return
		// every job created before templates existed as though one
		// particular template had launched them all.
		return nil, fmt.Errorf("job list template id must be positive, got %d", templateID)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("job list limit must be positive, got %d", limit)
	}

	rows, err := s.client.Job.Query().
		Where(job.TemplateIDEQ(templateID)).
		Order(ent.Desc(job.FieldJobID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs for template %d: %w", templateID, err)
	}

	jobs := make([]*Job, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, toJob(row))
	}
	return jobs, nil
}

// RecentForTemplates returns, for each of templateIDs, its perTemplate most
// recent jobs, newest first. See JobStore.RecentForTemplates.
//
// One query rather than a window-function top-N-per-group: it asks for
// perTemplate*len(templateIDs) rows across every named template ordered
// newest first, then groups client-side, keeping only the first perTemplate
// seen per template. That bound is exact -- newest-first means the rows
// dropped for a template that ran more often than its neighbours are always
// its oldest, never its most recent -- and it keeps this store reading
// through ent's query builder like everywhere else in it, rather than the
// one place that dropped to raw SQL for a window function.
func (s *entJobStore) RecentForTemplates(ctx context.Context, templateIDs []int, perTemplate int) (map[int][]*Job, error) {
	if perTemplate <= 0 {
		return nil, fmt.Errorf("recent-for-templates limit must be positive, got %d", perTemplate)
	}
	if len(templateIDs) == 0 {
		return map[int][]*Job{}, nil
	}

	rows, err := s.client.Job.Query().
		Where(job.TemplateIDIn(templateIDs...)).
		Order(ent.Desc(job.FieldJobID)).
		Limit(perTemplate * len(templateIDs)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list recent jobs for %d templates: %w", len(templateIDs), err)
	}

	out := make(map[int][]*Job, len(templateIDs))
	for _, row := range rows {
		j := toJob(row)
		if len(out[j.TemplateID]) >= perTemplate {
			continue
		}
		out[j.TemplateID] = append(out[j.TemplateID], j)
	}
	return out, nil
}

// Get loads job jobID and every JobTask recorded against it. See
// JobStore.Get.
func (s *entJobStore) Get(ctx context.Context, jobID string) (*Job, []JobTask, error) {
	row, err := s.client.Job.Query().
		Where(job.JobIDEQ(jobID)).
		// Ordered, which is not tidiness. Without it the eager load emits
		// no ORDER BY at all, so the database returns these rows in
		// whatever order it finds them, and that order MOVES: an UPDATE
		// rewrites a row, and the result pipeline updates every dispatched
		// task as its device reports. Two consumers read this slice, the
		// JSON job view and the server-rendered device-outcomes table, so
		// a person watching a run sees the table reshuffle under them and
		// a client polling the endpoint gets its array reordered between
		// identical reads.
		//
		// It is also a trap for a client written the obvious way, which is
		// the stronger argument: a per-task field is omitempty, so a
		// caller decoding each poll into one reused value keeps a previous
		// row's value in a slot the new order handed to a different
		// device. This suite's own harness did exactly that, and the
		// resulting failure accused the fan-out. FAILURE_PATTERNS.md #225.
		//
		// Ascending id is insertion order, which is fan-out order, which
		// is the inventory's own device ordering. So the list a reader
		// sees is the order the platform actually worked through.
		//
		// NOT covered by a unit test, deliberately, and this comment is
		// the guard instead. The reshuffle is a PostgreSQL behaviour: an
		// update rewrites the tuple and an unordered sequential scan then
		// finds it somewhere else. SQLite returns rows in rowid order
		// whatever happens, so a test against the in-memory store this
		// package's tests use passes identically with this line deleted.
		// A test that cannot fail is worse than none, because it is
		// counted; catching a regression here needs a real PostgreSQL.
		WithTasks(func(q *ent.JobTaskQuery) { q.Order(ent.Asc(jobtask.FieldID)) }).
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
		// A waiting row is stored as dispatched with its flag set, so a
		// build from before the window reads it as work still out
		// (internal/ent/schema/job_task.go); this build calls it queued.
		if t.Waiting {
			outcome = OutcomeQueued
		}
		tasks = append(tasks, JobTask{
			DeviceID:   t.DeviceID,
			DeviceName: t.DeviceName,
			Outcome:    outcome,
			Reason:     t.Reason,
			// Read straight through rather than through ParseResult. The
			// empty value is the ordinary case here, meaning this device
			// has not reported back, and ParseResult deliberately refuses
			// it: that parser guards what arrives off the mesh, where an
			// empty result is malformed, not what this store itself wrote.
			Result:       Result(t.Result),
			ResultReason: t.ResultReason,
			FinishedAt:   t.FinishedAt,
			Unchecked:    t.Unchecked,
		})
	}

	return toJob(row), tasks, nil
}

// storedOutcome is the outcome column's value for o: a queued device is
// stored as dispatched with its waiting flag set, and every other outcome
// as itself.
func storedOutcome(o Outcome) jobtask.Outcome {
	if o == OutcomeQueued {
		return jobtask.OutcomeDispatched
	}
	return jobtask.Outcome(o)
}

// toJob converts a generated *ent.Job row into this package's own domain
// Job, the one place ent's shape meets the domain model for this store,
// mirroring internal/inventory/ent_repository.go's toRecord.
func toJob(row *ent.Job) *Job {
	job := &Job{
		JobID:           row.JobID,
		RunbookID:       row.RunbookID,
		GroupName:       row.GroupName,
		TemplateName:    row.TemplateName,
		Kind:            row.Kind,
		Actor:           row.Actor,
		State:           row.State.String(),
		DispatchedCount: row.DispatchedCount,
		SkippedCount:    row.SkippedCount,
		FailedCount:     row.FailedCount,
		FailureReason:   row.FailureReason,
		CanceledAt:      row.CanceledAt,
		CanceledBy:      row.CanceledBy,
		CreatedAt:       row.CreatedAt,
	}

	// The four nillable references. A nil column means the job names no
	// such record, which is a different fact from naming record zero, so
	// the domain zero value is only ever reached by way of an absent
	// column rather than by dereferencing one that is not there.
	if row.InventoryID != nil {
		job.InventoryID = *row.InventoryID
	}
	if row.TemplateID != nil {
		job.TemplateID = *row.TemplateID
	}
	if row.OrganizationID != nil {
		job.OrganizationID = *row.OrganizationID
	}
	if row.LaunchConfigID != nil {
		job.LaunchConfigID = *row.LaunchConfigID
	}
	if len(row.Fields) > 0 {
		job.Fields = launch.Fields(row.Fields)
	}
	if len(row.ExtraVars) > 0 {
		job.ExtraVars = row.ExtraVars
	}
	if len(row.CredentialIds) > 0 {
		job.CredentialIDs = row.CredentialIds
	}
	job.ExternalChecks = row.ExternalChecks
	// A plan that does not decode leaves Rollback nil beside a set
	// RollbackOf, which the fan-out fails rather than runs.
	if row.RollbackOf != "" {
		job.RollbackOf = row.RollbackOf
		var plan RollbackPlan
		if err := json.Unmarshal(row.Rollback, &plan); err == nil {
			job.Rollback = &plan
		}
	}
	return job
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
	// Somebody stopped this job while its fan-out was in flight. Checked
	// here, on a row this method has already read for the fence, so it
	// costs no extra query: the fan-out loop learns of a cancel on its
	// next device rather than by polling, and the devices it has not
	// reached are never dispatched to. This is the durable half of what
	// Cancel promises.
	//
	// It is checked AFTER the fence, deliberately. A superseded worker is
	// superseded whatever the job's state is, and telling it the job was
	// canceled would send it to the wrong conclusion about why it must
	// stop.
	if row.State == job.StateCanceled {
		return fmt.Errorf("job %s: %w", jobID, ErrCanceled)
	}

	create := s.client.JobTask.Create().
		SetJobID(row.ID).
		SetDeviceID(task.DeviceID).
		SetDeviceName(task.DeviceName).
		SetOutcome(storedOutcome(task.Outcome)).
		SetWaiting(task.Outcome == OutcomeQueued)
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
