package main

import (
	"os"
	"path/filepath"
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
