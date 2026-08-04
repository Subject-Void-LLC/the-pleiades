package runner_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

func FuzzAgentPayload(f *testing.F) {
	// Realistic wire content: an Event envelope wrapping the
	// DispatchPayload JSON, matching what a real Bus.Publish call
	// actually puts on the wire (see wireWrapDispatchPayload).
	f.Add(wireWrapDispatchPayload(`{"runbook_id":"pb-1","device_name":"router-1","device_ip":"10.0.0.1"}`))
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
		agent := runner.NewAgent(consumer, &MockAdapter{}, nil, 5, logger)

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
		if !msg.ack && !msg.term {
			t.Errorf("expected message to be acked or terminated to prevent queue blocking")
		}
	})
}
