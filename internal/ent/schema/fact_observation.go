// Package schema: the FactObservation entity, one stored gather per device
// (Phase 41h).
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// FactObservation holds the schema definition for the FactObservation entity:
// one task's facts on one device, as stored (Phase 41h). Every gather that
// stores facts writes exactly one, and nothing ever updates it: when a stored
// Fact was last confirmed is read from these rows (internal/facts'
// Sightings) rather than kept on the Fact, so the history stays insert-only.
type FactObservation struct {
	ent.Schema
}

// Fields of the FactObservation. Every field is immutable.
func (FactObservation) Fields() []ent.Field {
	return []ent.Field{
		// device_id is the device observed; a field of its own so the
		// unique index below leads with it, and "this device's observations
		// after n" is one range of the index.
		field.Int("device_id").Immutable(),
		// seq is the observation's place among the device's observations,
		// from 1. The unique index on (device_id, seq) makes two stores writing for one
		// device at once take turns: the second to commit fails and its
		// batch is redelivered after the first.
		field.Int64("seq").Positive().Immutable(),
		// observed_at is when the task finished (facts.Observation.At).
		field.Time("observed_at").Immutable(),
		// order_source and order_token copy the run's order out of
		// provenance, so a store can ask the database which observations
		// came after one without reading every row. A token past what a
		// signed 64-bit column holds is stored as an unknown order.
		field.String("order_source").Default("").MaxLen(256).Immutable(),
		field.Int64("order_token").Default(0).NonNegative().Immutable(),
		// provenance is the run that made the observation, in full.
		field.JSON("provenance", map[string]interface{}{}).Immutable(),
		// coverage is the digest (facts.Coverage.Digest) of the keys it
		// covered, a FactCoverage of the same device.
		field.String("coverage").NotEmpty().MaxLen(64).Immutable(),
	}
}

// Indexes of the FactObservation: one per device and number.
func (FactObservation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("device_id", "seq").Unique(),
	}
}

// Edges of the FactObservation.
func (FactObservation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("device", Device.Type).
			Ref("fact_observations").
			Field("device_id").
			Unique().
			Required().
			Immutable(),
	}
}
