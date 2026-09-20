// This file is the ent-backed Store: launchables as rows.
//
// It is the one file in this package that touches a database, and it holds no
// secret material of any kind: a launchable row is a type, a name and a
// tenant, and nothing a credential could hide behind.
package launchable

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entlaunchable "github.com/Subject-Void-LLC/the-pleiades/internal/ent/launchable"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
)

// defaultPageSize bounds a List with no explicit limit, and maxPageSize is
// the ceiling a caller cannot raise. Both match internal/launch's own, since
// the listings appear side by side in the same pickers.
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

type entStore struct {
	client *ent.Client
}

// NewEntStore builds the ent-backed Store over client.
func NewEntStore(client *ent.Client) Store {
	return &entStore{client: client}
}

// Get returns one launchable by id.
func (s *entStore) Get(ctx context.Context, id int) (Target, error) {
	row, err := s.client.Launchable.Query().
		Where(entlaunchable.IDEQ(id)).
		WithOrganization().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Target{}, fmt.Errorf("%w: launchable %d", ErrNotFound, id)
		}
		return Target{}, fmt.Errorf("launchable: reading %d: %w", id, err)
	}
	return hydrate(row), nil
}

// List returns the launchables matching q.
func (s *entStore) List(ctx context.Context, q Query) ([]Target, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	query := s.client.Launchable.Query().WithOrganization()
	if q.OrganizationID > 0 {
		query = query.Where(entlaunchable.HasOrganizationWith(entorg.IDEQ(q.OrganizationID)))
	}
	if len(q.Types) > 0 {
		query = query.Where(entlaunchable.TypeIn(q.Types...))
	}

	rows, err := query.
		// Type then name, so a picker's groups arrive grouped and a listing
		// keeps its order between reads. The id is the tiebreaker, because
		// two targets of one type may share a name across tenants.
		Order(ent.Asc(entlaunchable.FieldType), ent.Asc(entlaunchable.FieldName), ent.Asc(entlaunchable.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("launchable: listing: %w", err)
	}

	out := make([]Target, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// hydrate projects a row onto a Target, carrying the organization's name
// across when the edge was loaded so a list can render it without a second
// query.
func hydrate(row *ent.Launchable) Target {
	t := Target{
		ID:   row.ID,
		Type: row.Type,
		Name: row.Name,
	}
	if org := row.Edges.Organization; org != nil {
		t.OrganizationID = org.ID
		t.OrganizationName = org.Name
	}
	return t
}
