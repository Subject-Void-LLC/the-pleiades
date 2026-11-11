// Package telemetry: the masking exporter, the one place every span passes
// through on its way out of the process.
package telemetry

import (
	"context"
	"fmt"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// maskingExporter applies the attribute policy and the shared masking
// ruleset to every span on its way to the real exporter.
//
// It is an exporter rather than a span processor because a processor cannot
// do this job. OnStart runs before any attribute set after start, any event,
// any recorded error and the status exist, and can only add, never remove.
// OnEnd receives a read-only span. The exporter is the last thing before the
// wire, so whatever reaches it is exactly what would have left the process,
// including what the SDK and third-party instrumentation added themselves
// (a recovered panic's exception event, environment-supplied resource keys).
type maskingExporter struct {
	// next is the real exporter.
	next sdktrace.SpanExporter

	// policy decides which keys, event names and status descriptions may
	// be exported.
	policy *attributePolicy

	// masker masks every string value an allowed key carries. It is the
	// process-wide masker, so a secret registered for the logs is masked on
	// a span too.
	masker *redact.Masker

	// mu guards the one-entry resource cache below. Every span of a
	// provider shares one resource, so filtering it once is enough.
	mu sync.Mutex
	// resIn is the last resource filtered.
	resIn *resource.Resource
	// resOut is what resIn filtered to.
	resOut *resource.Resource
}

// newMaskingExporter wraps next. Setup is the only production caller, and
// internal/archtest keeps span pipelines from being built anywhere else.
func newMaskingExporter(next sdktrace.SpanExporter, policy *attributePolicy, masker *redact.Masker) *maskingExporter {
	return &maskingExporter{next: next, policy: policy, masker: masker}
}

// ExportSpans masks spans and hands the result to the real exporter. A batch
// that cannot be masked is dropped, never exported as it is.
func (e *maskingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	masked, err := e.maskAll(spans)
	if err != nil {
		return err
	}
	return e.next.ExportSpans(ctx, masked)
}

// Shutdown releases the real exporter.
func (e *maskingExporter) Shutdown(ctx context.Context) error {
	return e.next.Shutdown(ctx)
}

// maskAll masks a batch, turning a panic in masking into an error. The
// export runs on the batch processor's goroutine, where an unrecovered panic
// would end the process. The error deliberately does not include the
// recovered value, which could quote the very text being masked.
func (e *maskingExporter) maskAll(spans []sdktrace.ReadOnlySpan) (masked []sdktrace.ReadOnlySpan, err error) {
	defer func() {
		if recover() != nil {
			masked = nil
			err = fmt.Errorf("telemetry: masking a batch of %d spans panicked, so the batch was dropped unexported", len(spans))
		}
	}()

	masked = make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, s := range spans {
		masked = append(masked, e.mask(s))
	}
	return masked, nil
}

// mask builds the snapshot the real exporter sees in place of s. It is
// computed eagerly, once, so every accessor of the result is consistent.
func (e *maskingExporter) mask(s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	attrs, dropped := e.attributes(s.Attributes())

	// An event whose name is not on the list is dropped whole: its name is
	// free text, and the attributes of an unknown event are not worth the
	// risk of keeping.
	source := s.Events()
	events := make([]sdktrace.Event, 0, len(source))
	droppedEvents := s.DroppedEvents()
	for _, ev := range source {
		if !e.policy.allowsEvent(ev.Name) {
			droppedEvents++
			continue
		}
		kept, d := e.attributes(ev.Attributes)
		ev.Attributes = kept
		ev.DroppedAttributeCount += d
		events = append(events, ev)
	}

	sourceLinks := s.Links()
	links := make([]sdktrace.Link, 0, len(sourceLinks))
	for _, l := range sourceLinks {
		kept, d := e.attributes(l.Attributes)
		l.SpanContext = withoutTraceState(l.SpanContext)
		l.Attributes = kept
		l.DroppedAttributeCount += d
		links = append(links, l)
	}

	status := s.Status()
	status.Description = e.policy.description(status.Description)

	// The scope's name and version are kept only for a declared scope, and
	// its schema URL never: each is text whoever asked for the tracer chose.
	scope := s.InstrumentationScope()
	scope.Name, scope.Version = scopeIdentity(scope.Name, scope.Version)
	scope.SchemaURL = ""
	scopeAttrs, _ := e.attributes(scope.Attributes.ToSlice())
	scope.Attributes = attribute.NewSet(scopeAttrs...)

	return maskedSpan{
		ReadOnlySpan:      s,
		name:              spanName(scope.Name, s.Name()),
		spanContext:       withoutTraceState(s.SpanContext()),
		parent:            withoutTraceState(s.Parent()),
		attributes:        attrs,
		events:            events,
		links:             links,
		status:            status,
		scope:             scope,
		resource:          e.maskResource(s.Resource()),
		droppedAttributes: s.DroppedAttributes() + dropped,
		droppedEvents:     droppedEvents,
	}
}

// attributes applies the policy to one attribute list: a key that is not
// allowed is dropped, and an allowed key keeps its value only after masking.
// It reports how many were dropped so the count can be exported, which is
// what keeps a dropped attribute from being silent.
func (e *maskingExporter) attributes(in []attribute.KeyValue) (out []attribute.KeyValue, dropped int) {
	for _, kv := range in {
		if !e.policy.allowsKey(kv.Key) {
			dropped++
			continue
		}
		v, ok := e.value(kv.Value)
		if !ok {
			dropped++
			continue
		}
		// error.type is a closed set: whoever else writes the key, only a
		// declared category leaves.
		if kv.Key == errorTypeKey {
			v = attribute.StringValue(e.policy.errorType(v.Emit()))
		}
		kv.Value = v
		out = append(out, kv)
	}
	return out, dropped
}

// value masks one attribute value. A kind that cannot carry text passes
// through, as it does in redact.Masker.Attr. A string is masked by the shared
// ruleset. Any other kind (bytes, a nested slice or map, and whatever a later
// SDK release adds) is refused, so a value this function cannot read is
// dropped rather than exported unread.
func (e *maskingExporter) value(v attribute.Value) (attribute.Value, bool) {
	switch v.Type() {
	case attribute.BOOL, attribute.INT64, attribute.FLOAT64,
		attribute.BOOLSLICE, attribute.INT64SLICE, attribute.FLOAT64SLICE:
		return v, true
	case attribute.STRING:
		return attribute.StringValue(e.masker.Text(nil, v.AsString())), true
	case attribute.STRINGSLICE:
		in := v.AsStringSlice()
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = e.masker.Text(nil, s)
		}
		return attribute.StringSliceValue(out), true
	default:
		return attribute.Value{}, false
	}
}

// maskResource applies the policy to the resource. resource.Default reads
// OTEL_RESOURCE_ATTRIBUTES, so the environment can put any key on the
// resource; filtering here is what makes "configuration may narrow the
// allowlist, never widen it" true of the environment as well.
func (e *maskingExporter) maskResource(r *resource.Resource) *resource.Resource {
	if r == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if r == e.resIn {
		return e.resOut
	}
	kept, _ := e.attributes(r.Attributes())
	e.resIn = r
	e.resOut = resource.NewWithAttributes(r.SchemaURL(), kept...)
	return e.resOut
}

// withoutTraceState returns sc with its tracestate removed. Tracestate is
// the value of an inbound header, copied verbatim, and header values are not
// exported. Propagation is untouched: this changes only the copy the
// exporter serializes.
func withoutTraceState(sc trace.SpanContext) trace.SpanContext {
	return sc.WithTraceState(trace.TraceState{})
}

// maskedSpan is the snapshot the real exporter receives. It embeds the
// original span so the SDK's unexported interface method is satisfied and
// the accessors that carry no text (kind, times, counts) pass through. Every
// accessor that carries text or attributes is overridden below;
// TestMaskedSpanAccountsForEveryReadOnlySpanMethod fails when the SDK adds
// one this type has not been reviewed against.
type maskedSpan struct {
	sdktrace.ReadOnlySpan

	// name is a declared span name, or the scope's name plus " span".
	name string
	// spanContext is the span's context without its tracestate.
	spanContext trace.SpanContext
	// parent is the parent's context without its tracestate.
	parent trace.SpanContext
	// attributes are the allowed attributes, masked.
	attributes []attribute.KeyValue
	// events are the allowed events, with allowed, masked attributes.
	events []sdktrace.Event
	// links are the links, with allowed, masked attributes.
	links []sdktrace.Link
	// status carries a fixed-phrase description.
	status sdktrace.Status
	// scope is the instrumentation scope, declared or replaced, filtered.
	scope instrumentation.Scope
	// resource is the resource, filtered.
	resource *resource.Resource
	// droppedAttributes adds the policy's drops to the SDK's count.
	droppedAttributes int
	// droppedEvents adds the policy's drops to the SDK's count.
	droppedEvents int
}

// Name returns the span name the policy allows.
func (s maskedSpan) Name() string { return s.name }

// SpanContext returns the span context without its tracestate.
func (s maskedSpan) SpanContext() trace.SpanContext { return s.spanContext }

// Parent returns the parent span context without its tracestate.
func (s maskedSpan) Parent() trace.SpanContext { return s.parent }

// Attributes returns the allowed attributes, masked.
func (s maskedSpan) Attributes() []attribute.KeyValue { return s.attributes }

// Events returns the allowed events with allowed, masked attributes.
func (s maskedSpan) Events() []sdktrace.Event { return s.events }

// Links returns the links with allowed, masked attributes.
func (s maskedSpan) Links() []sdktrace.Link { return s.links }

// Status returns the status with a fixed-phrase description.
func (s maskedSpan) Status() sdktrace.Status { return s.status }

// InstrumentationScope returns the scope with allowed attributes only.
func (s maskedSpan) InstrumentationScope() instrumentation.Scope { return s.scope }

// InstrumentationLibrary is the deprecated spelling of InstrumentationScope.
// It is overridden too so an exporter that still calls it cannot read the
// unmasked scope through the embedded span.
func (s maskedSpan) InstrumentationLibrary() instrumentation.Library { //nolint:staticcheck // the interface still declares it
	return s.scope
}

// Resource returns the resource with allowed attributes only.
func (s maskedSpan) Resource() *resource.Resource { return s.resource }

// DroppedAttributes adds the attributes the policy dropped to the SDK's own
// count, so a key that is not on the allowlist is visible as a number on the
// span rather than only as a gap in a dashboard.
func (s maskedSpan) DroppedAttributes() int { return s.droppedAttributes }

// DroppedEvents adds the events the policy dropped to the SDK's own count.
func (s maskedSpan) DroppedEvents() int { return s.droppedEvents }
