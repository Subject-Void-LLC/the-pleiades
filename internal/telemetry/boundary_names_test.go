// Package telemetry_test: span names, instrumentation scopes, span limits,
// the SDK's internal logger and error reports, propagation and sampling,
// each through Setup.
package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// exportNamed is exportThroughSetup for a span whose scope, scope options
// and name the test chooses. It returns everything the exporter wrote.
func exportNamed(t *testing.T, scope, name string, opts ...trace.TracerOption) string {
	t.Helper()
	var out strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStdoutWriterForTest(
		telemetry.Config{ServiceName: "telemetry-boundary-test", Exporter: telemetry.ExporterStdout}, &out))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, span := provider.Tracer(scope, opts...).Start(context.Background(), name)
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"SpanContext"`) {
		t.Fatalf("no span was exported; got %q", got)
	}
	return got
}

// TestSetup_ExportsOnlyDeclaredSpanNames covers the span name, which is
// free text to whoever starts a span: third-party instrumentation (the
// Docker client names its spans after the request path) cannot put a value
// there. A declared name and the API's method-and-route shape survive;
// anything else becomes the scope's name plus " span".
func TestSetup_ExportsOnlyDeclaredSpanNames(t *testing.T) {
	const canary = "canaryspanname0012"
	cases := []struct {
		name, scope, span string
		want              string
	}{
		{"a declared runner name", telemetry.RunnerScope, "runner.handle_message", `"Name":"runner.handle_message"`},
		{"the API's method and route", telemetry.APIScope, "GET /api/v1/jobs/{id}", `"Name":"GET /api/v1/jobs/{id}"`},
		{"the API before routing", telemetry.APIScope, "_OTHER", `"Name":"_OTHER"`},
		{"an unmatched API request", telemetry.APIScope, "POST unmatched", `"Name":"POST unmatched"`},
		{"free text in the API scope", telemetry.APIScope, "login failed " + canary, `"Name":"` + telemetry.APIScope + ` span"`},
		{"a declared third-party scope", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp",
			"GET /v1.43/containers/" + canary + "/json", `"Name":"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp span"`},
		{"an undeclared scope", "example.com/" + canary, "op " + canary, `"Name":"undeclared span"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exportNamed(t, tc.scope, tc.span)
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %s in the export, got %s", tc.want, got)
			}
			if strings.Contains(got, canary) {
				t.Errorf("free text in a span or scope name was exported: %s", got)
			}
		})
	}
}

// TestSetup_ExportsOnlyDeclaredScopeDetails covers the instrumentation
// scope's other text: a version is kept for a declared scope in a
// version's shape, and a schema URL is never exported.
func TestSetup_ExportsOnlyDeclaredScopeDetails(t *testing.T) {
	const canary = "canaryschema0013"
	got := exportNamed(t, telemetry.RunnerScope, "runner.handle_message",
		trace.WithInstrumentationVersion("1.2.3"),
		trace.WithSchemaURL("https://example.com/"+canary))
	if strings.Contains(got, canary) {
		t.Errorf("a scope schema URL was exported: %s", got)
	}
	if !strings.Contains(got, `"Version":"1.2.3"`) {
		t.Errorf("a declared scope's version was dropped: %s", got)
	}

	got = exportNamed(t, telemetry.RunnerScope, "runner.handle_message", trace.WithInstrumentationVersion("v1 "+canary))
	if strings.Contains(got, canary) {
		t.Errorf("a scope version that is not a version was exported: %s", got)
	}
}

// TestSetup_ExportsOnlyDeclaredErrorCategories holds error.type to the
// declared categories: a value written under the key by anything other
// than RecordFailure is replaced.
func TestSetup_ExportsOnlyDeclaredErrorCategories(t *testing.T) {
	const canary = "canaryerrortype0014"
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.SetAttributes(attribute.String("error.type", "device said "+canary))
	})
	if strings.Contains(got, canary) {
		t.Errorf("free text under error.type was exported: %s", got)
	}
	if !strings.Contains(got, `"unclassified"`) {
		t.Errorf("an undeclared error.type was not replaced by the fixed category: %s", got)
	}
}

// TestSetup_CapsAttributeValues proves the span limits Setup installs: a
// value under an allowed key, such as the url.path an unauthenticated
// request supplies, cannot size a span.
func TestSetup_CapsAttributeValues(t *testing.T) {
	long := "/" + strings.Repeat("a", 8000)
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.SetAttributes(attribute.String("url.path", long))
	})
	if strings.Contains(got, strings.Repeat("a", 1025)) {
		t.Errorf("an attribute value longer than the limit was exported (%d bytes of output)", len(got))
	}
	if !strings.Contains(got, strings.Repeat("a", 1000)) {
		t.Errorf("the capped value is missing: %s", got)
	}
}

// TestSetup_TheSDKsInternalLoggerPrintsNoValues covers the logger the SDK
// writes its configuration errors to. otlptracehttp reports a malformed
// OTEL_EXPORTER_OTLP_HEADERS entry with the entry itself as a key-value
// pair, and the default logger printed it raw to standard error. Setup's
// logger writes the message and the error's type, masked, and drops the
// key-value pairs.
func TestSetup_TheSDKsInternalLoggerPrintsNoValues(t *testing.T) {
	const noEquals = "canarynoequals0010"
	const badEscape = "canaryescape0011"
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", noEquals+",Authorization=x%zz"+badEscape)

	var errOut strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStderrWriterForTest(telemetry.Config{
		ServiceName: "telemetry-boundary-test",
		Exporter:    telemetry.ExporterOTLP,
		Endpoint:    "127.0.0.1:4318",
		Insecure:    true,
	}, &errOut))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	got := errOut.String()
	if !strings.Contains(got, "parse headers") || !strings.Contains(got, "escape header value") {
		t.Fatalf("the SDK's configuration errors did not reach the logger Setup installed; got %q", got)
	}
	for _, canary := range []string{noEquals, badEscape} {
		if strings.Contains(got, canary) {
			t.Errorf("the SDK's logger printed a configuration value: %q", got)
		}
	}
}

// TestSetup_ErrorReportsAreOneBoundedLine holds the SDK's error reports to
// one escaped, capped line: a collector's reply can run to megabytes and
// carry newlines or terminal escapes, and written raw it could forge log
// lines of its own.
func TestSetup_ErrorReportsAreOneBoundedLine(t *testing.T) {
	var errOut strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStderrWriterForTest(
		telemetry.Config{ServiceName: "telemetry-boundary-test"}, &errOut))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	esc := string(rune(0x1b))
	otel.Handle(errors.New("collector said:\nlevel=INFO msg=forged " + esc + "[31mred" + strings.Repeat("x", 10000)))

	got := errOut.String()
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Errorf("the report is not exactly one line: %q", got)
	}
	if strings.Contains(got, esc) {
		t.Errorf("a terminal escape was written raw: %q", got)
	}
	if len(got) > 2200 {
		t.Errorf("the report is %d bytes, want it capped near 2 KiB", len(got))
	}
	if !strings.Contains(got, "forged") {
		t.Errorf("the report lost its text: %q", got)
	}
}

// TestPropagator_ExtractsNoTraceState drops inbound tracestate at every
// edge that extracts: a caller's vendor text no longer rides a request's
// context onto the bus or into an outbound request, while the trace itself
// still continues.
func TestPropagator_ExtractsNoTraceState(t *testing.T) {
	const canary = "canarytracestate0015"
	provider, err := telemetry.Setup(context.Background(), telemetry.Config{ServiceName: "telemetry-boundary-test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	in := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"tracestate":  "vendor=" + canary,
	}
	for name, p := range map[string]propagation.TextMapPropagator{
		"the provider's": provider.Propagator(),
		"the global":     otel.GetTextMapPropagator(),
	} {
		ctx := p.Extract(context.Background(), in)
		ctx, span := provider.Tracer(telemetry.APIScope).Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindServer))
		out := propagation.MapCarrier{}
		p.Inject(ctx, out)
		span.End()

		if !strings.HasPrefix(out["traceparent"], "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
			t.Errorf("%s propagator did not continue the inbound trace: %v", name, out)
		}
		for k, v := range out {
			if strings.EqualFold(k, "tracestate") || strings.Contains(v, canary) {
				t.Errorf("%s propagator injected inbound tracestate: %s=%s", name, k, v)
			}
		}
	}
}

// TestSetup_TheEdgeSamplesByItsOwnRatio covers who decides sampling. An
// inbound request's sampled flag is the caller's text, so a server span with
// a remote parent is sampled by the operator's ratio, keeping the caller's
// trace ID for correlation; a consumer span, whose parent is this platform's
// own dispatch, still follows its parent.
func TestSetup_TheEdgeSamplesByItsOwnRatio(t *testing.T) {
	sampledParent := propagation.MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	unsampledParent := propagation.MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"}

	start := func(t *testing.T, ratio float64, carrier propagation.MapCarrier, kind trace.SpanKind) trace.SpanContext {
		t.Helper()
		provider, err := telemetry.Setup(context.Background(), telemetry.Config{ServiceName: "telemetry-boundary-test", SampleRatio: ratio})
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
		ctx := provider.Propagator().Extract(context.Background(), carrier)
		_, span := provider.Tracer(telemetry.APIScope).Start(ctx, "GET", trace.WithSpanKind(kind))
		defer span.End()
		return span.SpanContext()
	}

	// A ratio this small samples none of these trace IDs.
	const tiny = 1e-12
	if sc := start(t, tiny, sampledParent, trace.SpanKindServer); sc.IsSampled() {
		t.Error("an inbound request's sampled flag decided sampling over the operator's ratio")
	} else if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("the edge span lost the caller's trace ID: %s", sc.TraceID())
	}
	if sc := start(t, 0, unsampledParent, trace.SpanKindServer); !sc.IsSampled() {
		t.Error("an inbound request's unsampled flag turned sampling off under a ratio of one")
	}
	if sc := start(t, tiny, sampledParent, trace.SpanKindConsumer); !sc.IsSampled() {
		t.Error("a consumer span did not follow its own platform's sampled parent")
	}
	if sc := start(t, 0, unsampledParent, trace.SpanKindConsumer); sc.IsSampled() {
		t.Error("a consumer span did not follow its own platform's unsampled parent")
	}
}
