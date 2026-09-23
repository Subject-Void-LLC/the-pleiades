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

// count returns how many distinct keys were marked.
//
// It replaced a has(key) helper deliberately. This is an external test
// package, so it cannot reach the unexported encoder that builds the key,
// and a test that RESTATES the key is a test that cannot notice the
// encoder changing under it. That is not hypothetical: the key spent its
// whole life illegal as a NATS KV key (FAILURE_PATTERNS.md #205) while a
// fixture restating it passed happily. Asserting on the count, and on
// suppression actually happening, pins the behaviour without naming the
// key at all.
func (m *memoryDedupStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen)
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
	store := &memoryDedupStore{}

	// The first arrival, which marks the work as done through whatever key
	// the Agent itself derives. Seeding the store with a hand-written key
	// instead would only prove the fixture agrees with itself.
	first := newCountingAdapter()
	firstMsg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}
	firstAgent := runner.NewAgent(&MockConsumer{PayloadMsgs: []jetstream.Msg{firstMsg}}, first, nil,
		lock.NewInProcessManager(), 5, slog.Default(), nil, runner.WithDedupStore(store, time.Hour))
	runAgentUntil(t, firstAgent, firstMsg.ack.Load, agentSettleTimeout)
	if got := first.total(); got != 1 {
		t.Fatalf("the first arrival ran %d times, want 1: the suppression below would prove nothing", got)
	}

	// The reissued dispatch, whose work this fleet already completed.
	adapter := newCountingAdapter()
	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithDedupStore(store, time.Hour))

	// Acked, because a duplicate is settled rather than left to redeliver.
	runAgentUntil(t, agent, msg.ack.Load, agentSettleTimeout)

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

	runAgentUntil(t, agent, msg.ack.Load, agentSettleTimeout)

	if got := adapter.total(); got != 1 {
		t.Fatalf("the adapter ran %d times for a first dispatch, want 1", got)
	}
	if got := store.count(); got != 1 {
		t.Fatalf("a completed dispatch recorded %d keys, want exactly 1, or a redelivery would run it again", got)
	}
}
