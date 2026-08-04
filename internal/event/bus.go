// Package event abstracts the underlying message broker (NATS JetStream).
package event

import "context"

// Bus defines the contract for the high-durability event mesh.
// It adheres to the Dependency Inversion principle by ensuring the core
// execution engine never imports the NATS library directly.
type Bus interface {
	// Publish fires evt to a specific topic without waiting for a response.
	// Publish stamps evt's envelope fields (CorrelationID, CausationID,
	// ChainDepth, Actor, TraceID, IdempotencyKey) from ctx before sending,
	// per PLAN.md Section 26, so no call site can omit them by forgetting
	// to set them itself: see WithCorrelationID and its siblings.
	Publish(ctx context.Context, topic string, evt Event) error

	// Subscribe registers a handler function to process incoming events for
	// a specific topic. The handler's error return is what makes
	// at-least-once delivery possible (PLAN.md Section 26.1): a nil error
	// acknowledges the message, a non-nil error triggers the adapter's own
	// redelivery/dead-letter policy. A handler that could not fail would
	// force the adapter to acknowledge unconditionally.
	Subscribe(ctx context.Context, topic string, handler func(event Event) error) error

	// Close releases any resources the Bus holds (a network connection,
	// for natsBus), per PATTERNS.md's Graceful Shutdown entry: already
	// in-flight work finishes or flushes before the underlying resource
	// actually closes, rather than being dropped. A composition root
	// calls this once, on its own shutdown path.
	Close() error
}
