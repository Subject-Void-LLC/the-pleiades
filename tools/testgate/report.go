// What a testgate run leaves behind besides its exit status: the coverage
// it measured, and an account of what it skipped and what it tolerated,
// printed and, on GitHub Actions, written to the job's summary page.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// ledgerLimit is how many tests the skip ledger names under each reason
// before counting the rest.
const ledgerLimit = 8

// writeCoverage writes coverage as JSON to path, for coverage-check
// -measured to read. The file is the measurement, so it is written whole
// or not at all.
func writeCoverage(path string, coverage map[string]float64) error {
	data, err := json.MarshalIndent(coverage, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding coverage: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// summarize prints the skip ledger and, when the run is on GitHub Actions,
// appends it and the tolerated failures to the job's summary page, which
// is where a reviewer reads a run without opening its log.
//
// GITHUB_STEP_SUMMARY is a file path GitHub hands the step. It is only
// ever opened for appending text this tool wrote, never executed or
// passed to a command.
func summarize(label string, skips []flakegate.Skip, contention []flakegate.Failure) {
	ledger := flakegate.SkipLedger(skips, ledgerLimit)
	if ledger != "" {
		fmt.Printf("\ntestgate: %s", ledger)
	}

	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", label)
	if len(contention) > 0 {
		fmt.Fprintf(&b, "**%d failure(s) passed when re-run alone** (tolerated as contention; a real concurrency bug looks the same, so a test that shows up here run after run needs a look):\n\n", len(contention))
		for _, f := range contention {
			fmt.Fprintf(&b, "- `%s` %s\n", f.Package, f.Test)
		}
		b.WriteString("\n")
	}
	if ledger == "" {
		b.WriteString("No test was skipped.\n\n")
	} else {
		fmt.Fprintf(&b, "```\n%s```\n\n", ledger)
	}
	// #nosec G304 -- the path is the file GitHub Actions itself provides
	// for this step's summary; gosec's G703 on the same line is waived in
	// gosec-waivers.json with the reason.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testgate: could not write the job summary: %v\n", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "testgate: could not write the job summary: %v\n", err)
	}
}
