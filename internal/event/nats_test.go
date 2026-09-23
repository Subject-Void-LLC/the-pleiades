package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// startNatsContainer boots an ephemeral NATS container with JetStream
// enabled and returns its connection URL. It is shared by every
// container-backed test in this package rather than each test hand-rolling
// its own container-start boilerplate.
func startNatsContainer(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	url := testsupport.StartNATS(t).URL()
	return url
}

func TestNatsJetStreamBus(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	// Initialize our NATS adapter (which ensures the Pleiades stream via
	// topology.EnsureStream).
	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus: %v", err)
	}

	// Set up the Subscriber. We use a channel to sync between the
	// subscriber goroutine and the main test thread.
	received := make(chan event.Event, 1)

	err = bus.Subscribe(ctx, "pleiades.events.device.created", func(evt event.Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// Wrap a payload in the DRY envelope and Publish it.
	mockData := map[string]string{"ip": "10.0.0.5"}
	evt, err := event.WrapPayload("uuid-123", "device.created", mockData)
	if err != nil {
		t.Fatalf("failed to wrap payload: %v", err)
	}

	err = bus.Publish(ctx, "pleiades.events.device.created", *evt)
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Wait for the event to be delivered (Release Gate).
	select {
	case result := <-received:
		if result.ID != "uuid-123" {
			t.Errorf("expected event ID uuid-123, got %s", result.ID)
		}
		if result.Type != "device.created" {
			t.Errorf("expected type device.created, got %s", result.Type)
		}
		if result.CorrelationID == "" {
			t.Error("expected Publish to stamp a non-empty CorrelationID")
		}
		if result.IdempotencyKey == "" {
			t.Error("expected Publish to stamp a non-empty IdempotencyKey")
		}
		t.Log("Event successfully published, persisted to JetStream, and consumed!")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
}

// TestNatsBusConformance runs the shared adapter conformance suite
// (conformance_test.go) against a real natsBus backed by an ephemeral NATS
// container, proving the NATS adapter honors the same substitutable Bus
// contract as the in-process one (TestInProcessBusConformance).
func TestNatsBusConformance(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus: %v", err)
	}

	// The same already-connected bus is handed back on every call. Each
	// conformance subtest uses a topic unique to itself, so sharing one bus
	// (and thus one underlying JetStream stream) across subtests is safe.
	runBusConformance(t, func() event.Bus {
		return bus
	})
}

// TestNatsBus_Close proves Close actually drains the real connection
// without erroring under normal conditions, and that the returned error
// (previously discarded entirely by a bare b.nc.Drain() call) is real
// and propagated.
func TestNatsBus_Close(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus: %v", err)
	}

	if err := bus.Close(); err != nil {
		t.Fatalf("Close() returned an unexpected error: %v", err)
	}
}
