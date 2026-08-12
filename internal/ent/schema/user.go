package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

// Mixin of the User.
func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the User.
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").Unique().NotEmpty(),
	}
}

// Edges of the User.
//
// There used to be a "role" string field here. It had zero real consumers
// (nothing in this codebase ever constructed an ent User, let alone read
// its role) and it is the literal orphaned-permission anti-pattern PLAN.md
// Section 18.2 forbids: "Roles are assigned to Teams, never directly to
// Users." It was removed, not deprecated in place, in favor of the teams
// edge below; a grant now lives on a Team's RoleBinding rows, so removing a
// User from a Team revokes every permission that membership carried.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		// A User can belong to any number of Teams. Team owns this edge;
		// this is the Ref side.
		edge.From("teams", Team.Type).
			Ref("users"),
	}
}
