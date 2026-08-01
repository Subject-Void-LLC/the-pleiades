package event

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type natsBus struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// NewNatsBus connects to an external NATS broker and initializes the Pleiades stream.
// It adheres to the Liskov Substitution Principle by perfectly substituting the event.Bus interface.
func NewNatsBus(ctx context.Context, url string) (Bus, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats at %s: %w", url, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize jetstream: %w", err)
	}

	// Ensure the core Pleiades event stream exists
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "Pleiades_Events",
		Subjects: []string{"pleiades.events.>"},
		Storage:  jetstream.FileStorage, // Durable
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	return &natsBus{
		nc: nc,
		js: js,
	}, nil
}

// Publish fires a JSON payload into the NATS mesh.
func (b *natsBus) Publish(ctx context.Context, topic string, payload []byte) error {
	// NATS JetStream Publish takes a context and the subject/payload
	_, err := b.js.Publish(ctx, topic, payload)
	if err != nil {
		return fmt.Errorf("failed to publish to %s: %w", topic, err)
	}
	return nil
}

// Subscribe listens for incoming JetStream messages, decodes the DRY Event envelope,
// and passes it to the handler.
func (b *natsBus) Subscribe(ctx context.Context, topic string, handler func(event Event)) error {
	// We use an ephemeral consumer for this simple pub/sub interface mapping
	consumer, err := b.js.CreateOrUpdateConsumer(ctx, "Pleiades_Events", jetstream.ConsumerConfig{
		FilterSubject: topic,
		DeliverPolicy: jetstream.DeliverNewPolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create consumer for %s: %w", topic, err)
	}

	// Consume messages infinitely in the background until context is canceled
	_, err = consumer.Consume(func(msg jetstream.Msg) {
		var e Event
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			// In a real implementation we would log this structured error using slog
			// But for now, we drop malformed messages.
			msg.Nak()
			return
		}
		
		handler(e)
		msg.Ack()
	})
	
	if err != nil {
		return fmt.Errorf("failed to start consumer for %s: %w", topic, err)
	}
	return nil
}

// Close gracefully drains the NATS connection.
func (b *natsBus) Close() {
	b.nc.Drain()
}
