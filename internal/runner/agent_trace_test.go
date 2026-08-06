package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// tracingMockMsg is a MockMsg that also carries NATS headers, the thing
// handleMessage reads trace context out of.
type tracingMockMsg struct {
	*MockMsg
	headers nats.Header
}

func (m *tracingMockMsg) Headers() nats.Header { return m.headers }

// TestAgent_ContinuesTraceFromMessageHeaders is the far end of PLAN.md
// Section 19's requirement, and the reason injecting trace context on
// publish is not a decoration: a job whose message carries the
// Controller's trace context must be executed inside a span belonging to
// that same trace, not a new unrelated one.
//
// The message headers are produced by the real event.InjectTraceContext,
// not hand-written, so this test breaks if the publish side ever changes
// its spelling.
func TestAgent_ContinuesTraceFromMessageHeaders(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	// Stand in for the Controller: start a request span and inject its
	// context the same way the Bus adapter does on publish.
	apiCtx, apiSpan := tp.Tracer("controller").Start(context.Background(), "api-request")
	headers := nats.Header{}
	event.InjectTraceContext(apiCtx, headers)
	apiSpan.End()

	msg := &tracingMockMsg{
		MockMsg: &MockMsg{data: wireWrapDispatchPayload(
			`{"job_id":"job-1","runbook_id":"pb-1","device_name":"router-1","device_ip":"10.0.0.1"}`)},
		headers: headers,
	}

	agent := runner.NewAgent(
		&MockConsumer{PayloadMsgs: []jetstream.Msg{msg}},
		&MockAdapter{},
		nil,
		5,
		nil,
		tp.Tracer("runner"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = agent.Run(ctx)

	if !msg.ack {
		t.Fatal("the job was not acknowledged, so no consumer span could have completed")
	}

	var consumerSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindConsumer {
			consumerSpan = span
			break
		}
	}
	if consumerSpan == nil {
		t.Fatal("the agent recorded no consumer span for the job it executed")
	}

	if got := consumerSpan.SpanContext().TraceID(); got != apiSpan.SpanContext().TraceID() {
		t.Errorf("the runner's span is in trace %v, want the Controller's trace %v: the trace did not survive the bus boundary",
			got, apiSpan.SpanContext().TraceID())
	}
	if got := consumerSpan.Parent().SpanID(); got != apiSpan.SpanContext().SpanID() {
		t.Errorf("the runner's span is parented to %v, want the Controller's span %v", got, apiSpan.SpanContext().SpanID())
	}

	// Job identity must be on the span, or an operator holding a trace
	// cannot tell which job it describes.
	attrs := make(map[string]string, len(consumerSpan.Attributes()))
	for _, attr := range consumerSpan.Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	if attrs["pleiades.job.id"] != "job-1" {
		t.Errorf("span attribute pleiades.job.id is %q, want %q", attrs["pleiades.job.id"], "job-1")
	}
	if attrs["pleiades.device.name"] != "router-1" {
		t.Errorf("span attribute pleiades.device.name is %q, want %q", attrs["pleiades.device.name"], "router-1")
	}
}

// TestAgent_StartsAnOwnTraceWhenHeadersCarryNone proves the degraded path:
// a message published outside any trace (a replayed message, or one from a
// producer with tracing disabled) still executes, inside a span that roots
// its own trace rather than being dropped or erroring.
func TestAgent_StartsAnOwnTraceWhenHeadersCarryNone(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	msg := &tracingMockMsg{
		MockMsg: &MockMsg{data: wireWrapDispatchPayload(
			`{"job_id":"job-2","runbook_id":"pb-1","device_name":"router-1","device_ip":"10.0.0.1"}`)},
		headers: nats.Header{},
	}

	agent := runner.NewAgent(
		&MockConsumer{PayloadMsgs: []jetstream.Msg{msg}},
		&MockAdapter{},
		nil,
		5,
		nil,
		tp.Tracer("runner"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = agent.Run(ctx)

	if !msg.ack {
		t.Fatal("a job with no trace context was not executed")
	}
	for _, span := range recorder.Ended() {
		if span.SpanKind() != trace.SpanKindConsumer {
			continue
		}
		if span.Parent().IsValid() {
			t.Errorf("the span claims a parent (%v) although the message carried no trace context", span.Parent().SpanID())
		}
		if !span.SpanContext().IsValid() {
			t.Error("the span's own context is not valid")
		}
		return
	}
	t.Fatal("the agent recorded no consumer span")
}
