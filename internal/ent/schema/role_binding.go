package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RoleBinding holds the schema definition for the RoleBinding entity:
// PLAN.md Section 18.4's scope-bound grant, "a Team is granted a role
// specifically for" a System, Organization, Group, or Device scope. A Team
// can hold any number of these, one per (role, scope) combination it has
// been granted or explicitly denied.
type RoleBinding struct {
	ent.Schema
}

// Mixin of the RoleBinding.
func (RoleBinding) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the RoleBinding.
func (RoleBinding) Fields() []ent.Field {
	return []ent.Field{
		// role is a plain string, not an ent enum, matching this codebase's
		// existing convention (Device.state's own doc comment: "so a new
		// state can be added without a schema migration and an unrecognized
		// value can be surfaced rather than coerced"). internal/auth.Role
		// is the typed Go enum that actually validates it before a write.
		field.String("role").NotEmpty(),
		// scope_type names which of the four PLAN.md Section 18.4 scopes
		// this binding applies at: "system", "organization", "group", or
		// "device".
		field.String("scope_type").NotEmpty(),
		// scope_id is the target Organization/Group/Device's int primary
		// key, nil for "system" scope (which has no target). It is a bare
		// polymorphic reference with no ent-level foreign key: ent has no
		// native support for an edge that points at one of several possible
		// types, and building that mechanism for a single caller would be
		// new infrastructure nothing else in this codebase needs. This is a
		// stated, honest gap: a scope_id can point at an Organization,
		// Group, or Device row that has since been deleted, with nothing at
		// the database layer to prevent or clean it up. The composite index
		// below exists for lookup performance, not integrity.
		field.Int("scope_id").Optional().Nillable(),
		// effect is "allow" or "deny". Deny exists specifically so a more
		// specific scope can revoke what a broader one granted (PLAN.md
		// Section 18.4: "A Deny at the device level always overrides an
		// Allow at the group level").
		field.String("effect").Default("allow").NotEmpty(),
	}
}

// Edges of the RoleBinding.
func (RoleBinding) Edges() []ent.Edge {
	return []ent.Edge{
		// A RoleBinding MUST belong to exactly one Team.
		edge.From("team", Team.Type).
			Ref("role_bindings").
			Unique().
			Required(),
	}
}

// Indexes of the RoleBinding.
func (RoleBinding) Indexes() []ent.Index {
	return []ent.Index{
		// Resolution walks "every binding targeting this scope", so the
		// pair is indexed together rather than each column alone.
		index.Fields("scope_type", "scope_id"),
	}
}
