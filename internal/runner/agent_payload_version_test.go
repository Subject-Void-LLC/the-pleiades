// Package runner_test: a dispatch from a newer Controller is refused by name.
package runner_test

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

// TestAgent_HandleMessage_ANewerPayloadIsRefusedNeverRun proves a payload
// version past this Runner's is terminated before the adapter runs: not
// executed with the fields this Runner would silently drop, not acked as if
// it ran, and not Nak'd, since redelivery to the same binary cannot help.
// The same payload at version 1 runs, which is the control.
func TestAgent_HandleMessage_ANewerPayloadIsRefusedNeverRun(t *testing.T) {
	for _, tc := range []struct {
		version int
		wantRun bool
	}{{version: 2, wantRun: false}, {version: 1, wantRun: true}} {
		t.Run(fmt.Sprintf("version %d", tc.version), func(t *testing.T) {
			body := fmt.Sprintf(`{"payload_version":%d,"job_id":%q,"runbook_id":"pb-1","device_id":"device-1","device_name":"router-1","device_host":"10.0.0.1","interruptible":true}`,
				tc.version, testJobID)
			msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(body)}}
			consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
			adapter := newCountingAdapter()
			agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

			if tc.wantRun {
				runAgentUntil(t, agent, msg.ack.Load, agentSettleTimeout)
			} else {
				runAgentUntil(t, agent, msg.term.Load, agentSettleTimeout)
			}
			if ran := adapter.total() > 0; ran != tc.wantRun {
				t.Errorf("adapter ran = %v, want %v", ran, tc.wantRun)
			}
			if !tc.wantRun && (msg.naked.Load() || msg.ack.Load()) {
				t.Errorf("a newer payload was Nak'd (%v) or acked (%v); want it terminated", msg.naked.Load(), msg.ack.Load())
			}
		})
	}
}
