package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/google/uuid"
)

// Device holds the schema definition for the Device entity.
type Device struct {
	ent.Schema
}

// Mixin of the Device.
func (Device) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// newDeviceID generates the stable opaque identifier a new Device row
// gets by default. UUIDv7 is time-ordered, so a future keyset-paginated
// listing (Phase 7) can page on this column without a separate ordering
// key; NewV7 only errors on an entropy-source failure, in which case a
// random v4 is still a valid, if non-monotonic, opaque identifier.
func newDeviceID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.New().String()
}

// Fields of the Device.
func (Device) Fields() []ent.Field {
	return []ent.Field{
		// device_id is the stable, opaque identifier described by
		// pkg/inventory.DeviceID. It is distinct from the internal
		// auto-increment primary key (which stays a storage-layer detail
		// used only for edges/FKs) and from the mutable name, so renaming
		// a device or moving it between backends never breaks a wire
		// reference, a lock key, or a fact key that points at it.
		field.String("device_id").
			Immutable().
			Unique().
			NotEmpty().
			DefaultFunc(newDeviceID),
		field.String("name").Unique().NotEmpty(),
		// type is the classification key the factory registry is keyed on
		// (e.g. "cisco_router"), matching record.Record.Type. It is
		// Immutable for the same reason device_id is: nothing in the
		// domain's base InventoryItem contract exposes a way to change a
		// device's classification after construction (there is no
		// Type() accessor to read from, let alone a setter), so the
		// schema encodes that same guarantee rather than leaving it as
		// an unenforced convention. Before this column, the ent adapter
		// read this value out of the mutable properties JSONB column
		// (dev.Properties["type"]), the load-bearing hole the chain audit
		// found: AddInfo("type", ..., true), a legal call through the
		// base contract every caller has, silently reclassified a device
		// on its next load. The file-backed adapter never shared this
		// defect; HostSpec already carries Type as a first-class field
		// kept out of Properties, which this column now matches.
		field.String("type").Immutable().NotEmpty(),
		// properties holds the dynamic Document schema for the Factory
		field.JSON("properties", map[string]interface{}{}).Optional(),
		// version is the optimistic-concurrency token. A write supplies the
		// version it read; the update is conditional on the stored value
		// still matching, so two writers racing on one device cannot
		// silently lose one of their changes. Without this column every
		// hydrated item started at version 0 forever and the whole
		// Section 1 versioning contract was unenforceable.
		field.Uint64("version").Default(0),
		// state is the lifecycle state (pkg/inventory.LifecycleState) as a
		// string, so a new state can be added without a schema migration
		// and an unrecognized value can be surfaced rather than coerced.
		// Before this column every hydrated device was hardcoded active.
		field.String("state").Default("active").NotEmpty(),
		// source names the sync plugin that authoritatively owns this
		// device's data (Section 11's One Authority Per Item), so
		// reconciliation on re-sync can tell which source is allowed to
		// override which fields.
		field.String("source").Optional(),
		// source_synced_at is when that source last synced this device,
		// separate from updated_at (mixin): updated_at moves on any write
		// to the row, including one this platform made itself, while
		// source_synced_at only moves when the authoritative source
		// actually re-confirmed the data.
		field.Time("source_synced_at").Optional().Nillable(),
		// tags carries classification labels forward for dynamic grouping
		// (Section 22.3 Smart Inventories). Before this column, Tags()
		// always returned an empty slice for every ent-backed device,
		// because there was nowhere to persist what a sync plugin reported.
		field.JSON("tags", []string{}).Optional(),
	}
}

// Edges of the Device.
func (Device) Edges() []ent.Edge {
	return []ent.Edge{
		// Self-referencing graph edge for network topology (Parent -> Child)
		edge.To("children", Device.Type).
			From("parent").
			Unique(),
		// A device has many immutable historical facts
		edge.To("facts", Fact.Type),
		// A device has many immutable revisions, its audit trail.
		edge.To("revisions", Revision.Type),
		// A device can belong to any number of overlapping groups
		// (Section 3: a device can live in us-east/prod AND databases AND
		// linux-servers at once). Group owns this edge; this is the Ref
		// side.
		edge.From("groups", Group.Type).
			Ref("devices"),
		// A device optionally belongs to one Organization, the Section 18
		// tenancy boundary. Optional rather than Required: no RBAC
		// consumer exists yet (Phase 8), and requiring it today would
		// break every existing device-creation call site, none of which
		// have any concept of organizations. Organization owns this edge;
		// this is the Ref side.
		edge.From("organization", Organization.Type).
			Ref("devices").
			Unique(),
		// Inventories this device is attached to directly, with no
		// intervening group -- the "ungrouped hosts" case every real
		// inventory eventually has. A device reachable through a group in
		// the same inventory does not need this edge; it exists so a
		// device can be in an inventory without inventing a group to hold
		// it (Inventory.devices is the owning side).
		edge.From("inventories", Inventory.Type).
			Ref("devices"),
	}
}

// Indexes of the Device.
func (Device) Indexes() []ent.Index {
	return []ent.Index{
		// device_id is looked up on every Save and GetByName call, and is
		// the column a future keyset-paginated listing (Phase 7) will page
		// on, so it needs an index beyond the uniqueness constraint alone
		// guarantees.
		index.Fields("device_id"),
	}
}
