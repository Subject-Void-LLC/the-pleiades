package event

import (
	"encoding/json"
	"time"
)

// Event is the generic, DRY envelope for all system events.
// Modeled after the CloudEvents spec, it ensures we don't rewrite JSON
// serializers for every new domain event (device.created, workflow.failed).
//
// CorrelationID, CausationID, ChainDepth, Actor, TraceID, and
// IdempotencyKey are PLAN.md Section 25's "Event envelope" shared
// primitive: every publish, every consumer, Section 15's loop-prevention,
// Section 19's tracing, and audit all read the same fields rather than each
// inventing its own. Bus.Publish stamps them from context, not WrapPayload,
// so a caller that builds an Event by hand still gets them filled in at the
// one real network boundary.
type Event struct {
	// ID is a unique UUID identifying this specific event instance.
	ID string `json:"id"`
	// Type defines the event schema (e.g., "device.created").
	Type string `json:"type"`
	// Timestamp is when the event occurred in UTC.
	Timestamp time.Time `json:"timestamp"`
	// Data holds the raw JSON payload specific to the Type.
	Data json.RawMessage `json:"data"`

	// CorrelationID identifies the chain of causally related events this
	// one belongs to. Every event in a chain (a trigger firing a runbook
	// whose own results fire another trigger) shares the same
	// CorrelationID, which is what lets PLAN.md Section 15's loop
	// prevention recognize a trigger re-firing within its own chain. A root
	// event (nothing caused it) gets a freshly minted CorrelationID from
	// Publish if the caller did not set one via context.
	CorrelationID string `json:"correlation_id,omitempty"`
	// CausationID is the ID of the specific event that directly caused this
	// one, distinct from CorrelationID: many events share one
	// CorrelationID across a whole chain, but each has at most one direct
	// cause. Empty for a root event.
	CausationID string `json:"causation_id,omitempty"`
	// ChainDepth counts how many causal hops this event is from its
	// chain's root (0 for the root itself). Wiring this onto the wire is
	// this phase's job; enforcing PLAN.md Section 15's "exceeded -> halt
	// and alert" ceiling is the Trigger Engine's, which does not exist as a
	// package yet. See Chained.
	ChainDepth int `json:"chain_depth,omitempty"`
	// Actor identifies who or what caused this event: a human identity
	// subject, a service account, or empty for a system-originated event.
	// Deliberately a plain string, never an auth.Identity: this package
	// must not import internal/auth.
	Actor string `json:"actor,omitempty"`
	// TraceID propagates api.TraceIDMiddleware's own trace ID (or an
	// equivalent from any other entry point) through the bus, so PLAN.md
	// Section 19's distributed trace can span API -> Bus -> Runner.
	TraceID string `json:"trace_id,omitempty"`
	// IdempotencyKey is a deterministic identifier derived from stable
	// inputs (PLAN.md Section 26.2), not a fresh random value: a retried
	// publish of the same logical operation must produce the same key so
	// both JetStream's own producer-side dedup and the Idempotent Consumer
	// decorator (see DedupStore) can recognize it as a duplicate.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// WrapPayload is a helper to marshal any Go struct into an Event envelope.
func WrapPayload(id string, eventType string, payload interface{}) (*Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Event{
		ID:        id,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      b,
	}, nil
}
