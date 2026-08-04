package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Organization holds the schema definition for the Organization entity:
// the PLAN.md Section 18 tenancy boundary, the highest multi-tenant scope
// a resource belongs to. This phase adds only the entity and its Device
// edge, as substrate; row-level tenant filtering, Teams, and Roles are
// Phase 8's job, not this one.
type Organization struct {
	ent.Schema
}

// Mixin of the Organization.
func (Organization) Mixin() []ent.Mixin {
	return []ent.Mixin{TimestampMixin{}}
}

// Fields of the Organization.
func (Organization) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
	}
}

// Edges of the Organization.
func (Organization) Edges() []ent.Edge {
	return []ent.Edge{
		// A device optionally belongs to one Organization (Device.
		// organization is the Ref side, and is what makes the edge
		// optional: .Required() is declared there, not here, and this
		// phase deliberately does not declare it).
		edge.To("devices", Device.Type),
	}
}
