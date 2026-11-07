// Package schema: the FactCoverage entity, each distinct set of keys a
// device's observations covered (Phase 41h).
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// FactCoverage holds the schema definition for the FactCoverage entity: one
// distinct set of keys a device's observations covered (Phase 41h), kept once
// and named by its digest, so a gather that covers the same thousands of keys
// every hour writes them once rather than every hour.
type FactCoverage struct {
	ent.Schema
}

// Fields of the FactCoverage. Every field is immutable.
func (FactCoverage) Fields() []ent.Field {
	return []ent.Field{
		// digest is facts.Coverage.Digest of the two lists.
		field.String("digest").NotEmpty().MaxLen(64).Immutable(),
		// stable and volatile are the keys covered, by kind, each sorted.
		field.Strings("stable").Optional().Immutable(),
		field.Strings("volatile").Optional().Immutable(),
	}
}

// Indexes of the FactCoverage: one per device and digest.
func (FactCoverage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("digest").Edges("device").Unique(),
	}
}

// Edges of the FactCoverage.
func (FactCoverage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("device", Device.Type).
			Ref("fact_coverages").
			Unique().
			Required().
			Immutable(),
	}
}
