package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Revision holds the schema definition for one entry in a Device's audit
// trail. It is the persisted form of inventory.Revision, which until now
// existed only in memory and was discarded when the process exited.
//
// This is a table rather than a JSON column on Device deliberately. The
// questions it exists to answer are cross-device and cross-time ("what
// changed on any device in this group in the last 7 days", "which devices
// had field X change"), and a JSON blob cannot be indexed for those.
type Revision struct {
	ent.Schema
}

// Mixin of the Revision.
//
// created_at will always equal changed_at: a Revision is only ever
// created once and never updated, so the two fields are redundant for
// this schema specifically. The mixin is still applied, for the same
// reason every other schema gets it: uniform column names let a Memento
// point-in-time query or a retention job iterate every table the same
// way, without a per-table exception to remember.
func (Revision) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Revision.
func (Revision) Fields() []ent.Field {
	return []ent.Field{
		// version is the owning Device's version *after* this change. It
		// is monotonic per device, so ordering by it reconstructs history
		// without relying on wall-clock time, which can move backward.
		field.Uint64("version").Immutable(),

		// changed_at is for humans and for freshness windows, never for
		// ordering. Use version for ordering.
		field.Time("changed_at").Immutable(),

		// field_name is the property key that changed. Named field_name
		// rather than field because ent generates a FieldField constant
		// from it, and FieldFieldName reads less badly than FieldField.
		field.String("field_name").NotEmpty().Immutable(),

		// old_value and new_value are JSON because a property value is
		// deliberately heterogeneous (inventory.PropertyValue is any).
		// JSON keeps them queryable in Postgres, which a bytes column
		// would not. A removal records new_value as null.
		field.JSON("old_value", new(any)).Optional().Immutable(),
		field.JSON("new_value", new(any)).Optional().Immutable(),
	}
}

// Edges of the Revision.
func (Revision) Edges() []ent.Edge {
	return []ent.Edge{
		// A Revision MUST belong to exactly one device.
		edge.From("device", Device.Type).
			Ref("revisions").
			Unique().
			Required(),
	}
}

// Indexes of the Revision.
func (Revision) Indexes() []ent.Index {
	return []ent.Index{
		// The audit trail is almost always read as "this device's history,
		// in order", so index the pair rather than each column alone.
		index.Fields("version").Edges("device"),
		// "which devices changed field X" is the drift and CVE census
		// question, and it reads across devices.
		index.Fields("field_name"),
	}
}
