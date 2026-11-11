// Package telemetry: tests of the masking exporter's parts that the
// exported API cannot reach, such as its drop counts, its fail-closed batch
// and the guard on ReadOnlySpan's method set.
package telemetry

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// exportMasked runs fn against one span on a provider whose only exporter
// is an in-memory one behind the masking exporter, with the production
// policy and masker, and returns the one span that reached it.
func exportMasked(t *testing.T, fn func(tp *sdktrace.TracerProvider, span trace.Span)) tracetest.SpanStub {
	t.Helper()

	policy, err := newAttributePolicy(nil)
	if err != nil {
		t.Fatalf("newAttributePolicy: %v", err)
	}
	mem := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(newMaskingExporter(mem, policy, redact.Shared())))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, span := tp.Tracer("masking_test").Start(context.Background(), "operation")
	fn(tp, span)
	span.End()

	spans := mem.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans, want 1", len(spans))
	}
	return spans[0]
}

// valueOf finds one attribute by key.
func valueOf(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// TestMaskingExporterCountsWhatItDrops proves a dropped attribute or event
// is visible as a number on the span rather than only as a gap.
func TestMaskingExporterCountsWhatItDrops(t *testing.T) {
	stub := exportMasked(t, func(_ *sdktrace.TracerProvider, span trace.Span) {
		span.SetAttributes(
			attribute.String("pleiades.job.id", "job"),
			attribute.String("pleiades.params.secret", "value"),
			// An allowed key whose value kind the exporter cannot read is
			// dropped too, rather than exported unread.
			attribute.ByteSlice("url.path", []byte("raw bytes")),
		)
		span.AddEvent("free text")
	})
	if stub.DroppedAttributes != 2 {
		t.Errorf("DroppedAttributes = %d, want 2", stub.DroppedAttributes)
	}
	if stub.DroppedEvents != 1 || len(stub.Events) != 0 {
		t.Errorf("DroppedEvents = %d with %d events exported, want 1 and 0", stub.DroppedEvents, len(stub.Events))
	}
}

// TestMaskingExporterMasksEveryTextChannel covers the channels beside the
// span's own attributes: string slices, the exception event's attributes,
// a link's attributes and tracestate, and the span name.
func TestMaskingExporterMasksEveryTextChannel(t *testing.T) {
	const pem = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAcanaryPemBody0123456789\n-----END RSA PRIVATE KEY-----"
	const canary = "canaryPemBody0123456789"

	state, err := trace.ParseTraceState("vendor=linkstate")
	if err != nil {
		t.Fatalf("ParseTraceState: %v", err)
	}
	linked := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceState: state,
	})

	stub := exportMasked(t, func(tp *sdktrace.TracerProvider, span trace.Span) {
		span.SetName("op " + pem)
		span.SetAttributes(
			attribute.StringSlice("url.path", []string{"/ok", pem}),
			attribute.Int("http.response.status_code", 500),
			attribute.Bool("pleiades.dispatch.duplicate", true),
		)
		span.AddLink(trace.Link{SpanContext: linked, Attributes: []attribute.KeyValue{
			attribute.String("pleiades.job.id", pem),
			attribute.String("pleiades.params.x", "y"),
		}})
		span.RecordError(errors.New("boom"))
	})

	if got := stub.Name; got == "op "+pem {
		t.Errorf("the span name was exported unmasked")
	}
	if v, ok := valueOf(stub.Attributes, "url.path"); !ok || v.AsStringSlice()[0] != "/ok" || v.AsStringSlice()[1] == pem {
		t.Errorf("url.path slice = %v (present %v), want the first entry kept and the second masked", v.AsStringSlice(), ok)
	}
	if v, ok := valueOf(stub.Attributes, "http.response.status_code"); !ok || v.AsInt64() != 500 {
		t.Errorf("a number under an allowed key did not pass through: %v %v", v, ok)
	}
	if v, ok := valueOf(stub.Attributes, "pleiades.dispatch.duplicate"); !ok || !v.AsBool() {
		t.Errorf("a bool under an allowed key did not pass through: %v %v", v, ok)
	}
	if len(stub.Links) != 1 {
		t.Fatalf("exported %d links, want 1", len(stub.Links))
	}
	link := stub.Links[0]
	if link.SpanContext.TraceState().Len() != 0 {
		t.Errorf("a link's tracestate was exported: %v", link.SpanContext.TraceState())
	}
	if v, _ := valueOf(link.Attributes, "pleiades.job.id"); v.AsString() == pem {
		t.Errorf("a link attribute was exported unmasked")
	}
	if _, ok := valueOf(link.Attributes, "pleiades.params.x"); ok || link.DroppedAttributeCount != 1 {
		t.Errorf("a link's unlisted attribute was kept or not counted (dropped %d)", link.DroppedAttributeCount)
	}
	for _, kv := range stub.Events[0].Attributes {
		if kv.Key == "exception.message" {
			t.Errorf("exception.message was exported")
		}
	}
	for _, s := range []string{stub.Name, stub.Status.Description} {
		if strings.Contains(s, canary) {
			t.Errorf("a PEM body reached the exporter in %q", s)
		}
	}
}

// TestMaskingExporterDropsABatchItCannotMask proves the fail-closed half of
// maskAll: a panic while masking drops the batch with an error that does not
// quote the panic, and the real exporter receives nothing.
func TestMaskingExporterDropsABatchItCannotMask(t *testing.T) {
	policy, err := newAttributePolicy(nil)
	if err != nil {
		t.Fatalf("newAttributePolicy: %v", err)
	}
	mem := tracetest.NewInMemoryExporter()
	exp := newMaskingExporter(mem, policy, redact.Shared())

	err = exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{panickingSpan{}})
	if err == nil {
		t.Fatal("a batch that could not be masked was reported exported")
	}
	if strings.Contains(err.Error(), "secret-in-panic") {
		t.Errorf("the error quotes the panic's value: %v", err)
	}
	if got := len(mem.GetSpans()); got != 0 {
		t.Errorf("the real exporter received %d spans from a batch that could not be masked", got)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// panickingSpan is a ReadOnlySpan whose accessors panic, standing in for an
// SDK snapshot the exporter cannot read. Embedding the interface satisfies
// its unexported method; every accessor the exporter calls first panics.
type panickingSpan struct{ sdktrace.ReadOnlySpan }

// Attributes panics with a value that must not reach the error.
func (panickingSpan) Attributes() []attribute.KeyValue { panic("secret-in-panic") }

// TestMaskedSpanAccountsForEveryReadOnlySpanMethod guards the one way the
// exporter can start missing something without any code here changing: the
// SDK documents that ReadOnlySpan may gain methods in a minor release, and a
// new one would pass through the embedded span unmasked.
func TestMaskedSpanAccountsForEveryReadOnlySpanMethod(t *testing.T) {
	reviewed := map[string]string{
		"Name":                   "declared names only",
		"SpanContext":            "tracestate removed",
		"Parent":                 "tracestate removed",
		"Attributes":             "filtered and masked",
		"Links":                  "filtered and masked",
		"Events":                 "filtered by name, attributes filtered and masked",
		"Status":                 "fixed phrase",
		"InstrumentationScope":   "masked and filtered",
		"InstrumentationLibrary": "masked and filtered",
		"Resource":               "filtered and masked",
		"DroppedAttributes":      "policy drops added",
		"DroppedEvents":          "policy drops added",
		"SpanKind":               "an enum, passes through",
		"StartTime":              "a time, passes through",
		"EndTime":                "a time, passes through",
		"DroppedLinks":           "a count, passes through",
		"ChildSpanCount":         "a count, passes through",
		"private":                "the SDK's own seal",
	}

	typ := reflect.TypeFor[sdktrace.ReadOnlySpan]()
	if typ.NumMethod() < len(reviewed)-1 {
		t.Fatalf("reflection saw %d methods, fewer than the %d reviewed, so this guard is not reading the interface", typ.NumMethod(), len(reviewed)-1)
	}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if _, ok := reviewed[name]; !ok {
			t.Errorf("ReadOnlySpan has a method %q that maskedSpan has not been reviewed against; "+
				"decide whether it carries text, override it if so, and add it here", name)
		}
	}
}

// TestDeclaredOutcomesCoverEveryOutcome holds declaredOutcomes in step with
// the Outcome values outcome.go declares: an Outcome left out of it would
// have its phrase withheld at export, silently.
func TestDeclaredOutcomesCoverEveryOutcome(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "outcome.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing outcome.go: %v", err)
	}
	literals := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok {
			if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "Outcome" {
				literals++
			}
		}
		return true
	})
	declared := declaredOutcomes()
	if literals != len(declared) {
		t.Errorf("outcome.go builds %d Outcome values and declaredOutcomes lists %d", literals, len(declared))
	}
	seen := make(map[string]bool)
	policy, err := newAttributePolicy(nil)
	if err != nil {
		t.Fatalf("newAttributePolicy: %v", err)
	}
	for _, o := range declared {
		if seen[o.category] {
			t.Errorf("category %q is declared twice", o.category)
		}
		seen[o.category] = true
		if policy.description(o.phrase) != o.phrase {
			t.Errorf("the policy withholds the declared phrase %q", o.phrase)
		}
	}
}

// TestRecordFailureTreatsAZeroOutcomeAsUnclassified proves a caller that
// passes the zero Outcome still marks the span failed with a fixed phrase.
func TestRecordFailureTreatsAZeroOutcomeAsUnclassified(t *testing.T) {
	stub := exportMasked(t, func(_ *sdktrace.TracerProvider, span trace.Span) {
		RecordFailure(span, Outcome{})
	})
	if stub.Status.Description != outcomeUnclassified.phrase {
		t.Errorf("status description = %q, want %q", stub.Status.Description, outcomeUnclassified.phrase)
	}
	if v, _ := valueOf(stub.Attributes, "error.type"); v.AsString() != outcomeUnclassified.category {
		t.Errorf("error.type = %q, want %q", v.AsString(), outcomeUnclassified.category)
	}
}

// TestMaskResourceHandlesNoResource covers the nil resource a hand-built
// snapshot may carry.
func TestMaskResourceHandlesNoResource(t *testing.T) {
	policy, err := newAttributePolicy(nil)
	if err != nil {
		t.Fatalf("newAttributePolicy: %v", err)
	}
	if got := newMaskingExporter(nil, policy, redact.Shared()).maskResource(nil); got != nil {
		t.Errorf("maskResource(nil) = %v, want nil", got)
	}
}
