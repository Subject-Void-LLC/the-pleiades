package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
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
//
// The local_credential edge below is not a counterexample to that, and the
// distinction is worth stating because this comment reads as "nothing
// belongs on User". A role is a GRANT, and a grant hung directly on a User
// outlives the Team membership that was supposed to bound it. A password is
// not a grant: it is proof of who the subject is, it carries no permission,
// and what a locally authenticated caller may do is still resolved entirely
// from the RoleBindings on their Teams. It is a separate ENTITY rather than
// a field for a different reason again, which internal/ent/schema/
// local_credential.go states in full: a hash column here would ride into
// every projection built from a User.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		// A User can belong to any number of Teams. Team owns this edge;
		// this is the Ref side.
		edge.From("teams", Team.Type).
			Ref("users"),
		// A User has at most one locally stored password. User owns this
		// edge; LocalCredential holds the Ref side.
		//
		// Cascade on delete, because a credential whose owner is gone is a
		// verifiable password for an identity that no longer exists.
		edge.To("local_credential", LocalCredential.Type).
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
