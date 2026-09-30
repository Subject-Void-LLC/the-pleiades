package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// makefileTimeout captures the value of the Makefile's GO_TEST_TIMEOUT
// assignment, whichever assignment operator it uses.
var makefileTimeout = regexp.MustCompile(`(?m)^\s*(?:export\s+)?GO_TEST_TIMEOUT\s*[:?]?=\s*(\S+)`)

// TestGoTestTimeoutMatchesMakefile keeps this tool's own per-package
// timeout equal to the one `make test` and `make test-race` pass.
//
// The value necessarily exists twice: a Makefile cannot read a Go
// constant, and reading it back out of the environment would put
// caller-controlled text into an exec.Command argument for no benefit.
// Two copies with nothing connecting them is exactly how this repository
// arrived at four simultaneous NATS versions and at a CI job checking
// with a different gosec than any developer had, so the copies get a
// test rather than a comment asking people to remember.
func TestGoTestTimeoutMatchesMakefile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean("../../Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	m := makefileTimeout.FindSubmatch(raw)
	if m == nil {
		t.Fatal("the Makefile no longer assigns GO_TEST_TIMEOUT; if it was renamed, update this tool's goTestTimeout constant and this test together")
	}

	if got := string(m[1]); got != goTestTimeout {
		t.Errorf("Makefile sets GO_TEST_TIMEOUT=%s but coverage-check passes -timeout %s.\n"+
			"These bound the same suite in the same `make ci` run and must agree; change both.",
			got, goTestTimeout)
	}
}

// TestParseCoverageOutput_HandlesPassAndFailLines proves the scanner
// measureCoverage relies on reads a package's percentage regardless of
// whether that package's own tests passed, matching the real behavior verified
// directly against `go test -json -cover` on a deliberately failing
// package: go test still prints "coverage: X% of statements" for a
// package whose tests failed.
func TestParseCoverageOutput_HandlesPassAndFailLines(t *testing.T) {
	const text = `ok  	github.com/Subject-Void-LLC/the-pleiades/pkg/wire	0.009s	coverage: 91.7% of statements
FAIL	github.com/Subject-Void-LLC/the-pleiades/tests/e2e	12.3s	coverage: 68.2% of statements
ok  	github.com/Subject-Void-LLC/the-pleiades/internal/dispatch	0.005s	coverage: [no statements]
`
	got, err := parseCoverageOutput(text)
	if err != nil {
		t.Fatalf("parseCoverageOutput returned unexpected error: %v", err)
	}
	want := map[string]float64{
		"github.com/Subject-Void-LLC/the-pleiades/pkg/wire":  91.7,
		"github.com/Subject-Void-LLC/the-pleiades/tests/e2e": 68.2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCoverageOutput() = %+v, want %+v", got, want)
	}
}
