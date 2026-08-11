package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Inventory holds the schema definition for the Inventory entity: a named,
// shareable set of devices that a runbook can be dispatched against.
//
// It is the container layer this codebase was missing. Group already models
// what an Ansible inventory's "children:" block does -- a nested, DAG-shaped
// set of devices -- but nothing above it said *whose* set that is, who may
// use it, or how one team lends a set of devices to another. Those are the
// questions an Inventory answers, and it is the same layer AWX puts at the
// top of Organization -> Inventory -> Group -> Host.
//
// Sharing is deliberately not modelled here. There is no shares edge and no
// access-control-list entity, because this codebase already has one
// authorization model and a second one would be a second place for the two
// to disagree. An inventory is shared by granting a Team a RoleBinding at
// scope_type "inventory" -- the same mechanism, the same resolver, and the
// same explicit-Deny-wins precedence that already governs organizations,
// groups and devices. See internal/auth.ScopeInventory.
type Inventory struct {
	ent.Schema
}

// Mixin of the Inventory.
func (Inventory) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Inventory.
func (Inventory) Fields() []ent.Field {
	return []ent.Field{
		// Unique per organization rather than globally: two tenants both
		// having a "production" inventory is the ordinary case, and a
		// global unique constraint would make the first one to claim a
		// common name deny it to everybody else. The composite index below
		// is what enforces it.
		field.String("name").NotEmpty(),
		field.String("description").Optional(),
	}
}

// Edges of the Inventory.
func (Inventory) Edges() []ent.Edge {
	return []ent.Edge{
		// An Inventory MUST belong to exactly one Organization: it is the
		// tenancy boundary, and an inventory belonging to none could be
		// resolved against no organization scope, which would make it
		// reachable either by everyone or by no one depending on which way
		// the resolver failed.
		edge.From("organization", Organization.Type).
			Ref("inventories").
			Unique().
			Required(),

		// The creator, kept because sharing is something a person does and
		// an audit trail of "who lent this out" is worth having. It is
		// optional and carries no permission of its own: a grant lives on a
		// Team's RoleBinding, never on a User directly (PLAN.md Section
		// 18.2), so this records authorship rather than authority.
		edge.From("owner", User.Type).
			Ref("owned_inventories").
			Unique(),

		// Groups this inventory contains. Many-to-many, because a group can
		// legitimately appear in more than one inventory -- "database
		// servers" belongs in both the DBA team's inventory and the
		// platform team's, and copying it would be two things to keep in
		// step.
		edge.To("groups", Group.Type),

		// Devices attached directly, with no intervening group, which is
		// the "ungrouped hosts" case every real inventory eventually has.
		edge.To("devices", Device.Type),
	}
}

// Indexes of the Inventory.
func (Inventory) Indexes() []ent.Index {
	return []ent.Index{
		// Name is unique within an organization, not globally. This is the
		// constraint, not merely a lookup index.
		index.Fields("name").
			Edges("organization").
			Unique(),
	}
}
