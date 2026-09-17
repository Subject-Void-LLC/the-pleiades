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
// pinning does not apply to it: `make ci` still uses the bare
// test-race/test-integration targets and hard-fails on anything at all.
// Only the Makefile's push-gate target (used by .githooks/pre-push) calls
// this tool, so this file changes how much local noise a developer fights
// through before pushing, never what the strict gate accepts.
//
// That distinction now carries more weight than it did when this was
// written. .github/workflows/ci.yml no longer runs `make ci`; it runs
// `make ci-remote`, which runs no tests at all. Every test result this
// repository has therefore comes from a developer's machine, through
// either `make ci` or the push-gate this tool serves -- so the tolerance
// implemented here is applied to the only test run anyone performs, not
// to a local preview of a stricter run happening elsewhere. Keep the
// classification narrow accordingly: a package added to
// flaky-packages.json without a real, written reason is now a package
// nothing checks.
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
	listed, warned := flakegate.Classify(events, tolerated)

	// The isolation pass, and it is what decides. Everything that failed
	// goes through it -- the failures the list would have tolerated as
	// well as the ones it would not -- because the question a static list
	// answers ("is this package known to lose races") is not the question
	// worth asking ("did THIS test fail because of what else was running").
	//
	// A test that passes alone failed because of its neighbours. A test
	// that fails alone fails, and a reason somebody wrote in a JSON file
	// months ago does not change that. So a listed package gets no
	// protection from a real defect, which is the direction this repository
	// has actually been hurt in: four e2e tests failed identically in five
	// consecutive runs and were warned about every time, and the defect
	// behind them was a total outage.
	failures := append(append([]flakegate.Failure{}, listed...), warned...)
	confirmed, contention, notRun, err := flakegate.Isolate(failures, args, os.Stdout)
	if err != nil {
		return fmt.Errorf("re-running failures in isolation: %w", err)
	}

	if len(contention) > 0 {
		fmt.Printf("\ntestgate: %d failure(s) passed when re-run alone, so they lost a race rather than broke:\n\n", len(contention))
		for _, f := range contention {
			if entry, ok := tolerated[f.Package]; ok {
				fmt.Printf("  %s: %s (listed: %s)\n", f.Package, f.Test, flakegate.FirstSentence(entry.Reason))
				continue
			}
			// Not listed, and it did not need to be: the re-run is the
			// evidence. Named anyway, because a package that starts
			// losing races is worth somebody noticing.
			fmt.Printf("  %s: %s (not listed; tolerated on this run's own evidence)\n", f.Package, f.Test)
		}
		fmt.Println()
	}

	if len(notRun) > 0 {
		// Their own heading, because they were never asked twice. Saying
		// they failed again would be the gate reporting a check it did not
		// perform, which is worse than having no isolation pass at all.
		fmt.Fprintf(os.Stderr, "\ntestgate: %d failure(s) were NOT re-run, because more than %d distinct tests failed:\n\n", len(notRun), flakegate.MaxIsolationRetries)
		for _, f := range notRun {
			if f.Test == "" {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Kind)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Test)
		}
		fmt.Fprintln(os.Stderr, "\nThat many failures at once is a change that broke something, not a busy machine. "+
			"If you believe otherwise, re-run one of them alone and see.")
		return fmt.Errorf("%d failure(s), too many to re-run in isolation", len(notRun))
	}

	if len(confirmed) > 0 {
		fmt.Fprintf(os.Stderr, "\ntestgate: %d failure(s) failed AGAIN when re-run alone, or could not be re-run at all:\n\n", len(confirmed))
		for _, f := range confirmed {
			if f.Test == "" {
				// Named by kind, because "build failed" was printed for a
				// TIMEOUT too until the two were separated, and a reader
				// sent to look for a compile error in a package that
				// compiles fine has been sent the wrong way.
				fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Kind)
				continue
			}
			if _, ok := tolerated[f.Package]; ok {
				fmt.Fprintf(os.Stderr, "  %s: %s (its package is in flaky-packages.json, which does not cover failing alone)\n", f.Package, f.Test)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Test)
		}
		fmt.Fprintln(os.Stderr, "\nThese are not contention. Fix them.")
		return fmt.Errorf("%d failure(s) confirmed in isolation", len(confirmed))
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

	if len(contention) == 0 {
		fmt.Println("testgate: all tests passed")
	} else {
		fmt.Println("testgate: passed (every failure above passed when re-run alone)")
	}
	return nil
}
