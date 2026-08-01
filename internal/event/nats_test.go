package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestNatsJetStreamBus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up an ephemeral NATS container with JetStream enabled
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	// Get the mapped connection URL
	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// 2. Initialize our NATS adapter (which creates the Pleiades_Events stream)
	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("failed to init nats bus: %v", err)
	}

	// 3. Set up the Subscriber
	// We use a channel to sync between the subscriber goroutine and the main test thread
	received := make(chan event.Event, 1)
	
	err = bus.Subscribe(ctx, "pleiades.events.device.created", func(evt event.Event) {
		received <- evt
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// 4. Wrap a payload in the DRY envelope and Publish it
	mockData := map[string]string{"ip": "10.0.0.5"}
	evt, err := event.WrapPayload("uuid-123", "device.created", mockData)
	if err != nil {
		t.Fatalf("failed to wrap payload: %v", err)
	}

	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	err = bus.Publish(ctx, "pleiades.events.device.created", payloadBytes)
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// 5. Wait for the event to be delivered (Release Gate)
	select {
	case result := <-received:
		if result.ID != "uuid-123" {
			t.Errorf("expected event ID uuid-123, got %s", result.ID)
		}
		if result.Type != "device.created" {
			t.Errorf("expected type device.created, got %s", result.Type)
		}
		t.Log("Event successfully published, persisted to JetStream, and consumed!")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
}
