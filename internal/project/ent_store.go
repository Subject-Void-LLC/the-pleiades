// This file is the ent-backed Store: projects as rows.
//
// It carries no secret material. A project names a Credential by id and the
// clone resolves it through credstore at sync time, so nothing here reads,
// writes or redacts one.
package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entlaunchable "github.com/Subject-Void-LLC/the-pleiades/internal/ent/launchable"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
	entsyncrun "github.com/Subject-Void-LLC/the-pleiades/internal/ent/syncrun"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// historyLimit bounds a sync history read when a caller names no limit.
const historyLimit = 50

// defaultPageSize bounds a List with no explicit limit. It is the store's,
// never the caller's: a page size read from a query parameter with no
// ceiling is a request to hold every row in memory.
const defaultPageSize = 100

// entStore is the ent-backed Store.
type entStore struct {
	client *ent.Client

	// policy is which sources a project may be pointed at. It lives here
	// rather than in the handlers because this store is the only thing that
	// writes the column: the API, the UI, every test and any future importer
	// all pass through Create and Update, so one check here is a check they
	// all get. The zero value refuses all but https and ssh.
	policy SourcePolicy

	// owner is the controller instance this store's claims are recorded as
	// belonging to (WithOwner). Empty records no owner.
	owner string
}

// StoreOption adjusts an entStore.
type StoreOption func(*entStore)

// WithOwner records instanceID as the owner of every sync this store claims,
// so that another controller's recovery can tell this one's live clones from
// a dead process's (ResetInterruptedSyncs). A controller passes its own
// heartbeat instance id.
func WithOwner(instanceID string) StoreOption {
	return func(s *entStore) { s.owner = instanceID }
}

// NewEntStore returns a Store over the given client, accepting only the
// sources policy admits.
func NewEntStore(client *ent.Client, policy SourcePolicy, opts ...StoreOption) Store {
	s := &entStore{client: client, policy: policy}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// checkSource refuses a source this deployment will not fetch from, and a URL
// carrying a password.
//
// Only a git project is checked, because only a git project is ever dialed.
// The predicate itself deliberately does not look at the type (see
// source.go), so the day the archive type ships its URL gets the same
// treatment by moving this one condition.
func (s *entStore) checkSource(p Project) error {
	if p.SCMType != SCMGit {
		return nil
	}
	if err := s.policy.AdmitsSource(p.SCMURL); err != nil {
		return err
	}
	if HasEmbeddedSecret(p.SCMURL) {
		// Refused at the write only. See ErrSourceSecretInURL for why an
		// existing row that already carries one keeps working.
		return fmt.Errorf("%w: give the credential to this project instead, where it is encrypted", ErrSourceSecretInURL)
	}
	return nil
}

// Create stores a new project.
func (s *entStore) Create(ctx context.Context, p Project) (Project, error) {
	// Refused before anything is written, so a source this deployment will
	// not fetch from never becomes a row somebody has to find later.
	if err := s.checkSource(p); err != nil {
		return Project{}, err
	}

	// The project and the launchable row standing for it are written
	// together. That row is what a schedule points at, so a project created
	// without one could never be scheduled, and a crash between two separate
	// writes would leave exactly that with nothing to explain it.
	//
	// The transaction is opened before the builder, deliberately: a builder
	// made from the client writes outside the transaction it appears to be
	// inside, which is the shape that looks atomic in a diff and is not.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return Project{}, fmt.Errorf("project: opening a transaction to create %q: %w", p.Name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	create := tx.Project.Create().
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

	if err := tx.Launchable.Create().
		SetType(launchable.TypeProject).
		SetName(row.Name).
		SetOrganizationID(p.OrganizationID).
		SetProjectID(row.ID).
		Exec(ctx); err != nil {
		return Project{}, fmt.Errorf("project: recording %d as launchable: %w", row.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return Project{}, fmt.Errorf("project: committing %q: %w", p.Name, err)
	}
	committed = true

	return s.Get(ctx, row.ID)
}

// Get returns one project.
func (s *entStore) Get(ctx context.Context, id int) (Project, error) {
	row, err := s.client.Project.Query().
		Where(entproject.IDEQ(id)).
		WithOrganization().
		WithCredential().
		// The launchable row standing for this project, so a caller holding a
		// Project can name it to a schedule without a second query.
		WithLaunchable().
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
		WithLaunchable().
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
	// Checked on every edit, not only at create: an edit can repoint a
	// project at another source, and a row stored before this rule existed is
	// corrected the first time somebody saves it.
	if err := s.checkSource(p); err != nil {
		return err
	}

	// One transaction, because the launchable row carries a copy of the name:
	// a rename that reached only one of the two would leave a schedule picker
	// offering a name this project no longer has.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("project: opening a transaction to update %d: %w", p.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	update := tx.Project.UpdateOneID(p.ID).
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

	if _, err := tx.Launchable.Update().
		Where(entlaunchable.HasProjectWith(entproject.IDEQ(p.ID))).
		SetName(p.Name).
		Save(ctx); err != nil {
		return fmt.Errorf("project: renaming the launchable row for %d: %w", p.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("project: committing the update of %d: %w", p.ID, err)
	}
	committed = true
	return nil
}

// ByLaunchable returns the project a launchable row stands for.
func (s *entStore) ByLaunchable(ctx context.Context, launchableID int) (Project, error) {
	row, err := s.client.Project.Query().
		Where(entproject.HasLaunchableWith(entlaunchable.IDEQ(launchableID))).
		WithOrganization().
		WithCredential().
		WithLaunchable().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("project: loading the project for launchable %d: %w", launchableID, err)
	}
	return hydrate(row), nil
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
		if ent.IsConstraintError(err) {
			// The sync history cascades and so does the launchable row, so
			// the only key that can refuse is a schedule's into that row.
			// Reported as in use rather than as a storage failure, because it
			// is a decision somebody has to make (the schedule) rather than a
			// fault: deleting a project out from under a schedule would stop
			// automation somebody relies on, and the deletion is the moment
			// to say so.
			return fmt.Errorf("%w: project %d is scheduled", ErrInUse, id)
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

// recordHistory writes one finished attempt into a project's history:
// finishing the row the claim opened, or appending a new one when there was
// no claim.
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

	if result.RunID > 0 {
		// The attempt already has a row, opened when it was claimed. It is
		// addressed by id AND by project, so a mismatched pair (a stale
		// claim, a caller passing somebody else's run) closes nothing
		// rather than stamping an outcome onto another project's history.
		n, err := s.client.SyncRun.Update().
			Where(
				entsyncrun.IDEQ(result.RunID),
				entsyncrun.HasProjectWith(entproject.IDEQ(id)),
			).
			SetStatus(entsyncrun.Status(result.Status)).
			SetRevision(result.Revision).
			SetError(result.Err).
			SetFinishedAt(finished).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("project: finishing sync history row %d for %d: %w", result.RunID, id, err)
		}
		if n == 0 {
			return fmt.Errorf("project: sync history row %d does not belong to project %d", result.RunID, id)
		}
		return nil
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
		run := SyncRun{
			ID:        row.ID,
			Status:    SyncStatus(row.Status),
			Revision:  row.Revision,
			Actor:     row.Actor,
			Err:       row.Error,
			StartedAt: row.StartedAt,
		}
		// A null finish is an attempt still running, which the domain reads
		// as the zero time (see SyncRun.Running).
		if row.FinishedAt != nil {
			run.FinishedAt = *row.FinishedAt
		}
		out = append(out, run)
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
// The claim and the history row it opens are written in one transaction, so
// there is no window in which a project reads running with no attempt to
// point at, or an attempt exists that no clone will ever finish.
func (s *entStore) BeginSync(ctx context.Context, id int, actor string) (Claim, error) {
	if strings.TrimSpace(actor) == "" {
		return Claim{}, ErrNoActor
	}

	p, err := s.Get(ctx, id)
	if err != nil {
		return Claim{}, err
	}
	if !p.Syncable() {
		return Claim{}, ErrNotSyncable
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("project: opening a transaction to claim a sync for %d: %w", id, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	n, err := tx.Project.Update().
		Where(
			entproject.IDEQ(id),
			entproject.SyncStatusNEQ(entproject.SyncStatus(SyncRunning)),
		).
		SetSyncStatus(entproject.SyncStatus(SyncRunning)).
		SetSyncError("").
		Save(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("project: claiming a sync for %d: %w", id, err)
	}
	if n == 0 {
		return Claim{}, ErrSyncInProgress
	}

	started := time.Now()
	create := tx.SyncRun.Create().
		SetStatus(entsyncrun.Status(SyncRunning)).
		SetActor(actor).
		SetStartedAt(started).
		SetProjectID(id)
	if s.owner != "" {
		create.SetOwnerInstance(s.owner)
	}
	run, err := create.Save(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("project: opening a sync history row for %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return Claim{}, fmt.Errorf("project: committing the claim of a sync for %d: %w", id, err)
	}
	committed = true

	p.SyncStatus = SyncRunning
	p.SyncError = ""
	return Claim{Project: p, RunID: run.ID, StartedAt: started}, nil
}

// ResetInterruptedSyncs fails every sync no live process is still running,
// and reports how many projects it moved to failed.
//
// A sync runs in memory, so a claim whose process is gone would stay running
// forever and refuse every future Sync (BeginSync's compare-and-swap would
// never match). Failing it is both honest and the state from which a Sync is
// offered again.
//
// What decides "gone" is the claim's owner. This used to fail every running
// sync, on the reasoning that it only ran at startup; but in a deployment of
// several controllers every replica's startup is somebody else's live clone,
// so each new pod of a rolling upgrade failed its peers' in-flight syncs and
// offered Sync again on a working tree still being cloned into
// (FAILURE_PATTERNS.md #278). Now an attempt is abandoned only when it has no
// recorded owner, or its owner is not in alive; and a project is failed only
// when no running attempt of its own has a live owner, which also covers a
// project left running with no attempt row at all.
//
// alive is the instance ids known to be running, the caller's own included.
// Nil means none, which is the right answer for a single process at startup:
// every claim is then abandoned, as before.
func (s *entStore) ResetInterruptedSyncs(ctx context.Context, alive []string) (int, error) {
	const reason = "The sync was interrupted by a restart. Sync again to retry."

	running := entsyncrun.StatusEQ(entsyncrun.Status(SyncRunning))

	// Every attempt no live process owns: all of them when nobody is alive.
	abandoned := running
	if len(alive) > 0 {
		abandoned = entsyncrun.And(running, entsyncrun.Or(
			entsyncrun.OwnerInstanceIsNil(),
			entsyncrun.OwnerInstanceNotIn(alive...),
		))
	}

	// Every running project no live process is syncing, decided inside this
	// one UPDATE. It used to read the live-synced projects first and exclude
	// them in a second statement, so a sync claimed between the two was failed
	// while its live owner was still cloning it (FAILURE_PATTERNS.md #283). In
	// one statement that cannot happen on either dialect: a project claimed
	// concurrently was not running in the statement's snapshot, so it never
	// matches, and a project that does match cannot be claimed meanwhile,
	// because BeginSync only claims a project that is not running. With nobody
	// alive the exclusion is left out, and every running project is failed.
	stuck := []predicate.Project{entproject.SyncStatusEQ(entproject.SyncStatus(SyncRunning))}
	if len(alive) > 0 {
		stuck = append(stuck, entproject.Not(entproject.HasSyncRunsWith(running, entsyncrun.OwnerInstanceIn(alive...))))
	}

	n, err := s.client.Project.Update().
		Where(stuck...).
		SetSyncStatus(entproject.SyncStatus(SyncFailed)).
		SetSyncError(reason).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("project: resetting interrupted syncs: %w", err)
	}

	// The history rows those claims opened are stranded the same way and for
	// the same reason, so they are failed in the same sweep. Without this a
	// history would show an attempt still cloning weeks after the process
	// that started it died, and Took would keep counting up.
	if _, err := s.client.SyncRun.Update().
		Where(abandoned).
		SetStatus(entsyncrun.Status(SyncFailed)).
		SetError(reason).
		SetFinishedAt(time.Now()).
		Save(ctx); err != nil {
		return 0, fmt.Errorf("project: failing interrupted sync history rows: %w", err)
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
	if l := row.Edges.Launchable; l != nil {
		p.LaunchableID = l.ID
	}
	return p
}
