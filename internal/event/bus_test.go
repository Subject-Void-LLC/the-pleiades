package event_test

import (
	"context"
	"testing"

	"github.com/customerx/pleiades/internal/event"
)

// mockBus implements event.Bus
type mockBus struct {
	published []event.Message
}

func (m *mockBus) Publish(ctx context.Context, topic string, payload []byte) error {
	m.published = append(m.published, event.Message{
		ID:      "test-1",
		Topic:   topic,
		Payload: payload,
	})
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(msg event.Message)) error {
	return nil
}

func TestEventBusCompliance(t *testing.T) {
	// If mockBus does not implement event.Bus, the compiler will fail.
	var bus event.Bus = &mockBus{}
	
	err := bus.Publish(context.Background(), "device.state_changed", []byte(`{"status":"ok"}`))
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

	if mock.published[0].Topic != "device.state_changed" {
		t.Errorf("expected topic 'device.state_changed', got '%s'", mock.published[0].Topic)
	}
}
