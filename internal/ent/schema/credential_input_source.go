package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CredentialInputSource binds one credential input to another credential
// that supplies its value from an external secret manager.
//
// # What this replaces, and why a row rather than a string
//
// Phase 22 shipped the same idea as a string. Credential.external maps an
// input id to "<source>:<reference>", the source names a process-wide
// Lookup wired at the composition root, and the rest is that Lookup's to
// interpret. internal/credtype/lookup.go's own doc comment records why that
// was the right thing to ship and what it costs: every credential naming
// Vault names the SAME Vault, configured from the Controller's environment,
// because a string has nowhere to put an address or a token.
//
// The moment a second Vault exists, or a Vault token needs rotating, that
// model has no answer. A Vault address and a Vault token are themselves
// credentials: they need RBAC, an audit trail, encryption at rest and a
// rotation story, and a string in a column is none of those. So the source
// becomes an ordinary Credential row, of a type whose kind is external, and
// this entity is the binding between one target input and one such source.
//
// The string form is NOT removed. Both resolve, and internal/credtype's
// lookup_factory.go carries the reasoning: the file source is genuinely
// deployment-wide configuration, since a Kubernetes projected volume is not
// per-credential, and a Vault genuinely is not.
type CredentialInputSource struct {
	ent.Schema
}

// Mixin of the CredentialInputSource.
func (CredentialInputSource) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the CredentialInputSource.
func (CredentialInputSource) Fields() []ent.Field {
	return []ent.Field{
		// input_id is the id of the TARGET credential's input this row
		// supplies. It is not validated against the target's type here,
		// because a schema cannot join to another row's JSON column to
		// find out which ids that type declares. The store checks it
		// before the write, which is the same split Credential's own
		// one-per-kind binding rule already lives with.
		field.String("input_id").NotEmpty(),

		// metadata is the source's own per-field addressing: for a
		// HashiCorp Vault source, the secret path, the key within it, and
		// optionally a version.
		//
		// map[string]string for the reason Credential.inputs is: every
		// value here is a string, and storing them typed would make this
		// column's shape depend on another row's schema, which no
		// migration could express.
		//
		// Deliberately NOT encrypted, which is the same decision
		// Credential.external already made and for the same reason. A
		// Vault path is a pointer to a secret, not a secret. Encrypting it
		// would make "which credentials point at this mount" unanswerable,
		// and that is a real question during a Vault migration or an
		// incident rather than a hypothetical one. The value behind the
		// path is what needs protecting, and this platform never stores
		// that at all.
		//
		// The consequence to be aware of rather than surprised by: a path
		// can still name a customer's environment, so this column is
		// operational information even though it is not a secret. It is
		// returned by the API to callers who may already read the
		// credential it belongs to, and to nobody else.
		field.JSON("metadata", map[string]string{}).Optional(),
	}
}

// Edges of the CredentialInputSource.
func (CredentialInputSource) Edges() []ent.Edge {
	return []ent.Edge{
		// The credential whose input this row supplies.
		//
		// Required: a binding with no target is not a partial binding, it
		// is a row that can never be read, since every read path starts
		// from the target credential being resolved.
		edge.From("target_credential", Credential.Type).
			Ref("input_sources").
			Unique().
			Required(),

		// The credential that supplies the value: an external-kind
		// credential holding a Vault address and token, or the equivalent
		// for whatever source its type names.
		//
		// Deliberately NOT Immutable, unlike Credential's own type edge.
		// Repointing a binding at a different Vault is an ordinary
		// operational act (a migration between clusters), and unlike
		// changing a credential's TYPE it reinterprets nothing already
		// stored: this row holds a path, and the path either resolves
		// against the new source or fails loudly at the next dispatch.
		//
		// Deleting the source deletes this binding, matching the target
		// edge above. Credential.sourced_by carries the reasoning: a
		// credential holds a secret, and a secret must stay deletable
		// during an incident.
		edge.From("source_credential", Credential.Type).
			Ref("sourced_by").
			Unique().
			Required(),
	}
}

// Indexes of the CredentialInputSource.
func (CredentialInputSource) Indexes() []ent.Index {
	return []ent.Index{
		// One source per input, enforced by the database.
		//
		// Worth contrasting with the rule Credential.templates carries,
		// which its own comment explains no dialect can express: that one
		// depends on a joined row's type's KIND plus a value inside that
		// row's encrypted inputs. This one depends on two foreign keys and
		// a plain column, so it IS expressible, and a rule that can be a
		// constraint should be one. Two rows supplying the same input
		// would make injection depend on row order, which is the silent
		// last-writer-wins failure internal/credtype.ErrInjectorConflict
		// already refuses elsewhere.
		index.Edges("target_credential").
			Fields("input_id").
			Unique(),
	}
}
