package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Contact holds the schema definition for the Contact entity: a named human
// or rota accountable for an Organization or a Team, and the way to reach
// them when that tenant's automation is doing something nobody expected at
// three in the morning.
//
// It is an entity rather than a pair of columns because the question it
// answers has more than one answer at a time. A tenant has an owner and an
// escalation path and, in a regulated deployment, a security officer and a
// billing contact, and each of those is a different person with a different
// number who changes on a different schedule. Flat columns force that into
// one row and make "who is the current escalation contact" unanswerable
// once the second one exists.
//
// It attaches to exactly one of an Organization or a Team, never both and
// never neither. That invariant is not expressible as an ent constraint and
// is enforced at the repository boundary, the same way RoleBinding's
// scope_id invariant is (FAILURE_PATTERNS.md #99): the lesson there was
// that a nullable reference nothing validates becomes a row that matches
// everything.
type Contact struct {
	ent.Schema
}

// Mixin of the Contact.
func (Contact) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Contact.
func (Contact) Fields() []ent.Field {
	return []ent.Field{
		// name is the person or the rota. A rota is the better answer and
		// the help text says so: an individual's name in an escalation
		// field is a page that goes unanswered the week they are on leave.
		field.String("name").NotEmpty(),

		// role is a plain string rather than an ent enum, following
		// RoleBinding.role's own reasoning: a new role can be added
		// without a schema migration, and an unrecognised value is
		// surfaced rather than coerced. access.ContactRole is the typed Go
		// enum that validates it before a write.
		field.String("role").NotEmpty(),

		// Every way of reaching them is optional individually, because
		// which ones exist varies: an air-gapped site may have a desk
		// phone and no reachable email, and a rota may be a URL with
		// neither. That at least one is present is a repository-level
		// check, not a column-level one, because no single column can say
		// it.
		field.String("email").Optional(),
		field.String("phone").Optional(),
		field.String("url").Optional(),

		// notes carries the conditions under which this contact is the
		// right one: "sev1 only after 22:00 UTC", "page the rota, never an
		// individual". Escalation information that is only a phone number
		// is escalation information somebody will misuse.
		field.String("notes").Optional(),

		// display_order lets an operator say which escalation contact is
		// tried first, which is the entire point of an escalation path and
		// cannot be inferred from a role or a creation timestamp.
		field.Int("display_order").Default(0),
	}
}

// Edges of the Contact.
func (Contact) Edges() []ent.Edge {
	return []ent.Edge{
		// Optional on both sides because exactly one of them is set, and
		// ent cannot express "exactly one of these two". Both are Ref
		// sides; Organization and Team own the edges.
		//
		// Cascade, not ent's default of setting the reference null. A
		// contact is a fact about its owner and has no meaning without
		// one, so nulling the reference would leave a row owned by
		// nothing, which is exactly the state the repository's
		// exactly-one-owner invariant exists to forbid. Deleting the owner
		// would quietly manufacture the invalid row the write path
		// refuses to accept.
		//
		// This is the first ON DELETE annotation in the schema and it is
		// deliberately not a new convention: Organization's devices edge
		// keeps ent's default because a device outlives the organization
		// it was assigned to (TestDeleteOrganization_LeavesItsDevicesAlone
		// asserts exactly that). The difference is ownership rather than
		// assignment. The annotation itself is declared on the owning
		// edges, in Organization and Team, because that is the side ent
		// builds the foreign key from.
		edge.From("organization", Organization.Type).
			Ref("contacts").
			Unique(),
		edge.From("team", Team.Type).
			Ref("contacts").
			Unique(),
	}
}

// Indexes of the Contact.
func (Contact) Indexes() []ent.Index {
	return []ent.Index{
		// Contacts are always read as "every contact for this owner, in
		// order", never individually, so the index matches the only query
		// that runs.
		index.Fields("role", "display_order"),
	}
}
