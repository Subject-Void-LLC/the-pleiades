// Package runner_test: a dispatch from a newer Controller is refused by name.
package runner_test

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// TestAgent_HandleMessage_ANewerPayloadIsRefusedNeverRun proves a payload
// version past this Runner's is reported failed, naming the version, and
// terminated before the adapter runs: not executed with the fields this
// Runner would silently drop, not acked as if it ran, and not Nak'd, since
// redelivery to the same binary cannot help. A newer payload whose new
// shape fails to decode here (a field retyped) is reported the same way.
// The same payload at version 1 runs, which is the control, and so does one at
// version 2, the version a pinned native dispatch is written at.
func TestAgent_HandleMessage_ANewerPayloadIsRefusedNeverRun(t *testing.T) {
	newer := wire.DispatchPayloadVersion + 1
	want := fmt.Sprintf("version %d and this runner reads up to %d", newer, wire.DispatchPayloadVersion)
	for _, tc := range []struct {
		name    string
		version int
		extra   string
		wantRun bool
	}{
		{name: "a newer version", version: newer},
		{name: "a newer version with a field retyped", version: newer, extra: `,"capabilities":{"ssh":true}`},
		{name: "version 1", version: 1, wantRun: true},
		// Version 2 holds a dispatch to its pinned graph, which this Runner
		// reads; with no graph named it runs as version 1 does.
		{name: "version 2", version: wire.PinnedGraphPayloadVersion, wantRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"payload_version":%d,"job_id":%q,"runbook_id":"pb-1","device_id":"device-1","device_name":"router-1","device_host":"10.0.0.1","interruptible":true%s}`,
				tc.version, testJobID, tc.extra)
			bus := event.NewInProcessBus()
			defer bus.Close()
			received := subscribeResultEntries(t, bus, testJobID)
			msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(body)}}
			consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
			adapter := newCountingAdapter()
			agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
				runner.WithResultReporting(bus))

			if tc.wantRun {
				runAgentUntil(t, agent, msg.ack.Load, agentSettleTimeout)
			} else {
				runAgentUntil(t, agent, msg.term.Load, agentSettleTimeout)
			}
			if ran := adapter.total() > 0; ran != tc.wantRun {
				t.Errorf("adapter ran = %v, want %v", ran, tc.wantRun)
			}
			if tc.wantRun {
				return
			}
			if msg.naked.Load() || msg.ack.Load() {
				t.Errorf("a newer payload was Nak'd (%v) or acked (%v); want it terminated", msg.naked.Load(), msg.ack.Load())
			}
			select {
			case entry := <-received:
				if entry.Outcome != "failed" || !strings.Contains(entry.Reason, want) {
					t.Errorf("result %q %q, want failed naming the version", entry.Outcome, entry.Reason)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no failed result was published, so the job would read dispatched forever")
			}
		})
	}
}
