package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Fact holds the schema definition for the Fact entity: one observed value of
// one key on one device (Phase 41). A row is written only when a key's value
// changes, so a device's rows for a key are the history of that value, and
// the newest is its current value. A row is never updated to record a
// sighting (Phase 41h): when a value was last confirmed is read from the
// device's FactObservation rows, and observation_seq names the observation
// that wrote this one. What a row holds never changes either; only how it is
// sealed and keyed does, by a master key rotation (payload) and by a change
// of the facts key (the keyed columns and payload, internal/ent's
// fact_rekey.go and internal/facts/entstore's rekey.go).
type Fact struct {
	ent.Schema
}

// Mixin of the Fact.
func (Fact) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Fact.
func (Fact) Fields() []ent.Field {
	return []ent.Field{
		// A stored fact names nothing in plaintext (internal/facts' atrest.go,
		// the owner's decision of 2026-11-19). A build before that kept the
		// fact's name in a key column, which the facts at rest migration
		// dropped (SQLite 0052, PostgreSQL 0049), so every statement such a
		// build sends to this table names a column that is not there and is
		// refused (Annotations below has the rest of that).
		//
		// key_token is what a row is looked up by: HMAC-SHA-256 over the
		// device's ID, a NUL and the fact's key, under the store's token key
		// (facts.Keys.Token), so the table names no key and one key on two
		// devices gives two tokens. The SQL default is there only because
		// SQLite adds a NOT NULL column only with one; the fact_keyed check
		// refuses it, and NotEmpty refuses an empty token on every write
		// through the generated client, to which it is immutable. The one
		// other write is a change of the facts key, which rewrites it with
		// hash, keyed_by and the chain link (internal/ent's fact_rekey.go).
		field.String("key_token").Default("").NotEmpty().MaxLen(64).Immutable(),
		// payload holds the fact's key and value, sealed: the one entry
		// crypto.EncryptedKeyMarker names is a bound envelope over the JSON of
		// {"key": <the key>, "json": <the value's canonical JSON text>}, bound
		// to the row's device and token (internal/crypto's fact_hook.go). It
		// is the one field with a setter, which two writers use: a master key
		// rotation seals it again under the new master key
		// (crypto.RotateFactPayloads), and a change of the facts key seals it
		// again for the row's new token (internal/facts/entstore's rekey.go).
		// The hook refuses any write of it that is not a sealed envelope, and
		// the chain covers what it holds.
		field.JSON("payload", map[string]interface{}{}),
		// hash is the fact's keyed hash (facts.Keys.Hash): HMAC-SHA-256 over
		// the device and the canonical JSON of key and value, which is how a
		// later observation is compared with this one. A plain hash would let
		// anyone holding the table test a guess at a short value. A change of
		// the facts key rewrites it (fact_rekey.go).
		field.String("hash").NotEmpty().Immutable(),
		// keyed_by is the identity of the keys the token and the hash were
		// made under (facts.Keys.ID). A store refuses a device whose rows name
		// keys other than its own, rather than start a second history beside
		// them, except a device under the previous keys of a change of the
		// facts key (PLEIADES_FACTS_DIGEST_KEY_PREVIOUS): it re-keys that
		// device's rows, rewriting this column with the token, the hash and
		// the chain link through fact_rekey.go. Its SQL default is there for
		// the same reason as key_token's and is refused the same way, so no
		// row names no keys.
		field.String("keyed_by").Default("").NotEmpty().MaxLen(128).Immutable(),
		// kind is what the emitting method declared (Phase 41b): a stable
		// key's rows are its history; a volatile key keeps one row, replaced
		// when its value changes. A row from before 41b reads as stable.
		field.Enum("kind").Values("stable", "volatile").Default("stable").Immutable(),
		// provenance is the run that first observed this value
		// (facts.Provenance, with its run order). Empty on a row from before
		// 41b, which reads as unknown provenance.
		field.JSON("provenance", map[string]interface{}{}).Optional().Immutable(),
		// last_seen_at and last_provenance are what a build before 41h kept
		// here and moved on every confirmation. This build writes neither
		// and reads neither (it reads FactObservation), and both are
		// immutable, so it cannot move them. They stayed so a build from
		// before 41h could still serve the schema (the expand-only rule in
		// internal/ent/migrate's compat.go). No build from before facts at
		// rest serves it now, since that migration is a contract, so they
		// hold nothing anyone reads; the 1.0.0 golden image drops them with
		// the squash.
		field.Time("last_seen_at").Optional().Immutable(),
		field.JSON("last_provenance", map[string]interface{}{}).Optional().Immutable(),
		// observation_seq is the device's FactObservation that wrote this
		// row (its seq); 0 on a row from before 41h, which every observation
		// finds already there.
		field.Int64("observation_seq").Default(0).NonNegative().Immutable(),
		// chain_prev and chain are the device's hash chain over its stable
		// rows (internal/facts' chain.go): chain_prev is the chain hash of
		// the device's stable row stored before this one ("" for the first),
		// chain this row's. Each link is keyed under the store's chain key,
		// so the row's other columns cannot confirm a guessed fact key. Both
		// are null on a volatile row and on a row from before 41b. The unique index below keeps the chain a line: a second
		// writer naming the same predecessor fails, and its batch is
		// redelivered against the new head. A change of the facts key
		// recomputes both for every stable row (fact_rekey.go).
		field.String("chain_prev").Optional().Nillable().Immutable(),
		field.String("chain").Optional().Immutable(),
	}
}

// Indexes of the Fact: one key's token on one device (how a key's rows are
// found), every fact of one device (what the device's facts route reads, and
// what keyState reads a device's keys from), the devices holding a row under
// one set of keys (keyed_by, then the device: what RekeyAll lists and counts
// during a change of the facts key, through its rows alone), and one
// successor per chain link. An index's fields come before its edges, so the
// keys index leads with keyed_by, which RekeyAll seeks; keyState's
// keyed_by <> current seeks in neither order, and reads the device index.
func (Fact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key_token").Edges("device"),
		index.Edges("device"),
		index.Fields("keyed_by").Edges("device"),
		index.Fields("chain_prev").Edges("device").Unique(),
	}
}

// Annotations of the Fact.
//
// The check, fact_keyed, refuses a row with an empty key_token or keyed_by,
// which is every row a build from before facts were sealed would write: such
// a build knows neither column, so its insert leaves both at their empty
// default. The dropped key column already refuses every statement the build
// just before sealing sends here (each names it). The check also refuses a
// writer that names neither column, such as a build from before the key
// column existed, so no writer that predates sealing can store a row in any
// shape, and every row there is names the keys it is read under. The
// expression is plain SQL both dialects read the same way.
func (Fact) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Checks(map[string]string{
			"fact_keyed": "key_token <> '' AND keyed_by <> ''",
		}),
	}
}

// Edges of the Fact.
func (Fact) Edges() []ent.Edge {
	return []ent.Edge{
		// A Fact MUST belong to a specific device
		edge.From("device", Device.Type).
			Ref("facts").
			Unique().
			Required().
			// Immutable: a value moved to another device would leave both
			// devices' chains reading as unbroken (Phase 41h).
			Immutable(),
	}
}
