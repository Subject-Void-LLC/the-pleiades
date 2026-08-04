package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Fact holds the schema definition for the Fact entity.
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
		field.JSON("payload", map[string]interface{}{}).Immutable(),
		field.String("hash").NotEmpty().Immutable(), // To verify immutability
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
