// Package event abstracts the underlying message broker (NATS JetStream).
package event

import "context"

// Bus defines the contract for the high-durability event mesh.
// It adheres to the Dependency Inversion principle by ensuring the core
// execution engine never imports the NATS library directly.
type Bus interface {
	// Publish fires a CloudEvent to a specific topic without waiting for a response.
	// The payload is expected to be a marshaled JSON string of the Event struct.
	Publish(ctx context.Context, topic string, payload []byte) error

	// Subscribe registers a handler function to process incoming events
	// for a specific topic.
	Subscribe(ctx context.Context, topic string, handler func(event Event)) error
}
