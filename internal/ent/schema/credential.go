package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Credential holds the schema definition for one credential: a set of
// values conforming to a CredentialType's input schema.
//
// This is the first entity in this repository whose entire reason for
// existing is to hold secrets, which changes what the ordinary decisions
// cost. Every field comment below says what it decided and why; the three
// worth reading first are the whole-map encryption on inputs, the
// deliberate absence of encryption on external, and secret_binding, which
// exists to close a documented weakness in the envelope format that this
// entity is the first to make dangerous.
type Credential struct {
	ent.Schema
}

// Mixin of the Credential.
func (Credential) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Credential.
func (Credential) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty(),
		field.String("description").Optional(),

		// inputs holds the values, keyed by the type's own input ids.
		//
		// map[string]string because every AWX credential input value is a
		// string, including a boolean. Storing them typed would mean this
		// column's shape depended on another row's schema, which no
		// migration could express.
		//
		// The WHOLE map is encrypted at rest, not just the fields the type
		// marks secret. That is deliberate and it is the same reasoning
		// internal/crypto/launch_hook.go already gives for
		// SavedLaunchConfig.answers: a mutation hook runs before the write
		// and cannot join to the credential's type to learn which ids are
		// secret, so a per-field scheme would either need the hook to issue
		// its own query mid-mutation or need the caller to pass the answer
		// in, and both are places to get it wrong per row. Encrypting
		// everything cannot be got wrong, is strictly safer, and costs
		// nothing anybody needs: no query filters on an input value, and
		// no API returns one.
		field.JSON("inputs", map[string]string{}).Optional(),

		// external maps an input id to a reference in an external secret
		// manager, resolved just in time immediately before the process
		// that needs it starts.
		//
		// Deliberately NOT encrypted, which is the one place this file
		// chooses less protection on purpose. A Vault path is not a secret;
		// it is a pointer to one. Encrypting it would make "which
		// credentials point at this mount" unanswerable, and that is a real
		// operational question during a Vault migration or an incident, not
		// a hypothetical one. The value behind the path is what needs
		// protecting, and this platform never stores that at all.
		field.JSON("external", map[string]string{}).Optional(),

		// secret_binding is the associated data that binds this row's
		// encrypted inputs to this row.
		//
		// internal/crypto/envelope.go documents a residual weakness: its
		// ciphertext carries no associated data tying it to the record it
		// came from, so an envelope string copied from one row to another
		// still decrypts. That was recorded as acceptable when the only
		// consumer was Device.properties. A credential row is that attack's
		// ideal target: relocate one organization's inputs onto another
		// organization's credential and it decrypts to the first one's
		// secrets, under a binding the second organization controls.
		//
		// A UUID rather than the primary key, because a create hook runs
		// before the insert and does not know the key yet. DefaultFunc
		// generates it during mutation, which is early enough to seal
		// against, and Immutable so it cannot be changed to match a
		// ciphertext somebody wants to relocate.
		field.String("secret_binding").
			Immutable().
			DefaultFunc(uuid.NewString),
	}
}

// Edges of the Credential.
func (Credential) Edges() []ent.Edge {
	return []ent.Edge{
		// What this credential is. Required and Immutable.
		//
		// Immutable is the interesting half. Changing a credential's type
		// reinterprets every stored value under a different schema: ids
		// that meant one thing now mean another or nothing, and injectors
		// that never saw these values start rendering them into somebody's
		// environment. That is a new credential, not an edit, and the same
		// argument Template.definition already makes.
		edge.From("credential_type", CredentialType.Type).
			Ref("credentials").
			Unique().
			Required().
			Immutable(),

		// The tenancy boundary. Required, unlike CredentialType's, because
		// there is no such thing as a global credential: a set of real
		// secret values always belongs to somebody.
		edge.From("organization", Organization.Type).
			Ref("credentials").
			Unique().
			Required(),

		// The templates this credential is bound to.
		//
		// Many to many, and the rule PLAN.md Section 29.3 attaches to it
		// cannot be a database constraint: "at most one credential per type
		// on a definition, except vault credentials, which may repeat when
		// each carries a distinct vault identifier" is a rule about the
		// joined row's TYPE's KIND plus a value stored inside the joined
		// row's own encrypted inputs. No dialect can express that.
		//
		// credtype.CheckBinding is the one implementation, called by the
		// store before the write and by the API handler so a caller gets a
		// conflict naming both credentials rather than an opaque store
		// error. The residual, that a direct SQL writer can still violate
		// it, is the same class as the cross-tenant note on Template.
		edge.From("templates", Template.Type).
			Ref("credentials"),

		// The bindings where THIS credential is the target: one per input
		// whose value comes from an external secret manager rather than
		// from this row's own encrypted inputs.
		//
		// Deleting a credential deletes its own bindings, which is
		// ordinary cascade: a binding describes how to fill an input of a
		// credential that no longer exists. The annotation is what makes
		// that true at the DATABASE rather than only in the store, matching
		// Template's own cascade edges; without it the NOT NULL foreign key
		// would make a credential undeletable the moment it gained a
		// binding.
		// The projects that authenticate their clone as this credential.
		// An scm credential is the ordinary case; nothing restricts the kind
		// here, because the binding rule that does live in credtype.
		edge.To("projects", Project.Type),

		edge.To("input_sources", CredentialInputSource.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),

		// The bindings where this credential is the SOURCE: the targets
		// that read their values through it.
		//
		// This cascades too, and the symmetry is deliberate rather than
		// lazy. The tempting alternative is to refuse the delete while any
		// target still depends on this source, the way DeleteType refuses a
		// type that credentials reference. That is right for a TYPE, which
		// is a schema, and wrong for a CREDENTIAL, which holds a secret:
		// DeleteCredential's own doc comment already made this call for
		// template bindings, and the reason applies here unchanged. A
		// compromised Vault token must be deletable now, not after every
		// credential that reads through it has been edited first, which is
		// exactly backwards during an incident.
		//
		// What makes that safe is that the resulting failure is loud. A
		// target left with an input it no longer has a value for fails at
		// injection with credtype's "input %q is required and has no value
		// at injection: it was not stored, not defaulted, not resolved from
		// an external source, and not supplied at launch", which names the
		// input and rules out all four sources it could have come from.
		edge.To("sourced_by", CredentialInputSource.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the Credential.
func (Credential) Indexes() []ent.Index {
	return []ent.Index{
		// Name is unique within an organization, matching every other
		// tenant-scoped entity here.
		index.Fields("name").
			Edges("organization").
			Unique(),
	}
}
