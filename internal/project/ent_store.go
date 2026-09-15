// This file is the ent-backed Store: projects as rows.
//
// It carries no secret material. A project names a Credential by id and the
// clone resolves it through credstore at sync time, so nothing here reads,
// writes or redacts one.
package project

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
	entsyncrun "github.com/Subject-Void-LLC/the-pleiades/internal/ent/syncrun"
)

// historyLimit bounds a sync history read when a caller names no limit.
const historyLimit = 50

// defaultPageSize bounds a List with no explicit limit. It is the store's,
// never the caller's: a page size read from a query parameter with no
// ceiling is a request to hold every row in memory.
const defaultPageSize = 100

// entStore is the ent-backed Store.
type entStore struct{ client *ent.Client }

// NewEntStore returns a Store over the given client.
func NewEntStore(client *ent.Client) Store { return &entStore{client: client} }

// Create stores a new project.
func (s *entStore) Create(ctx context.Context, p Project) (Project, error) {
	create := s.client.Project.Create().
		SetName(p.Name).
		SetDescription(p.Description).
		SetScmType(entproject.ScmType(p.SCMType)).
		SetScmURL(p.SCMURL).
		SetScmBranch(p.SCMBranch).
		SetOrganizationID(p.OrganizationID)
	if p.CredentialID > 0 {
		create = create.SetCredentialID(p.CredentialID)
	}

	row, err := create.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return Project{}, ErrExists
		}
		return Project{}, fmt.Errorf("project: creating %q: %w", p.Name, err)
	}
	return s.Get(ctx, row.ID)
}

// Get returns one project.
func (s *entStore) Get(ctx context.Context, id int) (Project, error) {
	row, err := s.client.Project.Query().
		Where(entproject.IDEQ(id)).
		WithOrganization().
		WithCredential().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("project: reading %d: %w", id, err)
	}
	return hydrate(row), nil
}

// List returns projects, newest id last.
func (s *entStore) List(ctx context.Context, q Query) ([]Project, error) {
	limit := q.Limit
	if limit <= 0 || limit > defaultPageSize {
		limit = defaultPageSize
	}

	query := s.client.Project.Query().
		WithOrganization().
		WithCredential().
		Order(ent.Asc(entproject.FieldID)).
		Limit(limit)
	if q.OrganizationID > 0 {
		// Filtered in the query rather than over the result, because
		// filtering after a Limit returns a short page that looks like the
		// end of the list.
		query = query.Where(entproject.HasOrganizationWith(entorg.IDEQ(q.OrganizationID)))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("project: listing: %w", err)
	}

	out := make([]Project, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Update saves a project's own fields.
//
// It writes neither the sync state nor the checkout location. Those belong
// to RecordSync, because a rename landing while a sync is finishing must
// not put a stale revision back, and a sync finishing must not revert a
// rename.
func (s *entStore) Update(ctx context.Context, p Project) error {
	update := s.client.Project.UpdateOneID(p.ID).
		SetName(p.Name).
		SetDescription(p.Description).
		SetScmType(entproject.ScmType(p.SCMType)).
		SetScmURL(p.SCMURL).
		SetScmBranch(p.SCMBranch)
	if p.CredentialID > 0 {
		update = update.SetCredentialID(p.CredentialID)
	} else {
		update = update.ClearCredential()
	}

	if err := update.Exec(ctx); err != nil {
		switch {
		case ent.IsNotFound(err):
			return ErrNotFound
		case ent.IsConstraintError(err):
			return ErrExists
		default:
			return fmt.Errorf("project: updating %d: %w", p.ID, err)
		}
	}
	return nil
}

// Delete removes a project.
//
// The working tree on disk is deliberately left behind. This store has no
// filesystem and no business having one, and an orphaned checkout is
// recoverable disk while a deletion racing a running sync is not.
func (s *entStore) Delete(ctx context.Context, id int) error {
	if err := s.client.Project.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("project: deleting %d: %w", id, err)
	}
	return nil
}

// RecordSync saves the outcome of one sync attempt.
func (s *entStore) RecordSync(ctx context.Context, id int, result Result) error {
	update := s.client.Project.UpdateOneID(id).
		SetSyncStatus(entproject.SyncStatus(result.Status)).
		SetSyncError(result.Err)

	// A failed attempt must not overwrite the revision or the path a
	// previous success recorded: the old checkout is still on disk and
	// still the last thing known to work, and blanking the revision would
	// make a transient network failure look like a project that had never
	// synced.
	if result.Status == SyncSucceeded {
		update = update.SetRevision(result.Revision).SetLocalPath(result.LocalPath)
	}
	if !result.At.IsZero() {
		update = update.SetLastSyncedAt(result.At)
	}

	if err := update.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("project: recording a sync for %d: %w", id, err)
	}

	// The history row is written after the project's own latest outcome,
	// deliberately. That row is what a badge and a playbook lookup read, so
	// it is the one that must be right if only one of the two lands; a
	// missing history entry is an observational gap, while a project left
	// reading running would be a project nobody could sync again. The error
	// is still returned rather than swallowed, so the gap is logged.
	if err := s.recordHistory(ctx, id, result); err != nil {
		return err
	}
	return nil
}

// recordHistory appends one completed attempt to a project's history.
//
// A run with no start time recorded falls back to the finish, which is what
// a caller that bypassed the runner produces: a zero start would otherwise
// render as an attempt that began in year one and ran for two millennia.
func (s *entStore) recordHistory(ctx context.Context, id int, result Result) error {
	// Only terminal attempts are history. A caller recording anything else
	// is describing a project's state rather than an attempt that finished.
	if result.Status != SyncSucceeded && result.Status != SyncFailed {
		return nil
	}

	finished := result.At
	if finished.IsZero() {
		finished = time.Now()
	}
	started := result.StartedAt
	if started.IsZero() {
		started = finished
	}

	err := s.client.SyncRun.Create().
		SetStatus(entsyncrun.Status(result.Status)).
		SetRevision(result.Revision).
		SetError(result.Err).
		SetStartedAt(started).
		SetFinishedAt(finished).
		SetProjectID(id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("project: recording sync history for %d: %w", id, err)
	}
	return nil
}

// ListSyncRuns returns a project's completed attempts, newest first.
func (s *entStore) ListSyncRuns(ctx context.Context, projectID, limit int) ([]SyncRun, error) {
	if limit <= 0 || limit > historyLimit {
		limit = historyLimit
	}

	rows, err := s.client.SyncRun.Query().
		Where(entsyncrun.HasProjectWith(entproject.IDEQ(projectID))).
		Order(ent.Desc(entsyncrun.FieldStartedAt), ent.Desc(entsyncrun.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("project: listing sync history for %d: %w", projectID, err)
	}

	out := make([]SyncRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, SyncRun{
			ID:         row.ID,
			Status:     SyncStatus(row.Status),
			Revision:   row.Revision,
			Err:        row.Error,
			StartedAt:  row.StartedAt,
			FinishedAt: row.FinishedAt,
		})
	}
	return out, nil
}

// BeginSync claims a project for an asynchronous sync, moving it to running
// and returning the record to hand to the syncer.
//
// The move is a compare-and-swap: the update matches only a project that is
// NOT already running, so two presses of Sync, or two replicas racing on
// one request, cannot both start a clone into the same working tree. A
// caller that loses the race is told ErrSyncInProgress rather than silently
// starting a second one. Syncability is checked first so an unfetchable
// project is refused synchronously, on the control that caused it, instead
// of being claimed and failed in the background where nobody is looking.
func (s *entStore) BeginSync(ctx context.Context, id int) (Project, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if !p.Syncable() {
		return Project{}, ErrNotSyncable
	}

	n, err := s.client.Project.Update().
		Where(
			entproject.IDEQ(id),
			entproject.SyncStatusNEQ(entproject.SyncStatus(SyncRunning)),
		).
		SetSyncStatus(entproject.SyncStatus(SyncRunning)).
		SetSyncError("").
		Save(ctx)
	if err != nil {
		return Project{}, fmt.Errorf("project: claiming a sync for %d: %w", id, err)
	}
	if n == 0 {
		return Project{}, ErrSyncInProgress
	}

	p.SyncStatus = SyncRunning
	p.SyncError = ""
	return p, nil
}

// ResetInterruptedSyncs clears syncs a process restart left mid-flight,
// moving every running project to failed. It is meant to run once at
// startup: a sync runs in memory, so a running row at boot is a clone whose
// process is gone, and leaving it running would refuse every future Sync
// (BeginSync's compare-and-swap would never match). Marking it failed is
// both honest and the state from which a Sync is offered again.
//
// It returns how many it cleared, for the startup log. A completing sync on
// another live replica that this resets is corrected when that replica's own
// RecordSync writes the real outcome, which addresses the row by id rather
// than by a status it must still hold.
func (s *entStore) ResetInterruptedSyncs(ctx context.Context) (int, error) {
	n, err := s.client.Project.Update().
		Where(entproject.SyncStatusEQ(entproject.SyncStatus(SyncRunning))).
		SetSyncStatus(entproject.SyncStatus(SyncFailed)).
		SetSyncError("The sync was interrupted by a restart. Sync again to retry.").
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("project: resetting interrupted syncs: %w", err)
	}
	return n, nil
}

// hydrate projects a row, carrying the organization's name across so a list
// can render it without a second query.
func hydrate(row *ent.Project) Project {
	p := Project{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		SCMType:     SCMType(row.ScmType),
		SCMURL:      row.ScmURL,
		SCMBranch:   row.ScmBranch,
		LocalPath:   row.LocalPath,
		Revision:    row.Revision,
		SyncStatus:  SyncStatus(row.SyncStatus),
		SyncError:   row.SyncError,
	}
	if row.LastSyncedAt != nil {
		at := *row.LastSyncedAt
		p.LastSyncedAt = &at
	}
	if org := row.Edges.Organization; org != nil {
		p.OrganizationID, p.OrganizationName = org.ID, org.Name
	}
	if cred := row.Edges.Credential; cred != nil {
		p.CredentialID = cred.ID
	}
	return p
}
