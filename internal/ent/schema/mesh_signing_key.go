package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// MeshSigningKey holds one NATS account signing key: the only key in the
// mesh identity hierarchy a running process is permitted to hold.
//
// # Why this is its own entity rather than a Credential row
//
// Phase 101's own text says key custody is Phase 78's job and that no
// parallel key store may be invented beside MASTER_ENCRYPTION_KEY. That
// constraint is honored exactly, and this entity is how: the seed below is
// sealed by the same crypto.EnvelopeService, under the same master key,
// in the same bound envelope Credential.inputs uses. There is one
// key-encryption key and one cipher suite in this platform, and this adds
// neither.
//
// What it does not do is pretend to be a Credential, and the reason is a
// different invariant. A Credential is a TENANT-OWNED secret injected into
// a target device: its organization edge is Required, its own schema
// comment says "there is no such thing as a global credential", its
// plaintext read path is import-restricted to internal/dispatch, and its
// values are shaped by a credential type's injector document. A control
// plane signing key belongs to no tenant, is injected into nothing, must
// never be readable by internal/dispatch, and has no injectors. Forcing it
// into that table would mean either relaxing a Required edge, which
// weakens multi-tenant isolation everywhere, or inventing a synthetic
// system organization, which leaves permanent edge cases in RBAC filters,
// tenant deletion cascades and every list query.
//
// # Why several rows rather than one
//
// Rotation. A signing key is rotatable precisely so the issuer's key can
// be rolled without re-minting the account, and a roll is not atomic: the
// account JWT lists BOTH the outgoing and incoming keys for as long as
// credentials signed by the outgoing one are still valid, while the
// Controller signs new credentials with the incoming one. That is two rows
// with one active, not one row overwritten. Overwriting would invalidate
// every credential already in the field at the instant of the write.
type MeshSigningKey struct {
	ent.Schema
}

// Mixin of the MeshSigningKey.
func (MeshSigningKey) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the MeshSigningKey.
func (MeshSigningKey) Fields() []ent.Field {
	return []ent.Field{
		// key_id is the operator-facing name for one key in the rotation
		// sequence. It is NOT the key's public key: an operator retiring a
		// key needs to name it in a command before the key material is
		// loaded, and a 56-character nkey is not that name.
		field.String("key_id").NotEmpty().Immutable().Unique(),

		// account_subject is the public key of the account this key signs
		// for, stored so a deployment holding keys for more than one
		// account can tell them apart without decrypting anything. It is
		// public by construction: an account's subject appears in every
		// user JWT this key issues.
		field.String("account_subject").NotEmpty().Immutable(),

		// public_key is this signing key's own public key, which is the
		// value that must appear in the account JWT's signing key list for
		// anything this key signs to be accepted.
		//
		// Stored deliberately, even though it is derivable from the seed,
		// because deriving it would mean decrypting the seed to answer a
		// question that is not secret. An operator checking whether the
		// account JWT lists the right keys should not have to unseal key
		// material to do it.
		field.String("public_key").NotEmpty().Immutable(),

		// seed is the key material, and it is the reason this entity
		// exists. It is written as an envelope string by
		// crypto.MeshSigningKeySeedHook and never reaches the database in
		// plaintext.
		field.String("seed").Sensitive(),

		// active marks the one key new credentials are signed with.
		//
		// A retired key stays in the table rather than being deleted: its
		// public key must remain in the account JWT until the last
		// credential it signed has expired, and an operator needs to see
		// what is still in that window. Deleting the row would leave the
		// account JWT naming a key nothing here records.
		field.Bool("active").Default(false),

		// secret_binding is the per-row associated data the seed's
		// ciphertext is sealed against, so a row's value cannot be
		// relocated onto another row and still decrypt.
		//
		// Immutable and defaulted, exactly as Credential's is, and for the
		// stronger reason: relocating one deployment's signing key onto
		// another's row would let the second deployment mint credentials
		// the first account trusts.
		field.String("secret_binding").Immutable().DefaultFunc(uuid.NewString),
	}
}

// Indexes of the MeshSigningKey.
func (MeshSigningKey) Indexes() []ent.Index {
	return []ent.Index{
		// Lookups are by account, and a rotation reads every key for one
		// account to decide which to sign with and which are still inside
		// their credentials' expiry window.
		index.Fields("account_subject"),
	}
}

// The "one ACTIVE key per account" rule is enforced in application code,
// not by a unique index, and the reason is worth stating because the
// obvious index is wrong.
//
// A unique index on (account_subject, active) does not mean "at most one
// active key". It means at most one row per DISTINCT value of active, so
// it would equally forbid a second INACTIVE key, which is precisely the
// retired-key history this entity exists to keep: a retired key's public
// key must stay in the account JWT until the last credential it signed has
// expired.
//
// What actually expresses the rule is a partial unique index
// ("WHERE active"), which both SQLite and PostgreSQL support but which
// this schema does not declare, because a constraint written only into the
// hand-authored migrations would be absent from the schema enttest builds
// and every unit test would run without it. That divergence is a worse
// failure than the application check, since it is invisible until
// production. This mirrors how Credential documents its own one-per-kind
// binding rule as application-enforced rather than pretending otherwise.
