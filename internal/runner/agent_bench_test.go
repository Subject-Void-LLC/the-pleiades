package runner_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

func BenchmarkAgent(b *testing.B) {
	// Create N messages
	var msgs []jetstream.Msg
	mockMsgs := make([]*MockMsg, b.N)

	for i := 0; i < b.N; i++ {
		mockMsgs[i] = &MockMsg{data: wireWrapDispatchPayload(`{"runbook_id":"pb-1","device_name":"router-1","device_ip":"10.0.0.1"}`)}
		msgs = append(msgs, mockMsgs[i])
	}

	consumer := &MockConsumer{PayloadMsgs: msgs}
	// Use discard logger to not artificially slow down benchmark with stdout I/O
	logger := slog.New(slog.NewTextHandler(new(discardWriter), nil))
	// js is nil: MockAdapter never returns an error, so handleMessage's
	// DLQ path (the only code that touches js) is never reached here.
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, 5, logger, nil)

	b.ResetTimer()

	// Run the agent. We pass a pre-cancelled context but since the logic checks for context
	// cancel *before* pulling, we will actually just do the processing inline. Wait, no,
	// Run blocks until context cancelled. We can't do it that way. Let's just create a context,
	// run it in a goroutine, and then cancel it when messages are empty. But this is racy.

	// A better benchmark is just to loop over the messages and call the un-exported handleMessage.
	// But we can't call unexported handleMessage from external test package.

	// So we'll just run it with a timeout.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		agent.Run(ctx)
	}()

	// Wait until all messages are acked
	for {
		ackCount := 0
		for _, m := range mockMsgs {
			if m.ack {
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
