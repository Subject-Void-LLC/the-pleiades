package access

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entgroup "github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/ent/inventory"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
)

// This file resolves a role binding's scope target to a name.
//
// It exists because a grant is the one row in this system whose target is a
// bare polymorphic reference: RoleBinding.scope_id is an integer with no
// foreign key, pointing at an Organization, an Inventory, a Group or a
// Device depending on the sibling scope_type column. That is a deliberate
// schema decision, recorded in the entity's own doc comment, because ent has
// no native edge that points at one of several types.
//
// The consequence is that no eager load can bring the name along, the way it
// does for every other reference in this package. The first version of the
// grants view concluded from that that the id was acceptable to render, and
// argued a per-row lookup would be too expensive. The first half was right.
// The conclusion did not follow: the answer to an expensive per-row join is
// a batch, not a primary key on the page. An auditor reading "operator,
// inventory 7, allow" cannot audit anything.
//
// One query per scope type present on the page, never per row. A page of
// fifty grants spanning all four levels costs four queries.

// resolveScopeNames fills in ScopeName for every binding whose target still
// exists.
//
// A target that has been deleted keeps an empty ScopeName, and the caller
// renders that as gone rather than as a blank. Deletion is genuinely
// possible here: nothing at the database layer stops an organization being
// removed while a grant still names it, which is the other half of the same
// missing foreign key.
func (s *entStore) resolveScopeNames(ctx context.Context, bindings []Binding) error {
	// Group the page's targets by what kind of record they point at, so
	// each kind costs one query regardless of how many rows name it.
	wanted := map[auth.ScopeType][]int{}
	for _, b := range bindings {
		if b.SystemWide() || b.ScopeID <= 0 {
			continue
		}
		wanted[b.ScopeType] = append(wanted[b.ScopeType], b.ScopeID)
	}
	if len(wanted) == 0 {
		return nil
	}

	names := map[auth.ScopeType]map[int]string{}
	for scopeType, ids := range wanted {
		found, err := s.namesForScope(ctx, scopeType, ids)
		if err != nil {
			return err
		}
		names[scopeType] = found
	}

	for i := range bindings {
		if byType, ok := names[bindings[i].ScopeType]; ok {
			bindings[i].ScopeName = byType[bindings[i].ScopeID]
		}
	}
	return nil
}

// namesForScope reads the names of one kind of target.
//
// An unrecognised scope type returns no names rather than an error. The
// write path already refuses to store one, so reaching this with an unknown
// type means a row predates a vocabulary change, and refusing to render the
// whole page because one row is odd would make a listing unreadable at
// exactly the moment somebody needs it to investigate.
func (s *entStore) namesForScope(ctx context.Context, scopeType auth.ScopeType, ids []int) (map[int]string, error) {
	out := map[int]string{}

	switch scopeType {
	case auth.ScopeOrganization:
		rows, err := s.client.Organization.Query().Where(entorg.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("access: naming organization scopes: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r.Name
		}
	case auth.ScopeInventory:
		rows, err := s.client.Inventory.Query().Where(entinventory.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("access: naming inventory scopes: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r.Name
		}
	case auth.ScopeGroup:
		rows, err := s.client.Group.Query().Where(entgroup.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("access: naming group scopes: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r.Name
		}
	case auth.ScopeDevice:
		rows, err := s.client.Device.Query().Where(entdevice.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("access: naming device scopes: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r.Name
		}
	}
	return out, nil
}
