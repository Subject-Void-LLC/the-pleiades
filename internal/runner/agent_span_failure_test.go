// Package runner_test: a failed dispatch's span carries a fixed phrase and
// its log line carries the trace_id.
package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// spanFailureCanary is device output an adapter error quotes. It must never
// reach a span, and must reach the masked log line beside the trace_id.
const spanFailureCanary = "canarydeviceoutput7781"

// quotingAdapter fails the way a real adapter does when a device answers an
// error: the error's text quotes what the device printed.
type quotingAdapter struct{ err error }

// Execute returns the adapter's error.
func (a quotingAdapter) Execute(context.Context, wire.DispatchPayload) (wire.Outcome, error) {
	return wire.Outcome{}, a.err
}

// lockedBuffer is a bytes.Buffer safe to write from the agent's worker
// goroutines and read from the test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns what was written.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestAgent_SpanFailuresAreFixedPhrases is the runner's half of the
// telemetry boundary: each of handleMessage's three failure branches marks
// its span with a fixed category and phrase and never with the error's
// text, which may quote device output, and the error's text goes to the log
// line instead, beside the trace_id that joins the two.
func TestAgent_SpanFailuresAreFixedPhrases(t *testing.T) {
	cases := []struct {
		name     string
		adapter  runner.ExecutionAdapter
		payload  string
		settled  func(*nakTrackingMsg) bool
		category string
		phrase   string
	}{
		{
			name:     "an adapter that ran and failed",
			adapter:  quotingAdapter{err: errors.New("device said: % Invalid input " + spanFailureCanary)},
			payload:  dispatchPayloadJSON("device-1"),
			settled:  func(m *nakTrackingMsg) bool { return m.naked.Load() },
			category: "adapter_execution_failed",
			phrase:   "adapter execution failed",
		},
		{
			name:     "a kind no adapter routes",
			adapter:  quotingAdapter{err: fmt.Errorf("%w: %q", routing.ErrNoAdapter, spanFailureCanary)},
			payload:  dispatchPayloadJSON("device-1"),
			settled:  func(m *nakTrackingMsg) bool { return m.term.Load() },
			category: "no_execution_adapter",
			phrase:   "no execution adapter for this kind",
		},
		{
			name:     "a payload newer than this runner reads",
			adapter:  &MockAdapter{},
			payload:  newerPayloadJSON("device-1"),
			settled:  func(m *nakTrackingMsg) bool { return m.term.Load() },
			category: "payload_version_unsupported",
			phrase:   "dispatch payload version not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			var logs lockedBuffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))

			msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(tc.payload)}}
			agent := runner.NewAgent(&MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}, tc.adapter, nil,
				lock.NewInProcessManager(), 5, logger, tp.Tracer("runner"))
			runAgentUntil(t, agent, func() bool { return tc.settled(msg) }, agentSettleTimeout)

			span := consumerSpan(t, recorder)
			if span.Status().Code != codes.Error || span.Status().Description != tc.phrase {
				t.Errorf("status = %v %q, want Error %q", span.Status().Code, span.Status().Description, tc.phrase)
			}
			gotCategory := ""
			for _, kv := range span.Attributes() {
				if kv.Key == "error.type" {
					gotCategory = kv.Value.AsString()
				}
			}
			if gotCategory != tc.category {
				t.Errorf("error.type = %q, want %q", gotCategory, tc.category)
			}
			for _, ev := range span.Events() {
				t.Errorf("the span carries an event %q, which RecordError would have filled with the error's text", ev.Name)
			}
			if strings.Contains(fmt.Sprint(span.Attributes(), span.Status()), spanFailureCanary) {
				t.Error("the error's text reached the span")
			}

			traceID := span.SpanContext().TraceID().String()
			if !logLineCarries(logs.String(), traceID) {
				t.Errorf("no error log line carries trace_id %s, so the span cannot be joined to the error's text; logs:\n%s", traceID, logs.String())
			}
		})
	}
}

// newerPayloadJSON is dispatchPayloadJSON from a Controller newer than this
// Runner reads.
func newerPayloadJSON(deviceID string) string {
	return strings.Replace(dispatchPayloadJSON(deviceID), `{`, fmt.Sprintf(`{"payload_version":%d,`, wire.DispatchPayloadVersion+1), 1)
}

// consumerSpan returns the one consumer span recorder saw end.
func consumerSpan(t *testing.T, recorder *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindConsumer {
			return span
		}
	}
	t.Fatal("the agent recorded no consumer span")
	return nil
}

// logLineCarries reports whether an error-level JSON log line in logs names
// traceID as its trace_id.
func logLineCarries(logs, traceID string) bool {
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry["level"] == "ERROR" && entry["trace_id"] == traceID {
			return true
		}
	}
	return false
}
