package runner_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

// BenchmarkAgent_PoolSize1 and BenchmarkAgent_PoolSizeDefault report
// Agent's own end-to-end throughput (fetch, decode, lock acquire/
// release, Execute, Ack) at poolSize=1 (the old, effectively-serial
// shape) versus defaultPoolSize (5), giving a concrete before/after
// number for this phase's own Worker Pool item -- see also
// internal/lock's own nats_bench_test.go/inprocess_bench_test.go for the
// lock-acquisition latency these numbers build on, and internal/dispatch/
// worker_bench_test.go's BenchmarkAnsiblePlaybookFanOutComparable for the
// adjacent, already-established real-`ansible-playbook`-subprocess
// comparison at the fan-out layer: Agent itself is a message pump across
// many devices, not a per-host fan-out, so a direct Agent-vs-
// ansible-playbook number would compare two different things rather than
// giving a meaningful "industry alternative" figure.
func BenchmarkAgent_PoolSize1(b *testing.B) {
	runBenchmarkAgent(b, runner.WithPoolSize(1))
}

func BenchmarkAgent_PoolSizeDefault(b *testing.B) {
	runBenchmarkAgent(b, runner.WithPoolSize(5))
}

// runBenchmarkAgent is BenchmarkAgent_PoolSize1/PoolSizeDefault's shared
// body: build b.N distinct-device messages, run a real Agent (real
// lock.NewInProcessManager(), a MockAdapter that never errors so DLQ
// handling is never reached) with opts applied, and time until every
// message is acked.
func runBenchmarkAgent(b *testing.B, opts ...runner.AgentOption) {
	var msgs []jetstream.Msg
	mockMsgs := make([]*MockMsg, b.N)

	for i := 0; i < b.N; i++ {
		mockMsgs[i] = &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON(fmt.Sprintf("device-%d", i)))}
		msgs = append(msgs, mockMsgs[i])
	}

	consumer := &MockConsumer{PayloadMsgs: msgs}
	// Use discard logger to not artificially slow down benchmark with stdout I/O
	logger := slog.New(slog.NewTextHandler(new(discardWriter), nil))
	// js is nil: MockAdapter never returns an error, so handleMessage's
	// DLQ path (the only code that touches js) is never reached here.
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, logger, nil, opts...)

	b.ResetTimer()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		agent.Run(ctx)
	}()

	// Wait until all messages are acked
	for {
		ackCount := 0
		for _, m := range mockMsgs {
			if m.ack.Load() {
				ackCount++
			}
		}
		if ackCount == b.N {
			break
		}
	}
	cancel()
}

type discardWriter struct{}

func (w *discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

// MockAdapter lives in agent_test.go now; it used to be declared here too,
// which is what actually broke go vet (see FAILURE_PATTERNS.md #14).
