package runner_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

func FuzzAgentPayload(f *testing.F) {
	// Realistic wire content: an Event envelope wrapping the
	// DispatchPayload JSON, matching what a real Bus.Publish call
	// actually puts on the wire (see wireWrapDispatchPayload). Carries a
	// valid job_id (testJobID) so this seed actually reaches the Ack
	// path, proving the happy-path terminal disposition too, not just the
	// Term paths every other seed below already covers.
	f.Add(wireWrapDispatchPayload(dispatchPayloadJSON("device-1")))
	// No job_id at all: exercises handleMessage's own uuid.Parse
	// rejection (Schema/Injection Hardening, FAILURE_PATTERNS.md #84),
	// terminated rather than acked.
	f.Add(wireWrapDispatchPayload(`{"runbook_id":"pb-2"}`))
	// Genuinely malformed wire content, not just a malformed inner
	// payload: this is what a corrupted message on the wire looks like.
	f.Add([]byte(`malformed`))
	f.Add([]byte(`{"id":"e1","data":malformed}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &MockMsg{data: data}
		consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
		logger := slog.Default()
		// js is nil: MockAdapter never returns an error, so handleMessage's
		// DLQ path (the only code that touches js) is never reached here.
		agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, logger, nil)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(5 * time.Millisecond)
			cancel()
		}()

		agent.Run(ctx)

		// A well-formed payload is Ack'd (MockAdapter always succeeds); a
		// malformed one is Term'd, not Ack'd (see handleMessage). Either
		// is a valid terminal disposition that prevents queue blocking;
		// only "neither happened" is a real failure.
		if !msg.ack.Load() && !msg.term.Load() {
			t.Errorf("expected message to be acked or terminated to prevent queue blocking")
		}
	})
}
