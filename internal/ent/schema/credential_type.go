package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// CredentialType holds the schema definition for a credential type: the
// declaration of what a credential of that type holds and how those values
// reach a running job.
//
// PLAN.md Section 29 states the shape in one line: credential types are
// data. Both halves are data here, in two typed JSON columns, so an
// administrator adds a credential type through the API without a code
// change and without a migration.
//
// AWX_PARITY.md calls this the single biggest gap in the migration story,
// and the reason is concrete rather than architectural: a customer's
// playbook reads the environment variables their credential type injects,
// so a type that cannot be represented is a playbook that cannot run.
type CredentialType struct {
	ent.Schema
}

// Mixin of the CredentialType.
func (CredentialType) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the CredentialType.
func (CredentialType) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("description").Optional(),

		// kind is AWX's coarse grouping, and it is a plain string rather
		// than an ent enum for the identical reason Template.kind is: an
		// ent enum becomes a CHECK constraint in Postgres and a rewritten
		// table in SQLite, so adding a kind would mean a schema migration
		// in every deployment.
		//
		// The vocabulary is still closed at the write. credtype.Kind is a
		// twelve-value set and the store refuses anything outside it. The
		// difference is that the set lives in one Go declaration rather
		// than in that declaration and in two dialects' DDL.
		//
		// This one carries more weight than Template.kind does: PLAN.md
		// Section 29.3's binding rule keys on it, at most one credential
		// per kind on a definition with vault exempted, so an open
		// vocabulary would make that rule unenforceable rather than merely
		// untidy.
		field.String("kind").NotEmpty(),

		// namespace is the stable identifier an AWX import keys on to
		// decide whether a type already exists. Immutable and globally
		// unique: changing it would make the next import create a
		// duplicate rather than update this row, and every credential
		// already bound to it would answer to a type nobody can find
		// again.
		//
		// AWX gives every managed type one. The committed parity corpus
		// shows a CUSTOM type carrying one too (custom_api_token), which
		// is why this is required here rather than managed-only.
		field.String("namespace").NotEmpty().Immutable(),

		// managed reports whether this platform ships the type.
		//
		// Immutable, and enforced further by a hook that refuses any
		// update or delete on a row where it is true. That is AWX's own
		// rule and it is load bearing for imports: a managed type cannot
		// be edited, so an import must recognize and reuse the built-ins
		// rather than recreating them as custom types, which would give
		// every deployment two divergent definitions of "AWS credential".
		field.Bool("managed").Default(false).Immutable(),

		// inputs is the schema: what a credential of this type holds.
		//
		// Typed rather than map[string]any, which is what makes AGENTS.md's
		// typing rule hold end to end and gives ent typed accessors. It
		// makes internal/ent import internal/credtype, which is safe only
		// because credtype never imports internal/ent or internal/credstore.
		// That direction is not incidental: reversing it is an import cycle,
		// which is the reason persistence lives in a separate package from
		// the domain types at all.
		field.JSON("inputs", credtype.InputSchema{}).Optional(),

		// injectors is how those inputs reach a running job: environment
		// variables, extra variables, and generated files, with every value
		// a template over the type's own input ids.
		field.JSON("injectors", credtype.Injectors{}).Optional(),
	}
}

// Edges of the CredentialType.
func (CredentialType) Edges() []ent.Edge {
	return []ent.Edge{
		// The tenancy boundary, and OPTIONAL, which is the one place this
		// entity differs from Template.
		//
		// A managed type is global: this platform ships it and every tenant
		// uses the same one, so it has no owning organization. A custom
		// type belongs to the tenant that wrote it. Both dialects treat
		// NULL as distinct in a unique index, so two GLOBAL types could
		// share a name at the database level; the store refuses that
		// explicitly, documented the same way Template's cross-tenant
		// inventory check is, because ent cannot express it.
		edge.From("organization", Organization.Type).
			Ref("credential_types").
			Unique(),

		// The credentials of this type. Deliberately not cascaded:
		// deleting a type that credentials still reference must fail
		// loudly rather than silently taking working credentials with it.
		// A credential whose type vanished cannot be injected, and finding
		// that out at launch is worse than being refused at the delete.
		edge.To("credentials", Credential.Type),
	}
}

// Indexes of the CredentialType.
func (CredentialType) Indexes() []ent.Index {
	return []ent.Index{
		// Globally unique, not per organization. The namespace is the
		// import key, and an import that had to know which tenant it was
		// looking in before it could tell whether a type already existed
		// would defeat the purpose of having a stable identifier.
		index.Fields("namespace").Unique(),

		// Name is unique within an organization, matching Template and
		// Inventory. Two tenants both naming a type "Vault" is ordinary.
		index.Fields("name").
			Edges("organization").
			Unique(),

		// "which types does this tenant have, of this kind" is the query a
		// credential form runs to populate its type picker.
		index.Fields("kind"),
	}
}
