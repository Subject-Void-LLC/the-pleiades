package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entgroup "github.com/Subject-Void-LLC/the-pleiades/internal/ent/group"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/ent/inventory"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
)

// defaultSetPageSize bounds a List with no explicit limit. It is the
// store's, never the caller's: a page size read from a query parameter with
// no ceiling is a request to hold every row in memory.
const defaultSetPageSize = 50

// maxSetPageSize is the ceiling a caller cannot raise.
const maxSetPageSize = 200

// entSetStore is the ent-backed SetStore.
type entSetStore struct{ client *ent.Client }

// NewEntSetStore builds a SetStore over an already-open ent client.
func NewEntSetStore(client *ent.Client) SetStore { return &entSetStore{client: client} }

// Create persists a new Set.
func (s *entSetStore) Create(ctx context.Context, set Set) (Set, error) {
	if strings.TrimSpace(set.Name) == "" {
		return Set{}, fmt.Errorf("inventory: a set needs a name")
	}
	if set.OrganizationID == 0 {
		// Refused rather than defaulted. A set with no organization would
		// resolve against no organization scope, and whether that made it
		// reachable by everyone or by nobody would depend on which way the
		// resolver failed -- neither is an acceptable answer to arrive at
		// by accident.
		return Set{}, fmt.Errorf("inventory: a set must belong to an organization")
	}

	builder := s.client.Inventory.Create().
		SetName(set.Name).
		SetDescription(set.Description).
		SetOrganizationID(set.OrganizationID).
		AddGroupIDs(set.GroupIDs...).
		AddDeviceIDs(set.DeviceIDs...)

	created, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			// The composite unique index on (name, organization) is what
			// this reports. Mapped to a domain error so a caller answers
			// 409 rather than 500 for what is a user's mistake.
			return Set{}, fmt.Errorf("%w: %q", ErrSetExists, set.Name)
		}
		return Set{}, fmt.Errorf("inventory: creating set %q: %w", set.Name, err)
	}
	return s.Get(ctx, created.ID)
}

// Get loads one Set with its membership.
func (s *entSetStore) Get(ctx context.Context, id int) (Set, error) {
	row, err := s.client.Inventory.Query().
		Where(entinventory.IDEQ(id)).
		WithOrganization().
		WithOwner().
		WithGroups().
		WithDevices().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Set{}, fmt.Errorf("%w: %d", ErrSetNotFound, id)
		}
		return Set{}, fmt.Errorf("inventory: loading set %d: %w", id, err)
	}
	return hydrateSet(row), nil
}

// List returns a page of Sets.
func (s *entSetStore) List(ctx context.Context, q SetQuery) ([]Set, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSetPageSize
	}
	limit = min(limit, maxSetPageSize)

	query := s.client.Inventory.Query().
		WithOrganization().
		WithOwner().
		WithGroups().
		WithDevices().
		Order(ent.Asc(entinventory.FieldID)).
		Limit(limit)

	// An empty OrganizationIDs means no restriction, and that is the
	// caller's decision rather than this store's: authorization is the
	// admission chain's job, and a port that filtered on its own would be
	// a second place answering the same question.
	if len(q.OrganizationIDs) > 0 {
		query = query.Where(entinventory.HasOrganizationWith(entorg.IDIn(q.OrganizationIDs...)))
	}
	if q.After > 0 {
		query = query.Where(entinventory.IDGT(q.After))
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		query = query.Where(entinventory.NameContainsFold(search))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing sets: %w", err)
	}

	out := make([]Set, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateSet(row))
	}
	return out, nil
}

// Update saves a Set's editable fields and replaces its membership.
//
// Membership is replaced rather than merged, because the caller submits the
// whole list: a merge would make removing the last device impossible, since
// an empty submission would be indistinguishable from "no change".
func (s *entSetStore) Update(ctx context.Context, set Set) error {
	err := s.client.Inventory.UpdateOneID(set.ID).
		SetName(set.Name).
		SetDescription(set.Description).
		ClearGroups().
		ClearDevices().
		AddGroupIDs(set.GroupIDs...).
		AddDeviceIDs(set.DeviceIDs...).
		Exec(ctx)
	switch {
	case ent.IsNotFound(err):
		return fmt.Errorf("%w: %d", ErrSetNotFound, set.ID)
	case ent.IsConstraintError(err):
		return fmt.Errorf("%w: %q", ErrSetExists, set.Name)
	case err != nil:
		return fmt.Errorf("inventory: updating set %d: %w", set.ID, err)
	}
	return nil
}

// Delete removes a Set.
//
// The devices and groups it referenced are untouched. An inventory is a
// view onto the fleet, not its owner, and the join rows cascade while the
// rows they pointed at do not -- deleting a shared collection must never
// delete somebody's servers.
func (s *entSetStore) Delete(ctx context.Context, id int) error {
	err := s.client.Inventory.DeleteOneID(id).Exec(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: %d", ErrSetNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("inventory: deleting set %d: %w", id, err)
	}
	return nil
}

// SetsForDevice returns every Set a device is reachable through.
//
// Both routes count: attached directly, or through any group that contains
// it. This is what fills ScopeTarget.InventoryIDs, so an omission here
// silently denies a legitimate share and an over-inclusion silently grants
// one -- which is why it is one query in one place rather than something
// each caller assembles.
func (s *entSetStore) SetsForDevice(ctx context.Context, deviceID int) ([]int, error) {
	ids, err := s.client.Inventory.Query().
		Where(
			entinventory.Or(
				entinventory.HasDevicesWith(entdevice.IDEQ(deviceID)),
				entinventory.HasGroupsWith(entgroup.HasDevicesWith(entdevice.IDEQ(deviceID))),
			),
		).
		IDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: resolving sets for device %d: %w", deviceID, err)
	}
	return ids, nil
}

// hydrateSet turns a loaded row into the domain type.
func hydrateSet(row *ent.Inventory) Set {
	set := Set{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.Edges.Organization != nil {
		set.OrganizationID = row.Edges.Organization.ID
	}
	if row.Edges.Owner != nil {
		set.Owner = row.Edges.Owner.Email
	}
	for _, g := range row.Edges.Groups {
		set.GroupIDs = append(set.GroupIDs, g.ID)
	}
	for _, d := range row.Edges.Devices {
		set.DeviceIDs = append(set.DeviceIDs, d.ID)
	}
	return set
}
