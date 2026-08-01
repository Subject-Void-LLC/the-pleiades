package event_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// BenchmarkNatsPublish measures the raw throughput of pushing DRY CloudEvents
// to the JetStream message broker. We compare this to Kafka or RabbitMQ.
func BenchmarkNatsPublish(b *testing.B) {
	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		b.Fatalf("failed to start nats: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		b.Fatalf("failed to init bus: %v", err)
	}

	mockPayload := map[string]string{"metrics": "healthy"}
	evt, err := event.WrapPayload("bench-id", "device.metrics", mockPayload)
	if err != nil {
		b.Fatalf("failed to wrap payload: %v", err)
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		b.Fatalf("failed to marshal: %v", err)
	}

	b.Logf("[REFERENCE] Standard RabbitMQ cluster can handle ~30k-50k messages/sec.")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := bus.Publish(ctx, "pleiades.events.device.metrics", payloadBytes)
		if err != nil {
			b.Fatalf("failed to publish: %v", err)
		}
	}
}
