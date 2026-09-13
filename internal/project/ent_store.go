// This file is the ent-backed Store: projects as rows.
//
// It carries no secret material. A project names a Credential by id and the
// clone resolves it through credstore at sync time, so nothing here reads,
// writes or redacts one.
package project

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
)

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
	return nil
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
