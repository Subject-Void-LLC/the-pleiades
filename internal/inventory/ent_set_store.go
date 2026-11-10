package inventory

import (
	"context"
	"errors"
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
	if err := checkSetText(set, nil); err != nil {
		return Set{}, err
	}
	if set.OrganizationID == 0 {
		// Refused rather than defaulted. A set with no organization would
		// resolve against no organization scope, and whether that made it
		// reachable by everyone or by nobody would depend on which way the
		// resolver failed -- neither is an acceptable answer to arrive at
		// by accident.
		return Set{}, ErrSetOrganizationRequired
	}
	exists, err := s.client.Organization.Query().Where(entorg.IDEQ(set.OrganizationID)).Exist(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("inventory: checking organization %d: %w", set.OrganizationID, err)
	}
	if !exists {
		// Checked before the insert, because the foreign key failure the
		// insert would hit is a constraint error, which reads below as a
		// name already taken.
		return Set{}, fmt.Errorf("%w: %d", ErrUnknownOrganization, set.OrganizationID)
	}

	set.GroupIDs, set.DeviceIDs = distinctIDs(set.GroupIDs), distinctIDs(set.DeviceIDs)
	if err := checkMembers(ctx, s.client, set.OrganizationID, set.GroupIDs, set.DeviceIDs); err != nil {
		return Set{}, err
	}

	builder := s.client.Inventory.Create().
		SetName(set.Name).
		SetDescription(set.Description).
		SetOrganizationID(set.OrganizationID).
		SetOwner(set.Owner).
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

// ErrCrossTenantMember is returned when an inventory is asked to contain a
// group or device belonging to another organization.
var ErrCrossTenantMember = errors.New("inventory member belongs to another organization")

// assertMembersInOrganization returns a refusal (ErrCrossTenantMember) for
// each kind of member, devices and groups, that crosses a tenancy boundary,
// running both queries whatever the first finds, and an error only when a
// query fails.
//
// This closes a real hole rather than tidying an edge case, and it is worth
// stating plainly because the hole is not obvious from either side on its
// own. An inventory is a *grant surface*: sharing one with a team is a
// RoleBinding at inventory scope, and the resolver then treats every device
// reachable through that inventory as in scope. So without this check, a
// caller holding inventory:write in their own tenant could create an
// inventory in their own organization, list another tenant's device ids as
// its members, share it with their own team, and be legitimately authorized
// against hosts they were never granted -- with every individual step
// passing its own permission check.
//
// The membership write is where it has to be stopped, because by the time
// the resolver sees it the containment is a fact and resolving it is
// exactly the correct behaviour.
//
// Devices with no organization are allowed deliberately. A single-tenant
// deployment has never populated the device -> organization edge, and
// refusing those would make this feature unusable for the deployments most
// likely to try it first; they belong to no tenant, so admitting them
// crosses no boundary.
func assertMembersInOrganization(ctx context.Context, db *ent.Client, orgID int, groupIDs, deviceIDs []int) ([]error, error) {
	var refusals []error
	if len(deviceIDs) > 0 {
		foreign, err := db.Device.Query().
			Where(
				entdevice.IDIn(deviceIDs...),
				entdevice.HasOrganization(),
				entdevice.Not(entdevice.HasOrganizationWith(entorg.IDEQ(orgID))),
			).
			Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory: checking device tenancy: %w", err)
		}
		if foreign > 0 {
			// The count, never the ids. Reporting which devices belong to
			// somebody else would answer a question the caller is not
			// entitled to ask, on the exact request where they tried.
			refusals = append(refusals, fmt.Errorf("%w: %d device(s)", ErrCrossTenantMember, foreign))
		}
	}

	if len(groupIDs) > 0 {
		// A group has no organization edge of its own, so its tenancy is
		// its devices'. A group holding a foreign device would smuggle
		// that device in exactly the way the direct check above prevents.
		foreign, err := db.Group.Query().
			Where(
				entgroup.IDIn(groupIDs...),
				entgroup.HasDevicesWith(
					entdevice.HasOrganization(),
					entdevice.Not(entdevice.HasOrganizationWith(entorg.IDEQ(orgID))),
				),
			).
			Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory: checking group tenancy: %w", err)
		}
		if foreign > 0 {
			refusals = append(refusals, fmt.Errorf("%w: %d group(s) contain devices from another organization", ErrCrossTenantMember, foreign))
		}
	}
	return refusals, nil
}

// SetsForDevice returns every Set a device is reachable through.
//
// Both routes count: attached directly, or through any group that contains
// it. This is what fills ScopeTarget.InventoryIDs, so an omission here
// silently denies a legitimate share and an over-inclusion silently grants
// one -- which is why it is one query in one place rather than something
// each caller assembles.
//
// A device that belongs to an organization is reachable only through that
// organization's inventories. A device unowned when an inventory took it in
// can later be put in another organization (AssignDeviceOrganization), and a
// group can later gain another organization's device; the stored membership
// stays, since an update no longer re-checks members it does not add, but it
// grants nothing here (and Set.Selector keeps it out of every job). The
// device and its inventories are read in two statements, so a device moved
// between them is answered once by its old organization's inventories; no
// production path reads this yet. An unowned device is reachable through any inventory
// holding it, as the membership rule admits it.
func (s *entSetStore) SetsForDevice(ctx context.Context, deviceID int) ([]int, error) {
	device, err := s.client.Device.Query().Where(entdevice.IDEQ(deviceID)).WithOrganization().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inventory: resolving sets for device %d: %w", deviceID, err)
	}
	query := s.client.Inventory.Query().
		Where(
			entinventory.Or(
				entinventory.HasDevicesWith(entdevice.IDEQ(deviceID)),
				entinventory.HasGroupsWith(entgroup.HasDevicesWith(entdevice.IDEQ(deviceID))),
			),
		)
	if device.Edges.Organization != nil {
		query = query.Where(entinventory.HasOrganizationWith(entorg.IDEQ(device.Edges.Organization.ID)))
	}
	ids, err := query.IDs(ctx)
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
		Owner:       row.Owner,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.Edges.Organization != nil {
		set.OrganizationID = row.Edges.Organization.ID
		set.OrganizationName = row.Edges.Organization.Name
	}
	for _, g := range row.Edges.Groups {
		set.GroupIDs = append(set.GroupIDs, g.ID)
	}
	for _, d := range row.Edges.Devices {
		set.DeviceIDs = append(set.DeviceIDs, d.ID)
	}
	return set
}

// ListOrganizations reads every organization, for a form that has to offer
// one as a choice.
//
// Unpaged, deliberately. Organizations are the coarsest boundary this
// platform has -- a handful per deployment, not a fleet -- and paging a
// <select> would mean a control that silently cannot reach the tenant
// somebody wants. If a deployment ever has enough for this to matter, the
// control needs to become a search rather than this needing a cursor.
func (s *entSetStore) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.client.Organization.Query().
		Order(ent.Asc(entorg.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing organizations: %w", err)
	}

	out := make([]Organization, 0, len(rows))
	for _, row := range rows {
		out = append(out, Organization{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

// maxMemberChoices bounds ListMembers when a caller asks for no ceiling of
// its own. Deliberately larger than a page and far smaller than a fleet: it
// is what a <select multiple> can render without becoming unusable, not
// what the database can return.
const maxMemberChoices = 500

// ListMembers returns the devices and groups a membership control may
// offer. See SetStore.ListMembers for why it is bounded.
func (s *entSetStore) ListMembers(ctx context.Context, limit int) (Members, error) {
	if limit <= 0 || limit > maxMemberChoices {
		limit = maxMemberChoices
	}

	// One more than asked for, so truncation is observed rather than
	// inferred from a full page, which is the same trick the list readers
	// in internal/ui use for a next cursor.
	devices, err := s.client.Device.Query().
		Order(ent.Asc(entdevice.FieldName)).
		Limit(limit + 1).
		All(ctx)
	if err != nil {
		return Members{}, fmt.Errorf("inventory: listing devices for a membership control: %w", err)
	}
	groups, err := s.client.Group.Query().
		Order(ent.Asc(entgroup.FieldName)).
		Limit(limit + 1).
		All(ctx)
	if err != nil {
		return Members{}, fmt.Errorf("inventory: listing groups for a membership control: %w", err)
	}

	out := Members{Truncated: len(devices) > limit || len(groups) > limit}
	if len(devices) > limit {
		devices = devices[:limit]
	}
	if len(groups) > limit {
		groups = groups[:limit]
	}
	for _, row := range devices {
		out.Devices = append(out.Devices, Member{ID: row.ID, Name: row.Name})
	}
	for _, row := range groups {
		out.Groups = append(out.Groups, Member{ID: row.ID, Name: row.Name})
	}
	return out, nil
}
