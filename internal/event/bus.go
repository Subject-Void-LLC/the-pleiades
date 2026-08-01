// Package event abstracts the underlying message broker (NATS JetStream).
package event

import "context"

// Message represents a single event payload traversing the mesh.
type Message struct {
	ID      string
	Topic   string
	Payload []byte
}

// Bus defines the contract for the high-durability event mesh.
type Bus interface {
	// Publish fires an event to a specific topic without waiting for a response.
	Publish(ctx context.Context, topic string, payload []byte) error

	// Subscribe registers a handler function to process incoming messages
	// for a specific topic.
	Subscribe(ctx context.Context, topic string, handler func(msg Message)) error
}
