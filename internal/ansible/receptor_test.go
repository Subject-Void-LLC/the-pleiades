package ansible_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ansible"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// mockBus is a minimal event.Bus fake: ReceptorAdapter only ever calls
// Publish, never Subscribe, so this is all TestReceptorAdapter_ReleaseGate
// needs to prove the release gate's own claim (10 events + 1 EOF event
// published).
type mockBus struct {
	Publishes int
}

func (m *mockBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.Publishes++
	return nil
}

func (m *mockBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *mockBus) Close() error {
	return nil
}

func TestReceptorAdapter_ReleaseGate(t *testing.T) {
	bus := &mockBus{}
	adapter := ansible.NewReceptorAdapter(bus)

	// Stream 10 events
	err := adapter.StreamMockJob(context.Background(), "job-123", 10)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Expect 10 events + 1 EOF event
	if bus.Publishes != 11 {
		t.Errorf("expected 11 publishes, got %d", bus.Publishes)
	}
}

// failAfterNBus errors starting on its (n+1)th Publish call, so tests can
// exercise both StreamMockJob's mid-loop publish-error path and its
// separate EOF-event publish-error path.
type failAfterNBus struct {
	n     int
	calls int
}

func (m *failAfterNBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	m.calls++
	if m.calls > m.n {
		return errors.New("deliberate publish failure")
	}
	return nil
}

func (m *failAfterNBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return nil
}

func (m *failAfterNBus) Close() error { return nil }

func TestReceptorAdapter_PropagatesMidLoopPublishFailure(t *testing.T) {
	bus := &failAfterNBus{n: 0}
	adapter := ansible.NewReceptorAdapter(bus)

	err := adapter.StreamMockJob(context.Background(), "job-fail", 5)
	if err == nil {
		t.Fatal("expected an error from the first failing publish, got nil")
	}
}

func TestReceptorAdapter_PropagatesEOFPublishFailure(t *testing.T) {
	const numEvents = 3
	bus := &failAfterNBus{n: numEvents} // succeed for every event, fail on the EOF publish
	adapter := ansible.NewReceptorAdapter(bus)

	err := adapter.StreamMockJob(context.Background(), "job-fail-eof", numEvents)
	if err == nil {
		t.Fatal("expected an error from the failing EOF publish, got nil")
	}
}

func TestReceptorAdapter_RespectsCanceledContext(t *testing.T) {
	bus := &mockBus{}
	adapter := ansible.NewReceptorAdapter(bus)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := adapter.StreamMockJob(ctx, "job-canceled", 5)
	if err == nil {
		t.Fatal("expected an error for an already-canceled context, got nil")
	}
	if bus.Publishes != 0 {
		t.Errorf("expected 0 publishes for an already-canceled context, got %d", bus.Publishes)
	}
}
