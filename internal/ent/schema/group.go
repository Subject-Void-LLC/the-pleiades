package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Group holds the schema definition for the Group entity: the substrate
// PLAN.md Section 3 (inventory group nesting) and Section 18 (group-scoped
// RBAC) both need. A device can live in any number of overlapping groups
// (a device can be in us-east/prod AND databases AND linux-servers at
// once), and a group can itself nest under more than one parent group, the
// same way an Ansible inventory group can appear under more than one
// parent in a "children:" block.
type Group struct {
	ent.Schema
}

// Mixin of the Group.
func (Group) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Group.
func (Group) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
	}
}

// Edges of the Group.
func (Group) Edges() []ent.Edge {
	return []ent.Edge{
		// Many-to-many: a group contains any number of devices, and a
		// device belongs to any number of groups (Device.groups is the
		// Ref side).
		edge.To("devices", Device.Type),
		// Self-referencing many-to-many, deliberately not .Unique() on
		// either side: group nesting is a DAG, not a tree, since a group
		// can have more than one parent group. This mirrors how
		// internal/engine/level_iterator.go already generalizes to
		// DAG.Adjacency's multi-parent shape rather than assuming a
		// linked list, wherever the domain genuinely allows more than one
		// parent.
		edge.To("children", Group.Type).
			From("parents"),
		// Inventories containing this group. Many-to-many rather than one
		// owner, because a group legitimately belongs to more than one:
		// "database servers" appears in both the DBA team's inventory and
		// the platform team's, and duplicating it would be two things to
		// keep in step (Inventory.groups is the owning side).
		edge.From("inventories", Inventory.Type).
			Ref("groups"),
	}
}
