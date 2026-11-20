package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Fact holds the schema definition for the Fact entity: one observed value of
// one key on one device (Phase 41). A row is written only when a key's value
// changes, so a device's rows for a key are the history of that value, and
// the newest is its current value. A row is never updated (Phase 41h): when
// a value was last confirmed is read from the device's FactObservation rows,
// and observation_seq names the observation that wrote this one.
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
		// the owner's decision of 2026-11-19). key is what a build before that
		// kept the fact's name in; this build writes it empty and reads it
		// never, and it stays only so an older build can still serve the
		// schema (the expand-only rule in internal/ent/migrate's compat.go).
		// The 1.0.0 golden image drops it with the squash.
		field.String("key").Default("").MaxLen(256).Immutable(),
		// key_token is what a row is looked up by: HMAC-SHA-256 over the
		// device's ID, a NUL and the fact's key, under the store's token key
		// (facts.Keys.Token), so the table names no key and one key on two
		// devices gives two tokens. The SQL default only keeps the column an
		// expand migration; NotEmpty refuses an empty token on every write.
		field.String("key_token").Default("").NotEmpty().MaxLen(64).Immutable(),
		// payload holds the fact's key and value, sealed: the one entry
		// crypto.EncryptedKeyMarker names is a bound envelope over the JSON of
		// {"key": <the key>, "json": <the value's canonical JSON text>}, bound
		// to the row's device and token (internal/crypto's fact_hook.go). It
		// is the one field with a setter, and only so a master key rotation
		// can seal it again under the new key (crypto.RotateFactPayloads); the
		// hook refuses any other write, and the chain covers what it holds.
		field.JSON("payload", map[string]interface{}{}),
		// hash is the fact's keyed hash (facts.Keys.Hash): HMAC-SHA-256 over
		// the device and the canonical JSON of key and value, which is how a
		// later observation is compared with this one. A plain hash would let
		// anyone holding the table test a guess at a short value.
		field.String("hash").NotEmpty().Immutable(),
		// keyed_by is the identity of the keys the token and the hash were
		// made under (facts.Keys.ID). A store refuses a device whose rows
		// name other keys, rather than start a second history beside them.
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
		// immutable, so it cannot move them. They stay only so a build from
		// before 41h can still serve this schema (the expand-only rule in
		// internal/ent/migrate's compat.go); the 1.0.0 golden image drops
		// them with the squash. A build from before 41h can serve the
		// schema, but what it writes this build reads as observed once and
		// never confirmed (its rows carry observation_seq 0); nothing that
		// old is deployed (pre-1.0, forward only).
		field.Time("last_seen_at").Optional().Immutable(),
		field.JSON("last_provenance", map[string]interface{}{}).Optional().Immutable(),
		// observation_seq is the device's FactObservation that wrote this
		// row (its seq); 0 on a row from before 41h, which every observation
		// finds already there.
		field.Int64("observation_seq").Default(0).NonNegative().Immutable(),
		// chain_prev and chain are the device's hash chain over its stable
		// rows (internal/facts' chain.go): chain_prev is the chain hash of
		// the device's stable row stored before this one ("" for the first),
		// chain this row's. Both are null on a volatile row and on a row from
		// before 41b. The unique index below keeps the chain a line: a second
		// writer naming the same predecessor fails, and its batch is
		// redelivered against the new head.
		field.String("chain_prev").Optional().Nillable().Immutable(),
		field.String("chain").Optional().Immutable(),
	}
}

// Indexes of the Fact: one key's token on one device (how a key's rows are
// found), every fact of one device (what the device's facts route reads), and
// one successor per chain link.
func (Fact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key_token").Edges("device"),
		index.Edges("device"),
		index.Fields("chain_prev").Edges("device").Unique(),
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
