package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Organization holds the schema definition for the Organization entity:
// the PLAN.md Section 18 tenancy boundary, the highest multi-tenant scope
// a resource belongs to. Phase 1 added only the entity and its Device edge,
// as substrate; Phase 8 adds the Team edge, its own RBAC job.
type Organization struct {
	ent.Schema
}

// Mixin of the Organization.
func (Organization) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Organization.
func (Organization) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),

		// description is what this tenant actually is, in a sentence. A
		// list of organization names is unreadable at the point where
		// there are thirty of them and four are called some variation of
		// "platform".
		field.String("description").Optional(),

		// classification is this tenant's own marking, distinct from the
		// deployment-wide banner. A single installation can hold tenants
		// at different levels, and the banner alone cannot say which
		// tenant's data is on screen.
		//
		// A plain string rather than an ent enum, following
		// RoleBinding.role's reasoning. access.Classification is the typed
		// Go enum that validates it, and it accepts only the six
		// classification markings: the environment markings the banner
		// also renders (development, staging, production) are facts about
		// an installation, not about a tenant inside one.
		field.String("classification").Optional(),

		// change_window is when this tenant permits automation to run,
		// written for a human ("Sat 02:00-06:00 UTC") rather than parsed.
		// Nothing enforces it yet and the field's help text says so: it is
		// recorded here so the dispatcher has something to consult when a
		// phase owns enforcing it, rather than being invented at that
		// point with no history behind it.
		field.String("change_window").Optional(),

		// frozen is an operator-declared stop on this tenant. It is a
		// separate boolean rather than an absent change window because
		// "there is no declared window" and "there is a window and we are
		// deliberately not running" are different facts, and only the
		// second is a decision somebody made and can be asked about.
		field.Bool("frozen").Default(false),
		field.String("freeze_reason").Optional(),

		// External reference ids reconcile this row against whatever
		// system of record the customer already runs. They are opaque
		// here on purpose: validating somebody else's key format is a
		// promise about their system that this one cannot keep.
		field.String("cost_centre").Optional(),
		field.String("ticket_key").Optional(),
		field.String("cmdb_id").Optional(),

		// Attestation. Who confirmed this tenant's ownership and
		// escalation information is current, and when.
		//
		// This is the point of recording contacts at all. Contact details
		// decay silently: nothing breaks when an escalation number stops
		// working, right up until the moment it is needed, and a stale
		// record is worse than an empty one because it stops anybody
		// looking further. A dated attestation by a named subject is what
		// makes the difference between "we hold this information" and "we
		// know it is true", which is the claim an auditor is actually
		// asking about.
		field.String("attested_by").Optional(),
		field.Time("attested_at").Optional().Nillable(),
	}
}

// Edges of the Organization.
func (Organization) Edges() []ent.Edge {
	return []ent.Edge{
		// A device optionally belongs to one Organization (Device.
		// organization is the Ref side, and is what makes the edge
		// optional: .Required() is declared there, not here, and this
		// phase deliberately does not declare it).
		edge.To("devices", Device.Type),
		// A Team MUST belong to exactly one Organization (Team.organization
		// is the Ref side, .Required() declared there).
		edge.To("teams", Team.Type),
		// An Inventory MUST belong to exactly one Organization: it is the
		// tenancy boundary for a shareable set of devices (Inventory.
		// organization is the Ref side, .Required() declared there).
		edge.To("inventories", Inventory.Type),

		// The launch templates this tenant owns. Not cascaded: deleting an
		// organization with templates still in it is refused rather than
		// silently destroying the saved definitions of everything it runs,
		// the same posture its inventories get.
		edge.To("templates", Template.Type),

		// The schedules this tenant owns. Not cascaded, same posture as
		// templates and inventories: deleting an organization that still
		// schedules work is refused rather than silently stopping that
		// work, which is a change nobody would see until the run that did
		// not happen.
		edge.To("schedules", Schedule.Type),

		// The credential types this tenant defined. A MANAGED type has no
		// organization at all (the edge is optional on the other side), so
		// this holds only the custom ones somebody here wrote. Not
		// cascaded: deleting an organization with credential types still
		// bound to templates should fail rather than silently take the
		// bindings with it.
		edge.To("credential_types", CredentialType.Type),

		// The credentials this tenant owns. Not cascaded, and this one
		// matters more than the others: a cascade here would delete real
		// secret material as a side effect of an organization delete,
		// which is the kind of destruction that should require naming what
		// is being destroyed.
		edge.To("credentials", Credential.Type),
		// An Announcement optionally belongs to one Organization. The
		// absence is meaningful: no organization means system-wide, shown
		// to everybody, which is what a platform maintenance notice has to
		// be (Announcement.organization is the Ref side).
		edge.To("announcements", Announcement.Type),
		// A Contact optionally belongs to one Organization (Contact.
		// organization is the Ref side). Optional there because a Contact
		// attaches to exactly one of an Organization or a Team, which is
		// an invariant the repository enforces rather than the schema.
		edge.To("contacts", Contact.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
