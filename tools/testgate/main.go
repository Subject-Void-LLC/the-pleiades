// Command testgate runs the same `go test -race ./...` (or, with
// -integration, the same `go test -tags integration -race -count=1
// ./...`) the Makefile's own test-race/test-integration targets run, and
// fails only on a test failure outside the packages listed, each with a
// written reason, in flaky-packages.json at the repo root. See
// tools/internal/flakegate's own doc comment for the classification rule
// this tool and tools/coverage-check's -tolerant mode share, and
// flaky-packages.json's own header comment for the full policy.
//
// This tool is deliberately not part of `make ci`, and GOSEC_VERSION-style
// pinning does not apply to it: .github/workflows/ci.yml runs `make ci`
// directly, which still uses the bare test-race/test-integration targets
// and hard-fails on anything at all. Only the Makefile's push-gate target
// (used by .githooks/pre-push) calls this tool, so this file changes how
// much local noise a developer fights through before pushing, never what
// actually gates a merge.
//
// Usage: go run ./tools/testgate [-integration]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// goTestTimeout replaces go test's 10-minute per-package default, matching
// the Makefile's own GO_TEST_TIMEOUT. It exists twice for the identical
// reason tools/coverage-check's own goTestTimeout constant does (that
// file's own doc comment has the full reasoning): a Makefile cannot read a
// Go constant, and reading this one back out of the environment would put
// caller-controlled text into an exec.Command argument for no benefit.
// TestGoTestTimeoutMatchesMakefile keeps the two copies honest.
const goTestTimeout = "20m"

// flakyPackagesPath is the repo-relative path to the waiver file, a
// constant rather than a flag: this tool has exactly one caller
// (.githooks/pre-push, via the Makefile's push-gate target), invoked from
// the repo root, the same assumption gosec-check's loadWaivers makes about
// gosec-waivers.json.
const flakyPackagesPath = "flaky-packages.json"

func main() {
	integration := flag.Bool("integration", false, "run with -tags integration -count=1, matching the Makefile's test-integration target")
	flag.Parse()

	if err := run(*integration); err != nil {
		fmt.Fprintln(os.Stderr, "testgate:", err)
		os.Exit(1)
	}
}

func run(integration bool) error {
	tolerated, err := flakegate.LoadTolerated(flakyPackagesPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", flakyPackagesPath, err)
	}

	args := []string{"-race", "-timeout", goTestTimeout}
	if integration {
		args = append(args, "-tags", "integration", "-count=1")
	}

	// Every raw JSON line is echoed to this process's own stdout as it is
	// read, so a slow package's progress is visible live rather than
	// silent until the whole run finishes.
	events, waitErr := flakegate.RunGoTestJSON(args, os.Stdout)
	hard, warned := flakegate.Classify(events, tolerated)

	if len(warned) > 0 {
		fmt.Printf("testgate: %d test failure(s) confined to flaky-packages.json packages, treated as warnings:\n\n", len(warned))
		for _, f := range warned {
			fmt.Printf("  %s: %s (%s)\n", f.Package, f.Test, tolerated[f.Package])
		}
		fmt.Println()
	}

	if len(hard) > 0 {
		fmt.Fprintf(os.Stderr, "testgate: %d failure(s) NOT in flaky-packages.json, or a build failure (never tolerated):\n\n", len(hard))
		for _, f := range hard {
			if f.Test == "" {
				fmt.Fprintf(os.Stderr, "  %s: build failed\n", f.Package)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Test)
		}
		fmt.Fprintln(os.Stderr, "\nFix it, or if this package genuinely provisions real ephemeral infrastructure and this is a resource-contention flake (confirm by rerunning the exact failing test in isolation), add a flaky-packages.json entry with a written reason.")
		return fmt.Errorf("%d failure(s) outside flaky-packages.json", len(hard))
	}

	// waitErr (go test's own exit status) is otherwise ignored: a non-zero
	// exit with zero hard failures means every failure go test reported was
	// tolerated above, which is exactly the state this tool exists to
	// allow through. A non-zero exit with NO parsed events at all (go test
	// itself could not even start, an unrelated wiring problem) is the one
	// case worth surfacing regardless.
	if waitErr != nil && len(events) == 0 {
		return fmt.Errorf("go test produced no output at all: %w", waitErr)
	}

	if len(warned) == 0 {
		fmt.Println("testgate: all tests passed")
	} else {
		fmt.Println("testgate: passed (warnings above)")
	}
	return nil
}
