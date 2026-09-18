// The encryption_keys table: master keys this database has been told about, by
// fingerprint.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// EncryptionKey holds the schema definition for one master encryption key
// this database has been told about, by fingerprint and never by value.
//
// It answers "when did this key come into existence", a question nothing
// could answer before it: MASTER_ENCRYPTION_KEY lives in a deployment's
// environment, and the database it protects kept no record of which key
// that was or since when. It also gives the activity stream an object to
// point at, since an activity entry names a row by kind and id, and a key
// had no row.
//
// A row here is written by exactly two things. The setup command writes one
// when it generates a key and can reach the database, recording whether the
// operator took possession of it. The controller writes one at startup for a
// current key it has never seen, which is how a key made somewhere this
// database could not see (a Helm install generates its Secret before any
// database exists) still gets an entry, marked as first used rather than as
// generated.
type EncryptionKey struct {
	ent.Schema
}

// Mixin of the EncryptionKey.
//
// created_at is the moment the key was recorded. Nothing updates a row, so
// updated_at always equals it; the mixin is applied for the uniform column
// names every other table has.
func (EncryptionKey) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the EncryptionKey.
func (EncryptionKey) Fields() []ent.Field {
	return []ent.Field{
		// fingerprint is internal/crypto.Fingerprint of the key: a
		// domain-separated SHA-256 in hex. Unique, so two controller
		// replicas starting at once with a new key record it once, and
		// immutable, since a row describes one key forever.
		//
		// It is not a secret and is stored as it is. Recovering a 256-bit
		// random key from its hash is infeasible, and anyone able to test a
		// guessed key against this value could test it just as well against
		// any ciphertext the key protects, which lives in this same database.
		field.String("fingerprint").NotEmpty().Immutable().Unique(),

		// version is the MASTER_ENCRYPTION_KEY_VERSION tag the key carried
		// when it was recorded. A label, not an identity: the fingerprint is
		// the identity.
		field.String("version").NotEmpty().Immutable(),

		// origin says which of the two writers recorded this row.
		field.Enum("origin").Values("setup", "first_use").Immutable(),

		// possession records whether the operator re-entered the key at the
		// moment it was generated. A key the setup command generated without
		// a terminal was never shown to anybody, and a key first seen at
		// startup was generated somewhere this database cannot see, so for
		// those two the honest answers are not_checked and not_applicable
		// rather than a guess.
		field.Enum("possession").Values("checked", "not_checked", "not_applicable").Immutable(),
	}
}
