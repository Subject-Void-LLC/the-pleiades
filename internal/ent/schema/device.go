package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Device holds the schema definition for the Device entity.
type Device struct {
	ent.Schema
}

// Fields of the Device.
func (Device) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		// properties holds the dynamic Document schema for the Factory
		field.JSON("properties", map[string]interface{}{}).Optional(),
	}
}

// Edges of the Device.
func (Device) Edges() []ent.Edge {
	return []ent.Edge{
		// Self-referencing graph edge for network topology (Parent -> Child)
		edge.To("children", Device.Type).
			From("parent").
			Unique(),
		// A device has many immutable historical facts
		edge.To("facts", Fact.Type),
	}
}
