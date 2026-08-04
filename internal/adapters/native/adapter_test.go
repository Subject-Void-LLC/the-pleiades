package native

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
)

// mockBus is a minimal event.Bus fake: Adapter only ever calls Publish,
// never Subscribe.
type mockBus struct {
	published []struct {
		topic string
		evt   event.Event
	}
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.published = append(m.published, struct {
		topic string
		evt   event.Event
	}{topic, evt})
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *mockBus) Close() error {
	return nil
}

func TestNativeAdapter_Execute(t *testing.T) {
	bus := &mockBus{}
	adapter := NewAdapter(bus)

	payload := runner.DispatchPayload{
		JobID:      "test-job-123",
		RunbookID:  "ping",
		DeviceName: "router1",
		DeviceIP:   "10.0.0.1",
	}

	err := adapter.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(bus.published) != 3 {
		t.Fatalf("expected 3 log events, got %d", len(bus.published))
	}

	wantSubject := topology.LogSubject("test-job-123")
	for _, p := range bus.published {
		if p.topic != wantSubject {
			t.Errorf("expected subject %s, got %s", wantSubject, p.topic)
		}
		var evt LogEvent
		if err := json.Unmarshal(p.evt.Data, &evt); err != nil {
			t.Fatalf("failed to unmarshal log event: %v", err)
		}
		if evt.Host != "router1" {
			t.Errorf("expected host router1, got %s", evt.Host)
		}
	}
}

// failingBus is an event.Bus whose Publish always errors, so
// TestNativeAdapter_Execute_ToleratesPublishFailure can prove streamLog's
// own documented contract: a publish failure is logged, not propagated to
// Execute's caller, since log streaming is an observability side effect of
// an execution that has already happened, not a precondition for it.
type failingBus struct{}

func (failingBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	return errors.New("deliberate publish failure")
}

func (failingBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (failingBus) Close() error { return nil }

func TestNativeAdapter_Execute_ToleratesPublishFailure(t *testing.T) {
	adapter := NewAdapter(failingBus{})

	payload := runner.DispatchPayload{
		JobID:      "test-job-456",
		RunbookID:  "ping",
		DeviceName: "router1",
		DeviceIP:   "10.0.0.1",
	}

	// Execute must still report success: a log-publish failure is not the
	// execution's own failure.
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("expected Execute to tolerate a publish failure, got error: %v", err)
	}
}
