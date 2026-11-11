// Package telemetry: what an inbound caller's trace context may decide,
// which is the trace it continues and nothing else.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// traceParentOnly is the W3C Trace Context propagator with tracestate
// dropped on the way in.
//
// tracestate is vendor text a caller chooses. Extracted, it rode the
// request's context into every span the request caused and out again on
// every injection: onto the bus with each dispatch, and through the global
// propagator into any outbound request a third-party transport (the Docker
// client's otelhttp) makes with that context. Nothing in this platform
// writes or reads tracestate, so dropping it costs nothing; traceparent,
// which carries the trace ID, the parent span ID and the sampling flag, is
// all a trace needs to continue.
type traceParentOnly struct {
	// tc is the standard propagator this one narrows.
	tc propagation.TraceContext
}

// Inject writes the traceparent of the span in ctx. A span this process
// started carries no tracestate, because Extract never lets one in.
func (p traceParentOnly) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	p.tc.Inject(ctx, carrier)
}

// Extract reads a remote parent from carrier's traceparent and drops its
// tracestate.
func (p traceParentOnly) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	extracted := p.tc.Extract(ctx, carrier)
	sc := trace.SpanContextFromContext(extracted)
	if !sc.IsValid() || sc.TraceState().Len() == 0 {
		return extracted
	}
	return trace.ContextWithRemoteSpanContext(ctx, sc.WithTraceState(trace.TraceState{}))
}

// Fields names the one header this propagator reads and writes.
func (p traceParentOnly) Fields() []string {
	return []string{"traceparent"}
}

// edgeSampler decides sampling for a span this process starts.
//
// A server span whose parent is remote is the API edge: its parent's
// sampled flag is a header a caller wrote, unauthenticated, so it is
// ignored there and the operator's ratio decides, keeping the caller's
// trace ID for correlation. Every other span, a Runner span continuing this
// platform's own dispatch included, follows its parent as ParentBased does,
// which is what keeps one distributed trace whole once the edge decided.
type edgeSampler struct {
	// edge samples a span at the API edge.
	edge sdktrace.Sampler
	// inner samples every other span.
	inner sdktrace.Sampler
}

// newEdgeSampler builds the sampler for ratio, where a ratio of zero or one
// or more means sample everything.
func newEdgeSampler(ratio float64) edgeSampler {
	root := sdktrace.AlwaysSample()
	if ratio > 0 && ratio < 1 {
		root = sdktrace.TraceIDRatioBased(ratio)
	}
	return edgeSampler{edge: root, inner: sdktrace.ParentBased(root)}
}

// ShouldSample applies the edge rule to a server span with a remote parent
// and ParentBased to the rest.
func (s edgeSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	parent := trace.SpanContextFromContext(p.ParentContext)
	if p.Kind == trace.SpanKindServer && parent.IsValid() && parent.IsRemote() {
		return s.edge.ShouldSample(p)
	}
	return s.inner.ShouldSample(p)
}

// Description names the sampler for the SDK's own diagnostics.
func (s edgeSampler) Description() string {
	return "EdgeSampler{edge:" + s.edge.Description() + ",inner:" + s.inner.Description() + "}"
}
