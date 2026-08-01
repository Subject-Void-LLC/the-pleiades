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

// Fields of the Fact.
func (Fact) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("payload", map[string]interface{}{}),
		field.String("hash").NotEmpty(), // To verify immutability
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
