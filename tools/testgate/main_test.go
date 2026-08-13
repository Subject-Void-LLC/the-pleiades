package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// makefileTimeout captures the value of the Makefile's GO_TEST_TIMEOUT,
// the identical regexp tools/coverage-check's own main_test.go uses for
// the identical reason: this tool's goTestTimeout constant has to bound
// the same suite the Makefile's test-race/test-integration targets do.
var makefileTimeout = regexp.MustCompile(`(?m)^\s*(?:export\s+)?GO_TEST_TIMEOUT\s*[:?]?=\s*(\S+)`)

// TestGoTestTimeoutMatchesMakefile keeps this tool's own per-package
// timeout equal to the one the Makefile's test-race/test-integration
// targets pass, mirroring tools/coverage-check's identically-named test
// for the identical reason: two copies with nothing connecting them is
// exactly how this repository ended up with a CI job checking with a
// different gosec than any developer had, so the copies get a test rather
// than a comment asking people to remember.
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
		t.Errorf("Makefile sets GO_TEST_TIMEOUT=%s but testgate passes -timeout %s.\n"+
			"These bound the same suite in the same push-gate run and must agree; change both.",
			got, goTestTimeout)
	}
}
