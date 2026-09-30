// How testgate reaches and explains a verdict: the strict judgment, the
// re-measurement of a package whose failure was contention, and the output
// it prints for each failure so a log alone says why.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// judgeStrict is the verdict with no tolerance: any failure fails, and so
// does a non-zero exit from go test with nothing classified, which is go
// test refusing to run at all (a package list it could not load, say).
func judgeStrict(failures []flakegate.Failure, waitErr error, events int) error {
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\ntestgate: %d failure(s), and -strict re-runs nothing:\n\n", len(failures))
		for _, f := range failures {
			if f.Test == "" {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Kind)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Test)
		}
		return fmt.Errorf("%d failure(s)", len(failures))
	}
	if waitErr != nil {
		return fmt.Errorf("go test exited with %v and %d event(s), none of them a failure it could name", waitErr, events)
	}
	fmt.Println("testgate: all tests passed")
	return nil
}

// failureLines is how much of a failure's own output testgate prints: its
// end, which is where a test states why it failed.
const failureLines = 40

// contentionLines is how much of a tolerated failure's output testgate
// prints: enough to see what timed out or raced, short enough that a busy
// run's log stays readable.
const contentionLines = 15

// indent prefixes every line of text with four spaces, so quoted output
// reads as belonging to the line above it.
func indent(text string) string {
	return "    " + strings.ReplaceAll(text, "\n", "\n    ")
}

// printOutput prints each failure's own output from the run, so a failure
// can be understood from the gate's log alone.
func printOutput(events []flakegate.Event, failures []flakegate.Failure) {
	for _, f := range failures {
		out := flakegate.FailureOutput(events, f, failureLines)
		if out == "" {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n--- %s %s ---\n%s\n", f.Package, f.Test, out)
	}
}

// remeasure runs each package that had a contention failure again, whole
// and alone, and records its coverage from that run in coverage. It
// returns a package-level failure for each one that fails even alone.
func remeasure(args []string, contention []flakegate.Failure, coverage map[string]float64) []flakegate.Failure {
	var failed []flakegate.Failure
	for pkg := range flakegate.FailedPackages(contention) {
		fmt.Printf("testgate: re-measuring %s alone, since its coverage came from a run where a test stopped partway\n", pkg)
		events, _ := flakegate.RunGoTestJSONPackages(args, []string{pkg}, os.Stdout)
		hard, warned := flakegate.Classify(events, nil)
		if len(hard)+len(warned) > 0 {
			printOutput(events, append(hard, warned...))
			failed = append(failed, flakegate.Failure{Package: pkg, Kind: flakegate.FailedPackage})
			continue
		}
		if pct, ok := flakegate.Coverage(events)[pkg]; ok {
			coverage[pkg] = pct
		} else {
			delete(coverage, pkg)
		}
	}
	return failed
}
