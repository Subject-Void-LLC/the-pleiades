package runner_test

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go/jetstream"
)

// TestAgent_HandleMessage_RejectsSubjectInjectingJobID is the regression
// test for FAILURE_PATTERNS.md #84: wire.DispatchPayload.JobID reaches
// topology.LogSubject (native.Adapter.streamLog) and topology.
// ResultSubject (agent_wal.go's flushOne) by bare string concatenation,
// with NATS subject wildcards (">", "*") and separators (".") being
// ordinary characters in an ordinary Go string. This mirrors
// internal/api/logs_test.go's own TestStreamLogs_RejectsSubjectInjectingJobIDs
// table exactly, the identical vulnerability shape at the Runner's own
// entry point rather than the HTTP-facing one that test already covers.
//
// The assertion is that a hostile job_id is Term'd, exactly like this
// codebase's other permanently-malformed-payload branches immediately
// above it in handleMessage, never Ack'd (which would silently execute
// against a poisoned JobID) and never left to reach any subject at all.
func TestAgent_HandleMessage_RejectsSubjectInjectingJobID(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
	}{
		{name: "full wildcard", jobID: ">"},
		{name: "token wildcard", jobID: "*"},
		{name: "wildcard suffix", jobID: "1f8c8a48.>"},
		{name: "subject separator", jobID: "1f8c8a48.0f21"},
		{name: "empty", jobID: ""},
		{name: "space", jobID: " "},
		{name: "newline injection", jobID: "1f8c8a48-0f21-4c65-9a45-2f6bd9b0a111\ndata: forged"},
		{name: "not a uuid", jobID: "123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payloadJSON := fmt.Sprintf(`{"job_id":%q,"runbook_id":"pb-1","device_id":"device-1","device_name":"router-1","device_host":"10.0.0.1"}`, tt.jobID)
			msg := &MockMsg{data: wireWrapDispatchPayload(payloadJSON)}
			consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
			agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

			runAgentUntil(t, agent, msg.term.Load, 10*time.Second)

			if msg.ack.Load() {
				t.Errorf("job id %q was acked: it reached execution instead of being refused", tt.jobID)
			}
			if !msg.term.Load() {
				t.Errorf("job id %q was not terminated: a hostile job_id must never be left for redelivery either", tt.jobID)
			}
		})
	}
}
