package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Team holds the schema definition for the Team entity: PLAN.md Section 18.2's
// binding unit for RBAC. Roles are granted to Teams, never to a User
// directly, so removing a User from a Team revokes every permission that
// membership carried, instead of leaving an orphaned grant nobody remembers
// to clean up. Every Team belongs to exactly one Organization.
type Team struct {
	ent.Schema
}

// Mixin of the Team.
func (Team) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Team.
func (Team) Fields() []ent.Field {
	return []ent.Field{
		// name is deliberately not globally .Unique(): two different
		// Organizations both naming a team "platform" is a real, expected
		// case. Real uniqueness here is per-Organization, which ent cannot
		// express as a composite constraint without an edge-based
		// workaround; that is a stated, honest gap, not built, since no
		// caller needs it enforced yet.
		field.String("name").NotEmpty(),
	}
}

// Edges of the Team.
func (Team) Edges() []ent.Edge {
	return []ent.Edge{
		// A Team MUST belong to exactly one Organization. Organization owns
		// this edge; this is the Ref side.
		edge.From("organization", Organization.Type).
			Ref("teams").
			Unique().
			Required(),
		// A Team has any number of Users, and a User can belong to any
		// number of Teams. Team owns this edge; User holds the Ref side.
		edge.To("users", User.Type),
		// A Team's permissions live as RoleBinding rows, not as a role
		// string on the Team itself, so one Team can hold grants at several
		// different scopes at once (e.g. Viewer on the whole Organization
		// plus Operator on one Group).
		edge.To("role_bindings", RoleBinding.Type),
	}
}
