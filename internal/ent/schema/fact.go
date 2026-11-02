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
		field.String("key").NotEmpty().MaxLen(256).Immutable(),
		// payload holds the value as {"value": <value>}.
		field.JSON("payload", map[string]interface{}{}).Immutable(),
		// hash is SHA-256 over the canonical JSON of key and value
		// (internal/facts.Hash), which is how a later observation is
		// compared with this one.
		field.String("hash").NotEmpty().Immutable(),
		// last_seen_at is when this value was last observed; a later
		// observation of the same value moves it rather than writing a row.
		field.Time("last_seen_at").Default(time.Now),
	}
}

// Indexes of the Fact: a device's facts by key, newest first.
func (Fact) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Edges("device"),
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
