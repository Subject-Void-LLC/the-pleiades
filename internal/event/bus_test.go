package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
)

// mockBus implements event.Bus
type mockBus struct {
	published []event.Event
	topics    []string
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.published = append(m.published, evt)
	m.topics = append(m.topics, topic)
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(evt event.Event) error) error {
	return nil
}

func (m *mockBus) Close() error {
	return nil
}

func TestEventBusCompliance(t *testing.T) {
	// If mockBus does not implement event.Bus, the compiler will fail.
	var bus event.Bus = &mockBus{}

	// Wrap a payload in the DRY envelope
	mockPayload := map[string]string{"status": "ok"}
	evt, err := event.WrapPayload("test-1", "device.state_changed", mockPayload)
	if err != nil {
		t.Fatalf("failed to wrap payload: %v", err)
	}

	err = bus.Publish(context.Background(), "pleiades.events.device.state_changed", *evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock, ok := bus.(*mockBus)
	if !ok {
		t.Fatal("failed to assert mockBus")
	}

	if len(mock.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mock.published))
	}

	if mock.topics[0] != "pleiades.events.device.state_changed" {
		t.Errorf("expected topic 'pleiades.events.device.state_changed', got '%s'", mock.topics[0])
	}

	if mock.published[0].ID != "test-1" {
		t.Errorf("expected published event ID test-1, got %s", mock.published[0].ID)
	}

	// Test the WrapPayload helper behavior
	if evt.ID != "test-1" {
		t.Errorf("expected ID test-1, got %s", evt.ID)
	}
	if evt.Type != "device.state_changed" {
		t.Errorf("expected Type device.state_changed, got %s", evt.Type)
	}
	if evt.Timestamp.After(time.Now()) {
		t.Errorf("timestamp is in the future")
	}
}
