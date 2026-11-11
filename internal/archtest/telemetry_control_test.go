// Package archtest: control cases proving each telemetry scan sees the
// forms it claims to hold, so an empty result from it means something.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// telemetryControl is one snippet and the number of findings a scan must
// report for it.
type telemetryControl struct {
	// name says what the snippet shows.
	name string
	// src is a whole file.
	src string
	// want is the expected number of findings.
	want int
}

// runTelemetryControls parses each snippet exactly as a real file is and
// holds scan to its count.
func runTelemetryControls(t *testing.T, scan func(*ast.File) []telemetryFinding, cases []telemetryControl) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			if got := len(scan(file)); got != tc.want {
				t.Fatalf("findings = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestKeyFindings_Control covers every key form keyFindings claims.
func TestKeyFindings_Control(t *testing.T) {
	allowed := map[string]bool{"pleiades.job.id": true, "error.type": true}
	only := map[string]bool{"error.type": true}
	outside := func(f *ast.File) []telemetryFinding { return keyFindings(f, false, allowed, only) }
	runTelemetryControls(t, outside, []telemetryControl{
		{"a listed literal key", `package p
import "go.opentelemetry.io/otel/attribute"
var a = attribute.String("pleiades.job.id", "x")`, 0},
		{"an unlisted key", `package p
import "go.opentelemetry.io/otel/attribute"
var a = attribute.String("pleiades.params.password", "x")`, 1},
		{"a key built at run time", `package p
import "go.opentelemetry.io/otel/attribute"
func f(k string) attribute.KeyValue { return attribute.Int(k, 1) }`, 1},
		{"an aliased import and a Key conversion", `package p
import attr "go.opentelemetry.io/otel/attribute"
var a = attr.Key("survey.answer").String("x")`, 1},
		{"a KeyValue literal", `package p
import "go.opentelemetry.io/otel/attribute"
var a = attribute.KeyValue{Key: "pleiades.job.id"}`, 1},
		{"a telemetry-only key outside telemetry", `package p
import "go.opentelemetry.io/otel/attribute"
var a = attribute.String("error.type", "x")`, 1},
		{"a typed constant key", `package p
import "go.opentelemetry.io/otel/attribute"
const k attribute.Key = "survey.answer"
var a = k.String("x")`, 1},
		{"a typed constant with a listed key", `package p
import "go.opentelemetry.io/otel/attribute"
const k attribute.Key = "pleiades.job.id"`, 0},
		{"a type alias and a defined type", `package p
import "go.opentelemetry.io/otel/attribute"
type K = attribute.Key
type KV attribute.KeyValue`, 2},
		{"an assignment to a Key field", `package p
import "go.opentelemetry.io/otel/attribute"
func f(kv attribute.KeyValue) attribute.KeyValue { kv.Key = "survey.answer"; return kv }`, 1},
		{"a parameter typed as a key", `package p
import "go.opentelemetry.io/otel/attribute"
func f(k attribute.Key) attribute.KeyValue { return k.String("x") }`, 1},
		{"a value helper is not a key", `package p
import "go.opentelemetry.io/otel/attribute"
var v = attribute.StringValue("anything")`, 0},
		{"a file without the attribute package", `package p
type attribute struct{}
func (attribute) String(k, v string) {}
func f() { attribute{}.String("x", "y") }`, 0},
	})

	inside := func(f *ast.File) []telemetryFinding { return keyFindings(f, true, allowed, only) }
	runTelemetryControls(t, inside, []telemetryControl{
		{"internal/telemetry may write a telemetry-only key", `package p
import "go.opentelemetry.io/otel/attribute"
var a = attribute.String("error.type", "x")
func f(k attribute.Key) bool { return k == "" }`, 0},
	})
}

// TestErrorTextFindings_Control covers every SetStatus and RecordError form
// errorTextFindings claims.
func TestErrorTextFindings_Control(t *testing.T) {
	runTelemetryControls(t, errorTextFindings, []telemetryControl{
		{"RecordError", `package p
func f(span interface{ RecordError(error) }, err error) { span.RecordError(err) }`, 1},
		{"RecordError as a method value", `package p
func f(span interface{ RecordError(error) }) func(error) { return span.RecordError }`, 1},
		{"a built status description", `package p
import "go.opentelemetry.io/otel/codes"
func f(span interface{ SetStatus(codes.Code, string) }, err error) { span.SetStatus(codes.Error, err.Error()) }`, 1},
		{"a literal or StatusText description", `package p
import (
	"net/http"
	"go.opentelemetry.io/otel/codes"
)
func f(span interface{ SetStatus(codes.Code, string) }) {
	span.SetStatus(codes.Error, "fixed")
	span.SetStatus(codes.Error, http.StatusText(500))
}`, 0},
		{"StatusText from a package merely named http", `package p
import (
	http "example.com/fake"
	"go.opentelemetry.io/otel/codes"
)
func f(span interface{ SetStatus(codes.Code, string) }) { span.SetStatus(codes.Error, http.StatusText(500)) }`, 1},
		{"StatusText through an aliased net/http", `package p
import (
	nh "net/http"
	"go.opentelemetry.io/otel/codes"
)
func f(span interface{ SetStatus(codes.Code, string) }) { span.SetStatus(codes.Error, nh.StatusText(500)) }`, 0},
		{"a code held in a variable", `package p
import "go.opentelemetry.io/otel/codes"
func f(span interface{ SetStatus(codes.Code, string) }, c codes.Code, s string) { span.SetStatus(c, s) }`, 1},
		{"an untyped constant code", `package p
import "go.opentelemetry.io/otel/trace"
func f(span trace.Span, s string) { span.SetStatus(2, s) }`, 1},
		{"a method expression", `package p
import (
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)
func f(span trace.Span, s string) { trace.Span.SetStatus(span, codes.Error, s) }`, 1},
		{"SetStatus as a method value", `package p
import "go.opentelemetry.io/otel/trace"
func f(span trace.Span) any { return span.SetStatus }`, 1},
		{"an unrelated SetStatus in a file with no span API", `package p
func f(j interface{ SetStatus(string, string) }, s string) { j.SetStatus("job", s) }`, 0},
	})
}

// TestPipelineFindings_Control covers every import and global form
// pipelineFindings claims.
func TestPipelineFindings_Control(t *testing.T) {
	outside := func(f *ast.File) []telemetryFinding { return pipelineFindings(f, false) }
	runTelemetryControls(t, outside, []telemetryControl{
		{"a second pipeline and its helpers", `package p
import (
	_ "go.opentelemetry.io/otel/sdk/trace"
	_ "go.opentelemetry.io/otel/sdk/resource"
	_ "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	_ "go.opentelemetry.io/otel/semconv/v1.26.0"
	_ "go.opentelemetry.io/auto/sdk"
	_ "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	_ "go.opentelemetry.io/otel/trace"
)`, 6},
		{"the global setters, called or as values", `package p
import o "go.opentelemetry.io/otel"
func f() {
	o.SetTextMapPropagator(nil)
	o.SetTracerProvider(nil)
	o.SetErrorHandler(nil)
	_ = o.SetLogger
}`, 4},
		{"a getter is fine", `package p
import "go.opentelemetry.io/otel"
var p = otel.GetTextMapPropagator()`, 0},
		{"dot imports", `package p
import (
	. "go.opentelemetry.io/otel/attribute"
	. "go.opentelemetry.io/otel/codes"
)`, 2},
	})
	inside := func(f *ast.File) []telemetryFinding { return pipelineFindings(f, true) }
	runTelemetryControls(t, inside, []telemetryControl{
		{"internal/telemetry may build the pipeline", `package p
import (
	"go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otel/sdk/trace"
)
func f() { otel.SetTracerProvider(nil) }`, 0},
		{"but may not dot-import", `package p
import . "go.opentelemetry.io/otel/trace"`, 1},
	})
}

// TestNameAndEventFindings_Control covers every span name and event form
// nameAndEventFindings claims.
func TestNameAndEventFindings_Control(t *testing.T) {
	events := map[string]bool{"exception": true}
	scan := func(f *ast.File) []telemetryFinding { return nameAndEventFindings(f, events) }
	runTelemetryControls(t, scan, []telemetryControl{
		{"literal names and the API's builders", `package p
import "go.opentelemetry.io/otel/trace"
func f(t trace.Tracer, s trace.Span) {
	t.Start(nil, "runner.handle_message")
	t.Start(nil, boundedMethod(nil))
	s.SetName(boundedMethod(nil) + " " + routePattern(nil))
}`, 0},
		{"names built from values", `package p
import "go.opentelemetry.io/otel/trace"
func f(t trace.Tracer, s trace.Span, path string) {
	t.Start(nil, path)
	s.SetName("GET " + path)
}`, 2},
		{"events", `package p
import "go.opentelemetry.io/otel/trace"
func f(s trace.Span, name string) {
	s.AddEvent("exception")
	s.AddEvent("user said")
	s.AddEvent(name)
}`, 2},
		{"method values", `package p
import "go.opentelemetry.io/otel/trace"
func f(s trace.Span) (any, any) { return s.SetName, s.AddEvent }`, 2},
		{"a file with no span API", `package p
func f(srv interface{ Start(int, string) }, s string) { srv.Start(1, s) }`, 0},
	})
}

// TestInTelemetryPackage_Control proves the exemption covers the package
// itself and nothing nested under it or merely named like it.
func TestInTelemetryPackage_Control(t *testing.T) {
	for rel, want := range map[string]bool{
		"internal/telemetry/telemetry.go":        true,
		"internal/telemetry/sub/x.go":            false,
		"internal/telemetryextra/x.go":           false,
		"internal/runner/agent_handle.go":        false,
		"cmd/controller/internal/telemetry/x.go": false,
	} {
		if got := inTelemetryPackage(rel); got != want {
			t.Errorf("inTelemetryPackage(%q) = %v, want %v", rel, got, want)
		}
	}
}
