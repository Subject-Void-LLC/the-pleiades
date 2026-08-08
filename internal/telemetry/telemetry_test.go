package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TestSetup_Validation is the table-driven guard on Setup's inputs: a
// configuration that cannot produce working tracing must fail loudly at
// startup rather than silently disable it.
func TestSetup_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     telemetry.Config
		wantErr string
	}{
		{
			name:    "missing service name",
			cfg:     telemetry.Config{},
			wantErr: "ServiceName is required",
		},
		{
			name:    "negative sample ratio",
			cfg:     telemetry.Config{ServiceName: "svc", SampleRatio: -0.5},
			wantErr: "SampleRatio must be between 0 and 1",
		},
		{
			name:    "sample ratio above one",
			cfg:     telemetry.Config{ServiceName: "svc", SampleRatio: 1.5},
			wantErr: "SampleRatio must be between 0 and 1",
		},
		{
			name:    "unknown exporter",
			cfg:     telemetry.Config{ServiceName: "svc", Exporter: "jaeger"},
			wantErr: `unknown exporter "jaeger"`,
		},
		{
			name: "explicit none is valid",
			cfg:  telemetry.Config{ServiceName: "svc", Exporter: telemetry.ExporterNone},
		},
		{
			name: "empty exporter defaults to none",
			cfg:  telemetry.Config{ServiceName: "svc"},
		},
		{
			name: "ratio inside the range is valid",
			cfg:  telemetry.Config{ServiceName: "svc", SampleRatio: 0.25},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := telemetry.Setup(context.Background(), tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Setup(%+v) succeeded, want error containing %q", tt.cfg, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Setup error is %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Setup(%+v): %v", tt.cfg, err)
			}
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("Shutdown: %v", err)
				}
			})
		})
	}
}

// TestSetup_NoExporterStillMintsValidTraceIDs is the reason ExporterNone
// is a real provider rather than a no-op one. With no collector deployed,
// trace IDs must still be valid: they are what a log line's trace_id
// carries, what an X-Trace-ID response header returns, and what lets a
// trace cross into the Runner. A no-op tracer would make all of that
// all-zeros and useless.
func TestSetup_NoExporterStillMintsValidTraceIDs(t *testing.T) {
	provider, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName: "pleiades-test",
		Exporter:    telemetry.ExporterNone,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	_, span := provider.Tracer("test").Start(context.Background(), "op")
	defer span.End()

	sc := span.SpanContext()
	if !sc.IsValid() {
		t.Fatalf("span context is not valid with ExporterNone: trace=%v span=%v", sc.TraceID(), sc.SpanID())
	}
	if !sc.IsSampled() {
		t.Error("span is not sampled: the default sampler should keep every trace")
	}
}

// TestSetup_StdoutExporterActuallyExports proves the stdout exporter path
// is wired end to end rather than merely constructed: a span started
// through the provider must appear in the writer after Shutdown flushes
// the batch.
func TestSetup_StdoutExporterActuallyExports(t *testing.T) {
	var out strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStdoutWriterForTest(
		telemetry.Config{ServiceName: "pleiades-test", Exporter: telemetry.ExporterStdout}, &out))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_, span := provider.Tracer("test").Start(context.Background(), "exported-operation")
	span.End()

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "exported-operation") {
		t.Errorf("the stdout exporter emitted no span for the operation; got %q", got)
	}
	if !strings.Contains(got, "pleiades-test") {
		t.Errorf("the exported span does not carry the service name; got %q", got)
	}
}

// TestSetup_OTLPExporterAndServiceVersion covers the production export
// path and the optional service.version attribute together.
//
// No collector is running, and that is the point: otlptracehttp connects
// lazily, so Setup must succeed against an endpoint that is not there
// rather than making a process fail to start because its telemetry
// backend is briefly down. The spans it cannot deliver are dropped, which
// is the right failure mode for observability.
func TestSetup_OTLPExporterAndServiceVersion(t *testing.T) {
	var out strings.Builder
	provider, err := telemetry.Setup(context.Background(), telemetry.ConfigWithStdoutWriterForTest(
		telemetry.Config{
			ServiceName:    "pleiades-test",
			ServiceVersion: "1.2.3",
			Exporter:       telemetry.ExporterStdout,
		}, &out))
	if err != nil {
		t.Fatalf("Setup with a service version: %v", err)
	}
	_, span := provider.Tracer("test").Start(context.Background(), "versioned")
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Errorf("the exported span does not carry service.version; got %q", out.String())
	}

	otlp, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName: "pleiades-test",
		Exporter:    telemetry.ExporterOTLP,
		Endpoint:    "127.0.0.1:4318",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("Setup with an OTLP exporter: %v", err)
	}
	if err := otlp.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown of the OTLP provider: %v", err)
	}
}

// TestPropagator_RoundTripsTraceContext proves the one wire encoding this
// platform uses really carries a trace across a boundary. Both the API
// edge and the event bus depend on this exact behavior, so it is asserted
// once, here, on the shared primitive.
func TestPropagator_RoundTripsTraceContext(t *testing.T) {
	provider, err := telemetry.Setup(context.Background(), telemetry.Config{ServiceName: "pleiades-test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	ctx, span := provider.Tracer("test").Start(context.Background(), "outgoing")
	defer span.End()

	carrier := propagation.MapCarrier{}
	provider.Propagator().Inject(ctx, carrier)

	if carrier["traceparent"] == "" {
		t.Fatalf("no traceparent was injected; carrier is %v", carrier)
	}

	extracted := provider.Propagator().Extract(context.Background(), carrier)
	got := trace.SpanContextFromContext(extracted)
	if got.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("extracted trace ID is %v, want %v", got.TraceID(), span.SpanContext().TraceID())
	}
	if got.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("extracted parent span ID is %v, want %v", got.SpanID(), span.SpanContext().SpanID())
	}
	if !got.IsRemote() {
		t.Error("extracted span context is not marked remote, so a child span would be attributed to this process")
	}
}

// TestConfigFromEnv reads the standard OpenTelemetry environment
// variables. The implied-otlp case matters most: an operator who sets an
// endpoint and nothing else has clearly asked for export, and treating
// that as "none" is a silent misconfiguration nobody notices until a trace
// is missing.
func TestConfigFromEnv(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantExporter telemetry.ExporterKind
		wantEndpoint string
		wantInsecure bool
		wantRatio    float64
		wantName     string
		wantErr      string
	}{
		{
			name:         "unset defaults to none",
			env:          map[string]string{},
			wantExporter: telemetry.ExporterNone,
			wantName:     "pleiades-controller",
		},
		{
			name:         "endpoint alone implies otlp",
			env:          map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318"},
			wantExporter: telemetry.ExporterOTLP,
			wantEndpoint: "collector:4318",
			wantName:     "pleiades-controller",
		},
		{
			name:         "http scheme is stripped and implies insecure",
			env:          map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/"},
			wantExporter: telemetry.ExporterOTLP,
			wantEndpoint: "collector:4318",
			wantInsecure: true,
			wantName:     "pleiades-controller",
		},
		{
			name:         "https scheme is stripped and stays secure",
			env:          map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector:4318"},
			wantExporter: telemetry.ExporterOTLP,
			wantEndpoint: "collector:4318",
			wantName:     "pleiades-controller",
		},
		{
			name: "explicit none wins over a set endpoint",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":        "none",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318",
			},
			wantExporter: telemetry.ExporterNone,
			wantEndpoint: "collector:4318",
			wantName:     "pleiades-controller",
		},
		{
			name:         "service name override",
			env:          map[string]string{"OTEL_SERVICE_NAME": "custom-name"},
			wantExporter: telemetry.ExporterNone,
			wantName:     "custom-name",
		},
		{
			name:         "sampler argument",
			env:          map[string]string{"OTEL_TRACES_SAMPLER_ARG": "0.1"},
			wantExporter: telemetry.ExporterNone,
			wantRatio:    0.1,
			wantName:     "pleiades-controller",
		},
		{
			name:    "unknown exporter is rejected",
			env:     map[string]string{"OTEL_TRACES_EXPORTER": "zipkin"},
			wantErr: "OTEL_TRACES_EXPORTER",
		},
		{
			name:    "non-numeric sampler argument is rejected",
			env:     map[string]string{"OTEL_TRACES_SAMPLER_ARG": "aggressive"},
			wantErr: "OTEL_TRACES_SAMPLER_ARG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := telemetry.ConfigFromEnv("pleiades-controller", "1.0.0")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ConfigFromEnv error is %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConfigFromEnv: %v", err)
			}
			if cfg.ServiceName != tt.wantName {
				t.Errorf("ServiceName is %q, want %q", cfg.ServiceName, tt.wantName)
			}
			if cfg.Exporter != tt.wantExporter {
				t.Errorf("Exporter is %q, want %q", cfg.Exporter, tt.wantExporter)
			}
			if cfg.Endpoint != tt.wantEndpoint {
				t.Errorf("Endpoint is %q, want %q", cfg.Endpoint, tt.wantEndpoint)
			}
			if cfg.Insecure != tt.wantInsecure {
				t.Errorf("Insecure is %v, want %v", cfg.Insecure, tt.wantInsecure)
			}
			if cfg.SampleRatio != tt.wantRatio {
				t.Errorf("SampleRatio is %v, want %v", cfg.SampleRatio, tt.wantRatio)
			}
			if cfg.ServiceVersion != "1.0.0" {
				t.Errorf("ServiceVersion is %q, want %q", cfg.ServiceVersion, "1.0.0")
			}
		})
	}
}
