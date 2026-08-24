package runner_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

// memoryDedupStore is a runner.DedupStore backed by a map.
type memoryDedupStore struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (m *memoryDedupStore) SeenRecently(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[key], nil
}

func (m *memoryDedupStore) MarkSeen(_ context.Context, key string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]bool{}
	}
	m.seen[key] = true
	return nil
}

func (m *memoryDedupStore) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[key]
}

// TestAgent_SuppressesADispatchThatAlreadyExecuted is Phase 96c's
// consumer-side half of the D3 fix, driven through the real Run loop.
//
// The hazard: a dispatch publish can report failure to the Controller and
// have succeeded anyway, because the client buffers the message while
// reconnecting and the caller's own context expires before the
// acknowledgement arrives. The Controller then holds no record for that
// device, and its stale-job reclaim reissues the dispatch roughly ten to
// twenty minutes later, far outside JetStream's producer-side duplicate
// window. This is what recognises the second arrival.
func TestAgent_SuppressesADispatchThatAlreadyExecuted(t *testing.T) {
	adapter := newCountingAdapter()
	store := &memoryDedupStore{seen: map[string]bool{}}

	// The reissued dispatch, whose work this fleet already completed.
	const key = testJobID + ":device-1"
	store.seen[key] = true

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithDedupStore(store, time.Hour))

	// Acked, because a duplicate is settled rather than left to redeliver.
	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	if got := adapter.total(); got != 0 {
		t.Fatalf("the adapter ran %d times for a dispatch that had already executed, want 0", got)
	}
}

// TestAgent_RunsAndRecordsAFirstDispatch is the other half, and the
// reason the test above is not vacuous: the same wiring must still run
// work it has not seen, and must remember it afterwards.
func TestAgent_RunsAndRecordsAFirstDispatch(t *testing.T) {
	adapter := newCountingAdapter()
	store := &memoryDedupStore{}

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithDedupStore(store, time.Hour))

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	if got := adapter.total(); got != 1 {
		t.Fatalf("the adapter ran %d times for a first dispatch, want 1", got)
	}
	if !store.has(testJobID + ":device-1") {
		t.Fatal("a completed dispatch was not recorded, so a redelivery would run it again")
	}
}
