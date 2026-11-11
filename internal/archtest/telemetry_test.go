// Package archtest: the telemetry boundary held at build time.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
)

// The build-time half of the telemetry boundary. internal/telemetry's
// masking exporter drops at run time whatever the policy does not allow, but
// a dropped attribute is discovered as an empty dashboard, weeks later. This
// file reaches the author instead.
//
// It is source inspection for the reason logging_test.go is: the call site
// that forgot is the one no test exercises. It is structural, without type
// information, so it reads the forms a call site is written in, and the
// scans in telemetry_scan_test.go say which forms they hold. What a
// structural scan cannot see (a key computed by a generic helper or by
// reflection, a span or scope name chosen inside a dependency, a value
// under an allowed key) the exporter still holds at run time: it drops an
// unlisted key, replaces an undeclared span or scope name, withholds a free
// status description, keeps only declared error.type categories, masks and
// caps every value it keeps.

// telemetryPackageDir is the one directory allowed to build a span pipeline,
// set the OpenTelemetry globals, write a span's failure text and the
// telemetry-only keys.
const telemetryPackageDir = "internal/telemetry"

// forEachProductionFile hands fn every non-test Go file under the module's
// scanned roots, with its slash-separated path relative to the module root.
// Test files are left out: a test builds its own in-memory pipeline and its
// own forbidden keys, on purpose, to prove the exporter drops them.
func forEachProductionFile(t *testing.T, fn func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	root := repoRoot(t)
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "/testdata/") {
				return
			}
			fn(rel, fset, file)
		})
	}
}

// inTelemetryPackage reports whether rel is a file of internal/telemetry
// itself, not of a package nested under it.
func inTelemetryPackage(rel string) bool {
	return filepath.ToSlash(filepath.Dir(rel)) == telemetryPackageDir
}

// scanModule runs scan over every production file, outside internal/telemetry
// unless includeTelemetry, and fails with explanation on any finding.
func scanModule(t *testing.T, includeTelemetry bool, explanation string, scan func(file *ast.File, inTelemetry bool) []telemetryFinding) {
	t.Helper()
	var findings []string
	forEachProductionFile(t, func(rel string, fset *token.FileSet, file *ast.File) {
		in := inTelemetryPackage(rel)
		if in && !includeTelemetry {
			return
		}
		for _, f := range scan(file, in) {
			findings = append(findings, rel+":"+strconv.Itoa(fset.Position(f.pos).Line)+": "+f.what)
		}
	})
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%s\n%s", explanation, strings.Join(findings, "\n"))
	}
}

// TestOnlyTelemetryBuildsSpanPipelines asserts that nothing outside
// internal/telemetry can construct a tracer provider, a span processor or an
// exporter, install OpenTelemetry's globals, or bring in an instrumentation
// package that does either, and that no file dot-imports an OpenTelemetry
// package. The masking exporter cannot miss anything that reaches the
// exporter Setup built; this is what keeps there from being another one.
func TestOnlyTelemetryBuildsSpanPipelines(t *testing.T) {
	scanModule(t, true, "a span pipeline or an OpenTelemetry global is set up outside "+telemetryPackageDir+". "+
		"Take a trace.Tracer from telemetry.Provider instead: a provider, exporter, propagator or logger installed "+
		"anywhere else sends spans or text that never pass the attribute policy.", pipelineFindings)
}

// TestEverySpanAttributeKeyIsOnTheAllowlist asserts that every attribute
// built in this module names its key as a string literal on telemetry's
// allowlist, that error.type and exception.type are written only inside
// internal/telemetry, and that no key is built where the scan cannot read
// it (a KeyValue literal, an assignment to a Key field, a type standing for
// attribute.Key, a parameter or field of that type).
func TestEverySpanAttributeKeyIsOnTheAllowlist(t *testing.T) {
	allowed := make(map[string]bool)
	for _, k := range telemetry.AllowedAttributeKeys() {
		allowed[k] = true
	}
	only := make(map[string]bool)
	for _, k := range telemetry.TelemetryOnlyAttributeKeys() {
		only[k] = true
	}
	scanModule(t, true, "a span attribute is not on the telemetry allowlist, or is written where it may not be. "+
		"Telemetry carries identifiers, never values. If the key names an identifier, add it to "+
		"allowedAttributeKeys in internal/telemetry/policy.go, where the addition is reviewed; if it names a "+
		"value, it does not belong on a span.", func(file *ast.File, in bool) []telemetryFinding {
		return keyFindings(file, in, allowed, only)
	})
}

// TestSpansCarryNoFreeErrorText asserts that no call site outside
// internal/telemetry puts error text on a span: no RecordError in any form,
// and no SetStatus whose description is not a fixed phrase, whatever its
// code argument is.
func TestSpansCarryNoFreeErrorText(t *testing.T) {
	scanModule(t, false, "a span is given error text. An error's text may quote device output, a parameter or a "+
		"credential, and a trace backend keeps it. Log the error through the masked logger with the trace_id, and "+
		"mark the span with telemetry.RecordFailure and a declared Outcome.", func(file *ast.File, _ bool) []telemetryFinding {
		return errorTextFindings(file)
	})
}

// TestSpanNamesAndEventsAreFixed asserts that every span name given in this
// module outside internal/telemetry is a string literal or built from the
// API's boundedMethod and routePattern, and that every event is a declared
// one.
func TestSpanNamesAndEventsAreFixed(t *testing.T) {
	events := make(map[string]bool)
	for _, name := range telemetry.AllowedEventNames() {
		events[name] = true
	}
	scanModule(t, false, "a span name or event is built from text that is not fixed. A span name is exported "+
		"as is when it is declared, so it must never carry a value: name a span with a literal (and declare it in "+
		"internal/telemetry/policy.go), or, in internal/api, with boundedMethod and routePattern.",
		func(file *ast.File, _ bool) []telemetryFinding {
			return nameAndEventFindings(file, events)
		})
}
