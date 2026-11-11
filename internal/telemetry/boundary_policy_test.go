// Package telemetry_test: the attribute policy's configurable and fixed
// parts through Setup: the deny list, the resource, recorded failures, a
// recovered panic and the SDK's own error reports.
package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TestSetup_KeepsARecordedFailure proves the supported path survives the
// policy that removes the unsupported one: RecordFailure's category and
// phrase reach the exporter unchanged.
func TestSetup_KeepsARecordedFailure(t *testing.T) {
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		telemetry.RecordFailure(span, telemetry.OutcomeAdapterFailed)
	})
	if !strings.Contains(got, `"Description":"adapter execution failed"`) {
		t.Errorf("the fixed failure phrase was withheld: %s", got)
	}
	if !strings.Contains(got, "adapter_execution_failed") {
		t.Errorf("error.type is missing: %s", got)
	}
}

// TestSetup_KeepsAServerErrorPhrase proves the other fixed phrase source,
// the standard library's text for a 5xx that internal/api's middleware
// sets, is not withheld.
func TestSetup_KeepsAServerErrorPhrase(t *testing.T) {
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		span.SetStatus(codes.Error, "Service Unavailable")
	})
	if !strings.Contains(got, `"Description":"Service Unavailable"`) {
		t.Errorf("a 5xx phrase was withheld: %s", got)
	}
}

// TestSetup_DenyNarrowsTheAllowlist proves the configurable half of the
// policy: a key an operator denies is not exported, and a key that is not
// on the allowlist cannot be named, so configuration can narrow the list
// and never widen it.
func TestSetup_DenyNarrowsTheAllowlist(t *testing.T) {
	got := exportThroughSetup(t, telemetry.Config{DenyAttributeKeys: []string{" pleiades.device.name", ""}}, func(span trace.Span) {
		span.SetAttributes(
			attribute.String("pleiades.device.name", "denieddevicename"),
			attribute.String("pleiades.job.id", "11111111-1111-4111-8111-111111111111"),
		)
	})
	if strings.Contains(got, "denieddevicename") {
		t.Errorf("a denied key was exported: %s", got)
	}
	if !strings.Contains(got, "11111111-1111-4111-8111-111111111111") {
		t.Errorf("denying one key dropped another: %s", got)
	}

	_, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName:       "telemetry-boundary-test",
		DenyAttributeKeys: []string{"pleiades.params.anything"},
	})
	if err == nil || !strings.Contains(err.Error(), "not on the allowlist") {
		t.Errorf("Setup accepted a deny entry that is not on the allowlist (err %v): a typo in a security control must not parse clean", err)
	}
}

// TestConfigFromEnv_ReadsTheDenyList proves an operator can narrow the
// policy through the environment the binaries read, and that the list
// reaches Setup in the shape Setup checks.
func TestConfigFromEnv_ReadsTheDenyList(t *testing.T) {
	t.Setenv(telemetry.DenyAttributesEnv, "url.path, pleiades.device.name")
	cfg, err := telemetry.ConfigFromEnv("pleiades-controller", "")
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	got := exportThroughSetup(t, cfg, func(span trace.Span) {
		span.SetAttributes(
			attribute.String("url.path", "/inventory/devices/deniedpath"),
			attribute.String("pleiades.device.name", "denieddevicename"),
			attribute.String("http.route", "/inventory/devices/{name}"),
		)
	})
	for _, denied := range []string{"deniedpath", "denieddevicename"} {
		if strings.Contains(got, denied) {
			t.Errorf("%q was exported although its key is denied: %s", denied, got)
		}
	}
	if !strings.Contains(got, "/inventory/devices/{name}") {
		t.Errorf("a key nobody denied was dropped: %s", got)
	}

	t.Setenv(telemetry.DenyAttributesEnv, "pleiades.params.password")
	cfg, err = telemetry.ConfigFromEnv("pleiades-controller", "")
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if _, err := telemetry.Setup(context.Background(), cfg); err == nil {
		t.Error("Setup accepted a deny entry from the environment that is not on the allowlist")
	}
}

// TestSetup_FiltersTheResourceTheEnvironmentSets holds the environment to
// the same rule as code: OTEL_RESOURCE_ATTRIBUTES can fill an allowlisted
// key and cannot add one.
func TestSetup_FiltersTheResourceTheEnvironmentSets(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging,db.password=canaryresourcepw0007")
	got := exportThroughSetup(t, telemetry.Config{}, func(trace.Span) {})
	if strings.Contains(got, "canaryresourcepw0007") || strings.Contains(got, "db.password") {
		t.Errorf("a resource key that is not on the allowlist was exported: %s", got)
	}
	if !strings.Contains(got, "staging") {
		t.Errorf("an allowlisted resource key set by the environment was dropped: %s", got)
	}
}

// TestSetup_ExportsNoPanicText covers the exception event the SDK writes on
// its own when a span ends during a panic: no RecordError call is involved,
// so only the exporter can stop the panic's value leaving the process.
func TestSetup_ExportsNoPanicText(t *testing.T) {
	const canary = "canarypanicvalue0008"
	got := exportThroughSetup(t, telemetry.Config{}, func(span trace.Span) {
		func() {
			defer func() { _ = recover() }()
			func() {
				defer span.End()
				panic("device said " + canary)
			}()
		}()
	})
	if strings.Contains(got, canary) {
		t.Errorf("a recovered panic's value reached the exporter: %s", got)
	}
	if !strings.Contains(got, "exception.type") {
		t.Errorf("the panic's exception event lost its type: %s", got)
	}
}

// TestSetup_MasksTheSDKsOwnErrorReports covers the last way the tracing
// SDK writes text: its global error handler. Setup installs one that
// writes through the shared masker, so an export error quoting a token is
// masked the way a log line quoting it would be.
func TestSetup_MasksTheSDKsOwnErrorReports(t *testing.T) {
	const canary = "c2lnbmF0dXJlQ2FuYXJ5MDEyMzQ1Njc4OQ"
	var errOut strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStderrWriterForTest(
		telemetry.Config{ServiceName: "telemetry-boundary-test"}, &errOut))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	otel.Handle(errors.New("traces export: collector said Authorization: Bearer " +
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJwbGVpYWRlcy10ZXN0In0." + canary))
	otel.Handle(nil)

	got := errOut.String()
	if !strings.Contains(got, "telemetry: traces export") {
		t.Fatalf("the SDK's error report did not reach the handler Setup installed; got %q", got)
	}
	if strings.Contains(got, canary) {
		t.Errorf("the SDK's error report was written unmasked: %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("a nil error was reported, or a report was not one line: %q", got)
	}
}
