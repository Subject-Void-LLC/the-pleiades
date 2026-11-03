package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Fact holds the schema definition for the Fact entity: one observed value of
// one key on one device (Phase 41). A row is written only when a key's value
// changes, so a device's rows for a key are the history of that value, and
// the newest is its current value; last_seen_at says when it was last
// confirmed, the one field a later observation of the same value updates.
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
		// payload and hash are .Immutable(): a Fact is a point-in-time
		// observation, never an in-place edit. Before this, immutability
		// was only a doc-comment claim ("to verify immutability" on hash)
		// with nothing in the schema actually enforcing it; a caller could
		// still build a FactUpdate that rewrote either field.
		// key is the fact's name as the method emitted it (sdk.EmitFact).
		// The SQL default only keeps the column an expand migration: a
		// build from before it, which writes no facts, still serves the
		// table; NotEmpty refuses an empty key on every write.
		field.String("key").Default("").NotEmpty().MaxLen(256).Immutable(),
		// payload holds the value as {"json": <its canonical JSON text>}, so
		// a number reads back exactly (internal/facts/entstore).
		field.JSON("payload", map[string]interface{}{}).Immutable(),
		// hash is SHA-256 over the canonical JSON of key and value
		// (internal/facts.Hash), which is how a later observation is
		// compared with this one.
		field.String("hash").NotEmpty().Immutable(),
		// last_seen_at is when this value was last observed; a later
		// observation of the same value moves it rather than writing a row.
		// Nullable for the expand migration's sake, as key's default is; the
		// store always sets it, and a reader falls back to created_at.
		field.Time("last_seen_at").Optional().Default(time.Now),
		// kind is what the emitting method declared (Phase 41b): a stable
		// key's rows are its history; a volatile key keeps one row, replaced
		// when its value changes. A row from before 41b reads as stable.
		field.Enum("kind").Values("stable", "volatile").Default("stable").Immutable(),
		// provenance is the run that first observed this value
		// (facts.Provenance, with its run order); last_provenance the run
		// that last confirmed it, which moves with last_seen_at. Empty on a
		// row from before 41b, which reads as unknown provenance.
		field.JSON("provenance", map[string]interface{}{}).Optional().Immutable(),
		field.JSON("last_provenance", map[string]interface{}{}).Optional(),
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

// Indexes of the Fact: one key across devices, every fact of one device
// (what the device's facts route reads), and one successor per chain link.
func (Fact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Edges("device"),
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
			Required(),
	}
}
