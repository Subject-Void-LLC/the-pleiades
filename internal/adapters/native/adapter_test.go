package native

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/wire"
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

	payload := wire.DispatchPayload{
		JobID:      "test-job-123",
		RunbookID:  "ping",
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
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

	payload := wire.DispatchPayload{
		JobID:      "test-job-456",
		RunbookID:  "ping",
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
	}

	// Execute must still report success: a log-publish failure is not the
	// execution's own failure.
	if err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("expected Execute to tolerate a publish failure, got error: %v", err)
	}
}

// TestNativeAdapter_Execute_HonorsContextCancellation proves Execute's
// simulated sleeps (sleepOrDone) actually stop promptly on cancellation,
// rather than running out their full ~1s of simulated work regardless.
// This is what makes internal/runner's self-abort mechanism
// (executeWithLease, agent_exec.go) provable against this adapter: a
// cancel that Execute silently ignored would make a self-abort
// indistinguishable, from the caller's point of view, from Execute simply
// finishing on its own a moment later.
func TestNativeAdapter_Execute_HonorsContextCancellation(t *testing.T) {
	adapter := NewAdapter(&mockBus{})
	payload := wire.DispatchPayload{JobID: "test-job-cancel", RunbookID: "ping", DeviceName: "router1", DeviceHost: "10.0.0.1"}

	ctx, cancel := context.WithCancel(context.Background())
	// Canceled almost immediately, well inside the first of Execute's two
	// 500ms simulated sleeps: a full, uninterrupted Execute takes ~1s, so
	// returning within a small fraction of that proves cancellation was
	// actually observed, not merely that Execute eventually finished.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := adapter.Execute(ctx, payload)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Execute() took %v to return after cancellation, want well under its own 500ms per-step sleep", elapsed)
	}
}

// TestNativeAdapter_Execute_HonorsContextCancellation_SecondStep is the
// companion to TestNativeAdapter_Execute_HonorsContextCancellation: that
// test cancels during the first of Execute's two sleepOrDone calls, so it
// alone never proves the second call site's own "if err != nil { return
// err }" branch is reachable. Canceling between the two calls (after the
// first has already completed normally) exercises that second branch
// directly.
func TestNativeAdapter_Execute_HonorsContextCancellation_SecondStep(t *testing.T) {
	adapter := NewAdapter(&mockBus{})
	payload := wire.DispatchPayload{JobID: "test-job-cancel-2", RunbookID: "ping", DeviceName: "router1", DeviceHost: "10.0.0.1"}

	ctx, cancel := context.WithCancel(context.Background())
	// Fires after the first ~500ms sleep has already completed, but well
	// before the second one would on its own.
	go func() {
		time.Sleep(600 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := adapter.Execute(ctx, payload)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if elapsed > 800*time.Millisecond {
		t.Errorf("Execute() took %v to return after cancellation, want well under a full second (two uninterrupted 500ms steps)", elapsed)
	}
}

// BenchmarkNativeAdapter_Execute_CancellationLatency reports the actual
// abort-to-return latency sleepOrDone gives self-abort (internal/runner's
// executeWithLease, agent_exec.go): each iteration cancels ctx
// immediately, before Execute's own first simulated sleep even starts, so
// the measured time is purely the cost of observing cancellation and
// unwinding, not any part of the simulated work itself. A concrete,
// sub-millisecond number here is what this phase's own Fuzz/Stress Test
// gate needs for the claim "self-abort is fast," rather than an assertion
// with no measurement behind it.
func BenchmarkNativeAdapter_Execute_CancellationLatency(b *testing.B) {
	adapter := NewAdapter(&mockBus{})
	payload := wire.DispatchPayload{JobID: "bench-job-cancel", RunbookID: "ping", DeviceName: "router1", DeviceHost: "10.0.0.1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = adapter.Execute(ctx, payload)
	}
}
