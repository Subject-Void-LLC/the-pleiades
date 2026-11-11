// Package telemetry_test: the telemetry boundary through Setup, the real
// pipeline a composition root builds, asserting on what its exporter wrote.
package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// exportThroughSetup builds a provider exactly as a composition root does,
// through Setup with the stdout exporter, runs fn against one span, and
// returns everything the exporter wrote. It is the real pipeline: the
// default masker, the fixed allowlist and the batch processor Setup itself
// installs, so what a test reads here is what a collector would receive.
func exportThroughSetup(t *testing.T, cfg telemetry.Config, fn func(span trace.Span)) string {
	t.Helper()

	var out strings.Builder
	if cfg.ServiceName == "" {
		cfg.ServiceName = "telemetry-boundary-test"
	}
	cfg.Exporter = telemetry.ExporterStdout
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStdoutWriterForTest(cfg, &out))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// A declared scope and span name, so the name policy keeps the span
	// recognizable and every assertion below is about the policy under test.
	_, span := provider.Tracer(telemetry.RunnerScope).Start(context.Background(), "runner.handle_message")
	fn(span)
	span.End()

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"Name":"runner.handle_message"`) {
		t.Fatalf("the span was not exported at all, so the assertions below would prove nothing; got %q", got)
	}
	return got
}

// TestSetup_ExportsNoValueUnderAnUnlistedKey is the allowlist through the
// real wiring: an attribute whose key nobody listed never leaves the
// process, whatever it holds, while a listed identifier still does.
func TestSetup_ExportsNoValueUnderAnUnlistedKey(t *testing.T) {
	const canary = "canaryparamvalue0001"
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.SetAttributes(
			attribute.String("pleiades.job.id", "11111111-1111-4111-8111-111111111111"),
			attribute.String("pleiades.params.enable_password", canary),
		)
	})
	if strings.Contains(got, canary) {
		t.Errorf("a value under a key that is not on the allowlist was exported: %s", got)
	}
	if strings.Contains(got, "pleiades.params.enable_password") {
		t.Errorf("a key that is not on the allowlist was exported: %s", got)
	}
	if !strings.Contains(got, "11111111-1111-4111-8111-111111111111") {
		t.Errorf("an allowlisted identifier was dropped: %s", got)
	}
}

// TestSetup_MasksSecretShapesUnderAListedKey covers a value an allowed key
// should never hold but one day will: each shape the shared ruleset knows is
// masked on its way out, under a key the policy otherwise keeps.
func TestSetup_MasksSecretShapesUnderAListedKey(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		canary string
	}{
		{
			name: "a PEM private key",
			value: "-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEowIBAAKCAQEAcanaryPemBody0123456789\n" +
				"-----END RSA PRIVATE KEY-----",
			canary: "canaryPemBody0123456789",
		},
		{
			name: "a bearer token",
			value: "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
				"eyJzdWIiOiJwbGVpYWRlcy10ZXN0In0." +
				"c2lnbmF0dXJlQ2FuYXJ5MDEyMzQ1Njc4OQ",
			canary: "c2lnbmF0dXJlQ2FuYXJ5MDEyMzQ1Njc4OQ",
		},
		{
			name:   "a URL with a password",
			value:  "https://admin:s3cr3tPassw0rdCanary@db.example.net/inventory",
			canary: "s3cr3tPassw0rdCanary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
				span.SetAttributes(attribute.String("pleiades.device.name", tc.value))
			})
			if strings.Contains(got, tc.canary) {
				t.Errorf("%s reached the exporter under an allowlisted key: %s", tc.name, got)
			}
			if !strings.Contains(got, "pleiades.device.name") {
				t.Errorf("the allowlisted key was dropped rather than masked: %s", got)
			}
		})
	}
}

// TestSetup_ExportsNoRecordedErrorText covers a RecordError that got past
// the build-time check: third-party instrumentation, or the SDK recording a
// panic. The exception event keeps its type, an identifier, and loses its
// message and stack, which quote whatever the error quoted.
func TestSetup_ExportsNoRecordedErrorText(t *testing.T) {
	const canary = "canarydeviceoutput0002"
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.RecordError(errors.New("ssh: device said: enable secret 0 "+canary), trace.WithStackTrace(true))
	})
	if strings.Contains(got, canary) {
		t.Errorf("recorded error text reached the exporter: %s", got)
	}
	if strings.Contains(got, "exception.stacktrace") {
		t.Errorf("a stack trace reached the exporter: %s", got)
	}
	if !strings.Contains(got, "exception.type") {
		t.Errorf("the exception event lost its type, which is an identifier and should stay: %s", got)
	}
}

// TestSetup_WithholdsAFreeStatusDescription covers a status description
// built at run time: only a fixed phrase is exported, and the failure
// itself stays visible.
func TestSetup_WithholdsAFreeStatusDescription(t *testing.T) {
	const canary = "canarystatuspassword0003"
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.SetStatus(codes.Error, "login failed for admin:"+canary+"@10.0.0.1")
	})
	if strings.Contains(got, canary) {
		t.Errorf("a free status description reached the exporter: %s", got)
	}
	if !strings.Contains(got, `"Error"`) {
		t.Errorf("the span is no longer marked failed: %s", got)
	}
}

// TestSetup_DropsEventsAndTraceStateItDoesNotKnow covers the two other
// channels a value can ride: an event named by its caller, and an inbound
// tracestate header copied onto the span context.
func TestSetup_DropsEventsAndTraceStateItDoesNotKnow(t *testing.T) {
	const eventCanary = "canaryeventname0004"
	const stateCanary = "canarytracestate0005"

	var out strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStdoutWriterForTest(
		telemetry.Config{ServiceName: "telemetry-boundary-test", Exporter: telemetry.ExporterStdout}, &out))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// The propagator no longer extracts tracestate, so the parent is built
	// directly, as third-party instrumentation or a future propagator could
	// hand one in: the exporter must strip it on its own.
	state, err := trace.ParseTraceState("vendor=" + stateCanary)
	if err != nil {
		t.Fatalf("ParseTraceState: %v", err)
	}
	parent := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9},
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
		TraceState: state,
		Remote:     true,
	}))
	_, span := provider.Tracer(telemetry.RunnerScope).Start(parent, "runner.handle_message", trace.WithSpanKind(trace.SpanKindConsumer))
	span.AddEvent("user typed " + eventCanary)
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "runner.handle_message") {
		t.Fatalf("the span was not exported at all; got %q", got)
	}
	if strings.Contains(got, eventCanary) {
		t.Errorf("an event the policy does not know was exported: %s", got)
	}
	if strings.Contains(got, stateCanary) {
		t.Errorf("an inbound tracestate value was exported: %s", got)
	}
}

// TestPropagator_CarriesNoBaggage holds the one wire encoding to trace
// context alone. W3C Baggage is caller-chosen key and value text from an
// inbound header that nothing in this platform reads; extracted, it would
// ride a request's context to any instrumentation that injects the global
// propagator into an outbound request.
func TestPropagator_CarriesNoBaggage(t *testing.T) {
	const canary = "canarybaggagevalue0006"
	in := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"baggage":     "note=" + canary,
	}
	ctx := telemetry.Propagator().Extract(context.Background(), in)

	out := propagation.MapCarrier{}
	telemetry.Propagator().Inject(ctx, out)
	if out["traceparent"] == "" {
		t.Fatalf("trace context was not carried; got %v", out)
	}
	for k, v := range out {
		if strings.EqualFold(k, "baggage") || strings.Contains(v, canary) {
			t.Errorf("the propagator forwarded baggage: %s=%s", k, v)
		}
	}
	for _, field := range telemetry.Propagator().Fields() {
		if strings.EqualFold(field, "baggage") {
			t.Errorf("the propagator still declares the baggage header")
		}
	}
}
