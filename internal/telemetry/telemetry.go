// Package telemetry owns this platform's OpenTelemetry composition: the
// tracer provider, the span exporter, the resource that names the service,
// and the W3C context propagator every boundary crossing shares.
//
// PLAN.md Section 19 ("Universal Observability") requires OTEL in the
// foundation of every component and requires a trace to survive the whole
// lifecycle: API Request -> Event Bus -> Lock Manager -> Runner Execution
// -> Device Result. That is only possible if exactly one place decides how
// spans are made and how trace context is encoded on the wire, so this
// package is that place. Callers receive a trace.Tracer and a
// propagation.TextMapPropagator as ordinary injected values rather than
// reaching for a package-level global.
//
// Setup does also install its provider and propagator on OpenTelemetry's
// own globals. That is not a second source of truth: third-party
// instrumentation libraries have no way to be handed a provider, so the
// globals exist for them. Pleiades code always takes the injected value.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ExporterKind selects where finished spans are sent. It is a named type
// with a fixed set of values rather than a bare string so an unknown
// spelling fails at configuration time instead of silently disabling
// tracing.
type ExporterKind string

const (
	// ExporterNone records spans but sends them nowhere. Trace and span
	// IDs are still real and still propagate across process boundaries,
	// which is what keeps a log line's trace_id meaningful even when no
	// collector is deployed. This is the default.
	ExporterNone ExporterKind = "none"

	// ExporterStdout writes finished spans to standard output as JSON.
	// PATTERNS.md's Sidecar entry ("NO") says this platform expects the
	// hosting environment to scrape stdout rather than run a co-located
	// shipping agent, so stdout is a real deployment target here, not only
	// a debugging aid.
	ExporterStdout ExporterKind = "stdout"

	// ExporterOTLP sends spans to an OpenTelemetry collector over OTLP
	// HTTP. This is the production target.
	ExporterOTLP ExporterKind = "otlp"
)

// Config describes how a process builds its tracing pipeline.
type Config struct {
	// ServiceName names the process in every emitted span
	// (service.name). Required.
	ServiceName string

	// ServiceVersion is the build version reported alongside the service
	// name. Optional.
	ServiceVersion string

	// Exporter selects the span destination. An empty value means
	// ExporterNone.
	Exporter ExporterKind

	// Endpoint is the OTLP collector endpoint, used only when Exporter is
	// ExporterOTLP. An empty value lets the OTLP exporter fall back to its
	// own standard OTEL_EXPORTER_OTLP_ENDPOINT handling.
	Endpoint string

	// Insecure sends OTLP over plain HTTP instead of HTTPS. It exists for
	// a collector running on the same host or inside the same cluster
	// network, and defaults to false so a misconfiguration cannot silently
	// downgrade an external export.
	Insecure bool

	// SampleRatio is the head-sampling probability for a trace that
	// arrives with no upstream sampling decision, between 0 and 1. Zero
	// means "not set" and is treated as 1 (sample everything), because a
	// platform whose whole point is auditability should not drop traces
	// unless an operator explicitly asks it to.
	SampleRatio float64

	// stdout is where ExporterStdout writes. It exists so a test can read
	// the exported spans back; production leaves it nil and gets
	// os.Stdout.
	stdout io.Writer
}

// Provider is a running tracing pipeline. It is what Setup returns and
// what a composition root passes down to the packages that need to make
// spans.
type Provider struct {
	tp         *sdktrace.TracerProvider
	propagator propagation.TextMapPropagator
}

// Tracer returns a named tracer for one instrumented component. The name
// identifies the instrumentation, not the service, so an API middleware
// and an event consumer inside the same binary use different names.
func (p *Provider) Tracer(name string) trace.Tracer {
	return p.tp.Tracer(name)
}

// Propagator returns the text map propagator every boundary crossing must
// use to serialize and deserialize trace context.
func (p *Provider) Propagator() propagation.TextMapPropagator {
	return p.propagator
}

// Shutdown flushes any spans still buffered and releases the exporter.
// A composition root calls this once, on its own shutdown path, before the
// process exits: without it, the spans describing the shutdown itself, and
// anything else still in the batch queue, are lost.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}

// Propagator is the one wire encoding this platform uses for trace context
// across every boundary: HTTP headers at the API edge and NATS message
// headers at the bus. It is W3C Trace Context plus W3C Baggage, the same
// pair OpenTelemetry itself defaults to.
//
// It is a free function rather than a Provider method because
// internal/event must inject trace context on publish whether or not the
// process that built the Bus also built a Provider. Encoding is a format
// decision with exactly one right answer; span creation is not.
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// Setup builds the tracing pipeline described by cfg and installs it on
// OpenTelemetry's globals for third-party instrumentation. The returned
// Provider is what Pleiades code should use directly.
func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("telemetry: ServiceName is required")
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return nil, fmt.Errorf("telemetry: SampleRatio must be between 0 and 1, got %v", cfg.SampleRatio)
	}

	res, err := buildResource(cfg)
	if err != nil {
		return nil, err
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler(cfg.SampleRatio)),
	}

	// ExporterNone deliberately adds no span processor at all. The
	// provider still mints real, valid trace and span IDs, so
	// X-Trace-ID response headers, log correlation, and cross-process
	// propagation all keep working with no collector deployed; only the
	// export step is absent.
	if cfg.Exporter != "" && cfg.Exporter != ExporterNone {
		exp, err := buildExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	propagator := Propagator()

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)

	return &Provider{tp: tp, propagator: propagator}, nil
}

// buildResource describes the process every span is attributed to. The
// attribute keys are the OpenTelemetry semantic-convention spellings
// ("service.name", "service.version"), written out literally rather than
// taken from a semconv package so a semconv version bump cannot silently
// rename what a dashboard groups by.
func buildResource(cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", cfg.ServiceName),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", cfg.ServiceVersion))
	}
	return resource.Merge(resource.Default(), resource.NewWithAttributes(resource.Default().SchemaURL(), attrs...))
}

// sampler turns a ratio into a sampler that always respects an upstream
// decision. ParentBased is what makes a distributed trace coherent: once
// the API edge decides to sample a request, every downstream span in that
// trace is kept even if the downstream process was configured with a lower
// ratio.
func sampler(ratio float64) sdktrace.Sampler {
	if ratio <= 0 || ratio >= 1 {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

// buildExporter constructs the span exporter cfg selects.
func buildExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case ExporterStdout:
		out := cfg.stdout
		if out == nil {
			out = os.Stdout
		}
		return stdouttrace.New(stdouttrace.WithWriter(out))
	case ExporterOTLP:
		opts := []otlptracehttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("telemetry: unknown exporter %q (want %q, %q, or %q)",
			cfg.Exporter, ExporterNone, ExporterStdout, ExporterOTLP)
	}
}

// ConfigFromEnv reads the standard OpenTelemetry environment variables a
// deployment already knows how to set, so an operator configures Pleiades
// tracing the same way they configure any other OTEL process:
//
//   - OTEL_SERVICE_NAME overrides serviceName.
//   - OTEL_TRACES_EXPORTER selects "none", "stdout", or "otlp". When it is
//     unset but OTEL_EXPORTER_OTLP_ENDPOINT is set, "otlp" is implied,
//     because an endpoint configured with nothing to send to it is far
//     more likely a mistake than an intention.
//   - OTEL_EXPORTER_OTLP_ENDPOINT is the collector endpoint. A URL with an
//     "http://" scheme also implies Insecure, matching the OTLP
//     specification's own reading of the scheme.
//   - OTEL_TRACES_SAMPLER_ARG is the sampling ratio.
func ConfigFromEnv(serviceName, serviceVersion string) (Config, error) {
	cfg := Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Exporter:       ExporterNone,
	}
	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		cfg.ServiceName = name
	}

	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	switch kind := strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")); kind {
	case "":
		if endpoint != "" {
			cfg.Exporter = ExporterOTLP
		}
	case string(ExporterNone), string(ExporterStdout), string(ExporterOTLP):
		cfg.Exporter = ExporterKind(kind)
	default:
		return Config{}, fmt.Errorf("telemetry: OTEL_TRACES_EXPORTER=%q is not one of %q, %q, %q",
			kind, ExporterNone, ExporterStdout, ExporterOTLP)
	}

	if endpoint != "" {
		host, insecure := splitEndpoint(endpoint)
		cfg.Endpoint = host
		cfg.Insecure = insecure
	}

	if arg := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); arg != "" {
		ratio, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			return Config{}, fmt.Errorf("telemetry: OTEL_TRACES_SAMPLER_ARG=%q is not a number: %w", arg, err)
		}
		cfg.SampleRatio = ratio
	}

	return cfg, nil
}

// splitEndpoint strips a scheme off an OTLP endpoint and reports whether
// that scheme was plaintext. otlptracehttp.WithEndpoint wants a bare
// host:port, but operators routinely set the full URL the OTLP
// specification describes, so accepting both is the difference between
// working and silently exporting nowhere.
func splitEndpoint(endpoint string) (host string, insecure bool) {
	switch {
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimSuffix(strings.TrimPrefix(endpoint, "http://"), "/"), true
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimSuffix(strings.TrimPrefix(endpoint, "https://"), "/"), false
	default:
		return endpoint, false
	}
}

// ShutdownTimeout is the bound a composition root should give Shutdown.
// Flushing a batch to a collector that has gone away must not hold a
// process open indefinitely on its way out.
const ShutdownTimeout = 5 * time.Second
