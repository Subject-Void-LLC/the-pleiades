// Package archtest: the structural scans behind telemetry_test.go, each one
// a pure function of a parsed file so its control cases can feed it source.
package archtest

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// The OpenTelemetry import paths the scans resolve names for.
const (
	// otelRootPath is the package holding the global setters.
	otelRootPath = "go.opentelemetry.io/otel"
	// otelAttributePath mints span attributes.
	otelAttributePath = "go.opentelemetry.io/otel/attribute"
	// otelCodesPath holds span status codes.
	otelCodesPath = "go.opentelemetry.io/otel/codes"
	// otelTracePath is the span API.
	otelTracePath = "go.opentelemetry.io/otel/trace"
	// netHTTPPath holds StatusText, the one non-literal fixed phrase.
	netHTTPPath = "net/http"
)

// attributeKeyConstructors are the attribute package's functions whose first
// argument is the attribute key, as of the SDK version go.mod pins, plus the
// Key conversion that starts attribute.Key("k").String(v). A constructor a
// later release adds and this list lacks is still held at run time, where
// the exporter drops a key that is not on the allowlist.
var attributeKeyConstructors = map[string]bool{
	"String": true, "Stringer": true, "Bool": true,
	"Int": true, "Int64": true, "Float64": true,
	"StringSlice": true, "BoolSlice": true,
	"IntSlice": true, "Int64Slice": true, "Float64Slice": true,
	"ByteSlice": true, "Slice": true, "Map": true,
	"Key": true,
}

// spanPipelineImportPrefixes are the imports only internal/telemetry may
// use. The SDK and exporter packages can build a second pipeline that never
// passes the masking exporter; the semconv packages mint attribute keys
// through helpers the literal scan cannot see; the bridges, the auto SDK and
// the contrib instrumentation packages build or reach providers and
// propagators of their own.
var spanPipelineImportPrefixes = []string{
	"go.opentelemetry.io/otel/sdk",
	"go.opentelemetry.io/otel/exporters/",
	"go.opentelemetry.io/otel/semconv",
	"go.opentelemetry.io/otel/bridge",
	"go.opentelemetry.io/auto/",
	"go.opentelemetry.io/contrib/",
}

// otelGlobalSetters are the go.opentelemetry.io/otel functions that install
// a process-wide provider, propagator, error handler or logger. Setup is
// the one place each is called.
var otelGlobalSetters = map[string]bool{
	"SetTracerProvider": true, "SetTextMapPropagator": true,
	"SetErrorHandler": true, "SetLogger": true, "SetMeterProvider": true,
}

// spanNameBuilders are the internal/api functions a span name may be built
// from besides a literal: both return a bounded identifier.
var spanNameBuilders = map[string]bool{"boundedMethod": true, "routePattern": true}

// telemetryFinding is one place a production file breaks the boundary.
type telemetryFinding struct {
	// pos is where it happens.
	pos token.Pos
	// what says what was found, for the failure message.
	what string
}

// importedName returns the name file refers to path by: its alias if it has
// one ("." for a dot import), otherwise the last path element, or "" when
// file does not import it. Resolving the alias is what keeps a renamed
// import from dodging the scan.
func importedName(file *ast.File, path string) string {
	for _, imp := range file.Imports {
		if imp.Path.Value != strconv.Quote(path) {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

// importsSpanAPI reports whether file can reach a span: it imports the span
// API, the status codes or the OpenTelemetry root package. The SetStatus,
// AddEvent, Start and SetName scans are confined to such files, because
// other types in this module have methods of those names.
func importsSpanAPI(file *ast.File) bool {
	return importedName(file, otelTracePath) != "" || importedName(file, otelCodesPath) != "" || importedName(file, otelRootPath) != ""
}

// isPackageSelector reports whether expr is pkg.name.
func isPackageSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg
}

// stringLiteralValue returns the value of expr when it is a string literal.
func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

// calledSelectors returns the selector expressions in file that are the
// function of a call, so a scan can tell a call from a method value.
func calledSelectors(file *ast.File) map[*ast.SelectorExpr]bool {
	called := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				called[sel] = true
			}
		}
		return true
	})
	return called
}

// pipelineFindings returns, for a file outside internal/telemetry, each
// import that can build a pipeline of its own and each use of a global
// setter, called or taken as a value; and, in any file, each dot import of
// an OpenTelemetry package, which would hide every name the other scans
// resolve.
func pipelineFindings(file *ast.File, inTelemetry bool) []telemetryFinding {
	var out []telemetryFinding
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if imp.Name != nil && imp.Name.Name == "." && strings.HasPrefix(path, "go.opentelemetry.io/") {
			out = append(out, telemetryFinding{imp.Pos(), "dot-imports " + path})
		}
		if inTelemetry {
			continue
		}
		for _, prefix := range spanPipelineImportPrefixes {
			if strings.HasPrefix(path, prefix) {
				out = append(out, telemetryFinding{imp.Pos(), "imports " + path})
			}
		}
	}
	otel := importedName(file, otelRootPath)
	if inTelemetry || otel == "" || otel == "." {
		return out
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && otelGlobalSetters[sel.Sel.Name] && isPackageSelector(sel, otel, sel.Sel.Name) {
			out = append(out, telemetryFinding{sel.Pos(), "uses otel." + sel.Sel.Name})
		}
		return true
	})
	return out
}

// keyFindings returns each attribute key built in file that is not a string
// literal on allowed, each telemetry-only key written outside
// internal/telemetry, and each form that builds or carries a key the scan
// cannot read: an attribute.KeyValue literal, an assignment to a Key field,
// a type declared as or aliasing attribute.Key or KeyValue, a constant or
// variable of type attribute.Key whose value is not an allowed literal, and
// a parameter, result or field of type attribute.Key.
func keyFindings(file *ast.File, inTelemetry bool, allowed, telemetryOnly map[string]bool) []telemetryFinding {
	pkg := importedName(file, otelAttributePath)
	if pkg == "" || pkg == "." {
		return nil
	}
	var out []telemetryFinding
	checkKey := func(pos token.Pos, expr ast.Expr) {
		key, ok := stringLiteralValue(expr)
		switch {
		case !ok:
			out = append(out, telemetryFinding{pos, "attribute key is not a string literal"})
		case !allowed[key]:
			out = append(out, telemetryFinding{pos, "attribute key " + strconv.Quote(key) + " is not on the allowlist"})
		case telemetryOnly[key] && !inTelemetry:
			out = append(out, telemetryFinding{pos, "attribute key " + strconv.Quote(key) + " is written only by internal/telemetry"})
		}
	}
	isKeyType := func(expr ast.Expr) bool {
		return isPackageSelector(expr, pkg, "Key") || isPackageSelector(expr, pkg, "KeyValue")
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if isPackageSelector(node.Type, pkg, "KeyValue") {
				out = append(out, telemetryFinding{node.Pos(), "builds an attribute.KeyValue literal; use a constructor with a literal key"})
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if ok && isPackageSelector(sel, pkg, sel.Sel.Name) && attributeKeyConstructors[sel.Sel.Name] && len(node.Args) > 0 {
				checkKey(node.Pos(), node.Args[0])
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Key" && !inTelemetry {
					out = append(out, telemetryFinding{sel.Pos(), "assigns a Key field; build the attribute with a literal key"})
				}
			}
		case *ast.TypeSpec:
			if isKeyType(node.Type) {
				out = append(out, telemetryFinding{node.Pos(), "declares a type standing for attribute." + node.Type.(*ast.SelectorExpr).Sel.Name})
			}
		case *ast.ValueSpec:
			if node.Type != nil && isPackageSelector(node.Type, pkg, "Key") {
				if len(node.Values) == 0 {
					out = append(out, telemetryFinding{node.Pos(), "declares an attribute.Key with no literal value"})
				}
				for _, v := range node.Values {
					checkKey(v.Pos(), v)
				}
			}
		case *ast.Field:
			if isPackageSelector(node.Type, pkg, "Key") && !inTelemetry {
				out = append(out, telemetryFinding{node.Pos(), "a parameter, result or field of type attribute.Key carries a key the scan cannot read"})
			}
		}
		return true
	})
	return out
}

// errorTextFindings returns each use of RecordError, called or taken as a
// value, in any file; and, in a file that can reach a span, each SetStatus
// taken as a value and each SetStatus call (two arguments, or three through
// a method expression) whose description is not a string literal or
// net/http's StatusText, whatever its code argument is. A wrapper that takes
// the description as a parameter is caught at its own SetStatus.
func errorTextFindings(file *ast.File) []telemetryFinding {
	called := calledSelectors(file)
	spanAPI := importsSpanAPI(file)
	httpName := importedName(file, netHTTPPath)
	var out []telemetryFinding
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			switch {
			case node.Sel.Name == "RecordError":
				out = append(out, telemetryFinding{node.Pos(), "uses RecordError; use telemetry.RecordFailure"})
			case node.Sel.Name == "SetStatus" && spanAPI && !called[node]:
				out = append(out, telemetryFinding{node.Pos(), "takes SetStatus as a value, so its description cannot be checked"})
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SetStatus" || !spanAPI || len(node.Args) < 2 || len(node.Args) > 3 {
				return true
			}
			if !isFixedPhrase(node.Args[len(node.Args)-1], httpName) {
				out = append(out, telemetryFinding{node.Pos(), "status description is not a fixed phrase; use telemetry.RecordFailure"})
			}
		}
		return true
	})
	return out
}

// isFixedPhrase reports whether expr is a string literal or a call to
// net/http's StatusText, resolved by the file's own import name, the two
// forms whose text cannot quote a value.
func isFixedPhrase(expr ast.Expr, httpName string) bool {
	if _, ok := stringLiteralValue(expr); ok {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	return ok && httpName != "" && httpName != "." && isPackageSelector(call.Fun, httpName, "StatusText")
}

// nameAndEventFindings returns, in a file that can reach a span, each Start
// (a context and a name at least) and SetName call whose name is not fixed,
// each AddEvent whose name is not a declared event, and each of the three
// taken as a method value.
func nameAndEventFindings(file *ast.File, events map[string]bool) []telemetryFinding {
	if !importsSpanAPI(file) {
		return nil
	}
	called := calledSelectors(file)
	var out []telemetryFinding
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if (node.Sel.Name == "SetName" || node.Sel.Name == "AddEvent") && !called[node] {
				out = append(out, telemetryFinding{node.Pos(), "takes " + node.Sel.Name + " as a value, so its name cannot be checked"})
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch {
			case sel.Sel.Name == "Start" && len(node.Args) >= 2:
				if !isFixedSpanName(node.Args[1]) {
					out = append(out, telemetryFinding{node.Pos(), "span name is not a literal or a boundedMethod/routePattern build"})
				}
			case sel.Sel.Name == "SetName" && len(node.Args) == 1:
				if !isFixedSpanName(node.Args[0]) {
					out = append(out, telemetryFinding{node.Pos(), "span name is not a literal or a boundedMethod/routePattern build"})
				}
			case sel.Sel.Name == "AddEvent" && len(node.Args) >= 1:
				if name, ok := stringLiteralValue(node.Args[0]); !ok || !events[name] {
					out = append(out, telemetryFinding{node.Pos(), "event name is not a declared event"})
				}
			}
		}
		return true
	})
	return out
}

// isFixedSpanName reports whether expr is a string literal, a call to one of
// spanNameBuilders, or a concatenation of those.
func isFixedSpanName(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		_, ok := stringLiteralValue(e)
		return ok
	case *ast.ParenExpr:
		return isFixedSpanName(e.X)
	case *ast.BinaryExpr:
		return e.Op == token.ADD && isFixedSpanName(e.X) && isFixedSpanName(e.Y)
	case *ast.CallExpr:
		id, ok := e.Fun.(*ast.Ident)
		return ok && spanNameBuilders[id.Name]
	default:
		return false
	}
}
