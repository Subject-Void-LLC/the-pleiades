// Package telemetry: tests of the masked logger's sink, the edge sampler's
// description, the exported policy lists and the masked span's deprecated
// accessor.
package telemetry

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestMaskedLoggerDropsValuesAndNames drives every logr entry point the SDK
// can reach: names and values never print, only level 0 messages do, and a
// message is one line however it was written.
func TestMaskedLoggerDropsValuesAndNames(t *testing.T) {
	const canary = "canarysinkvalue0019"
	var out strings.Builder
	logger := newMaskedLogger(&out).WithName("name-"+canary).WithValues("key", canary)

	logger.Info("configured exporter", "endpoint", canary)
	logger.V(1).Info("a warning the default logger hides", "value", canary)
	logger.V(4).Info("debug", "value", canary)

	got := out.String()
	if strings.Contains(got, canary) {
		t.Errorf("the logger printed a value or a name: %q", got)
	}
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, "telemetry: configured exporter") {
		t.Errorf("want exactly the level-0 message as one line, got %q", got)
	}
}

// TestReportLineCutsOnACharacterBoundary proves a capped line never ends
// inside a multi-byte character, so what is written stays valid UTF-8.
func TestReportLineCutsOnACharacterBoundary(t *testing.T) {
	var out strings.Builder
	// Each character is two bytes and the cap is even, so a cut one byte
	// past it would split one: start one byte off.
	text := "x" + strings.Repeat(string(rune(0xe9)), maxReportBytes)
	newReportWriter(&out).line(text)
	got := strings.TrimSuffix(strings.TrimPrefix(out.String(), "telemetry: "), truncatedMark+"\n")
	if !strings.HasSuffix(out.String(), truncatedMark+"\n") {
		t.Fatalf("a long line was not marked truncated: %q", out.String()[len(out.String())-40:])
	}
	if len(got) > maxReportBytes || !strings.HasPrefix(got, "x") {
		t.Errorf("capped text is %d bytes, want at most %d", len(got), maxReportBytes)
	}
	if !strings.HasSuffix(got, string(rune(0xe9))) {
		t.Errorf("the cut split a character")
	}
}

// TestEdgeSamplerDescribesBothRules keeps the description the SDK prints in
// its diagnostics truthful about the two rules.
func TestEdgeSamplerDescribesBothRules(t *testing.T) {
	d := newEdgeSampler(0.25).Description()
	if !strings.Contains(d, "TraceIDRatioBased{0.25}") || !strings.Contains(d, "ParentBased") {
		t.Errorf("Description() = %q, want the edge ratio and the ParentBased rule", d)
	}
}

// TestExportedPolicyListsAreCopies proves a caller of the exported lists,
// internal/archtest, cannot change the policy through what it is handed.
func TestExportedPolicyListsAreCopies(t *testing.T) {
	keys := AllowedAttributeKeys()
	keys[0] = "pleiades.params.password"
	if slices.Contains(AllowedAttributeKeys(), "pleiades.params.password") {
		t.Error("AllowedAttributeKeys handed out the policy's own slice")
	}
	events := AllowedEventNames()
	events[0] = "free text"
	if slices.Contains(AllowedEventNames(), "free text") {
		t.Error("AllowedEventNames handed out the policy's own slice")
	}
	for _, k := range TelemetryOnlyAttributeKeys() {
		if !slices.Contains(allowedAttributeKeys, k) {
			t.Errorf("telemetry-only key %q is not on the allowlist", k)
		}
	}
}

// TestMaskedSpanDeprecatedScopeAccessorIsMasked proves the deprecated
// InstrumentationLibrary accessor returns the filtered scope, not the
// embedded span's.
func TestMaskedSpanDeprecatedScopeAccessorIsMasked(t *testing.T) {
	policy, err := newAttributePolicy(nil)
	if err != nil {
		t.Fatalf("newAttributePolicy: %v", err)
	}
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	_, span := tp.Tracer("example.com/canaryscope").Start(context.Background(), "op")
	span.End()

	masked := newMaskingExporter(nil, policy, redact.Shared()).mask(recorder.Ended()[0])
	if got := masked.InstrumentationLibrary().Name; got != undeclaredScope { //nolint:staticcheck // the accessor under test
		t.Errorf("InstrumentationLibrary().Name = %q, want %q", got, undeclaredScope)
	}
}
