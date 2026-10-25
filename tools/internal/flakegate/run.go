// The record of one testgate run for a reader that is not a person: what it
// tested, how it ended, each failure with its outcome and output, what it
// skipped and why, and what it measured. testgate -report-out writes it, and
// tools/prreport merges a pr-check run's records into one document, so a
// run is read from that rather than from the text of several logs.
package flakegate

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// RunSchema is the version of Run's shape. A reader refuses a record of any
// other, since a field it does not know may be the one that failed the run.
const RunSchema = 1

// RunKind is the kind every Run record carries, so a reader of a directory
// of records tells a run from a check's leg or a step.
const RunKind = "testgate"

// The verdicts a run ends with.
const (
	// VerdictPass is a run with nothing against it: no failure, or only
	// failures that passed when re-run alone.
	VerdictPass = "pass"
	// VerdictFail is a run with a failure against it.
	VerdictFail = "fail"
	// VerdictError is a run that could not reach a verdict: go test would not
	// start, or the isolation pass could not run.
	VerdictError = "error"
	// VerdictNotRun is a run that never started, as a check's leg that never
	// reached its tests.
	VerdictNotRun = "not-run"
)

// The outcomes a failure has.
const (
	// OutcomeFailed counts against the run: a strict run's failure, or one
	// that failed again when re-run alone.
	OutcomeFailed = "failed"
	// OutcomeContention passed when re-run alone, so it lost a race; it does
	// not count against the run, and a test that keeps showing up here needs
	// a look, since a real concurrency bug looks the same.
	OutcomeContention = "contention"
	// OutcomeNotRerun was never re-run, because too many tests failed at
	// once; it counts against the run.
	OutcomeNotRerun = "not-rerun"
)

// Run is one testgate run's record.
type Run struct {
	// Schema is the run record's version (RunSchema).
	Schema int `json:"schema"`
	// Kind is always testgate.
	Kind string `json:"kind"`
	// Label is the run's go test arguments, as testgate prints it.
	Label string `json:"label"`
	// OS is the operating system the run's go test ran on (GOOS).
	OS string `json:"os"`
	// Arch is the architecture it ran on (GOARCH).
	Arch string `json:"arch"`
	// Packages are the import paths the run tested.
	Packages []string `json:"packages"`
	// Commit is the commit pr-check began at, which the run tested
	// (testgate -commit); absent on a record nobody stamped, which a merge
	// cannot tie to any commit.
	Commit string `json:"commit,omitempty"`
	// Clean is true when the working tree had no uncommitted changes as
	// pr-check began (testgate -clean yes). A stamped record without it ran
	// over code that is in no commit.
	Clean bool `json:"clean,omitempty"`
	// Started is when the run began.
	Started time.Time `json:"started"`
	// Seconds is how long the run took.
	Seconds float64 `json:"seconds"`
	// Verdict is pass, fail, error (the run reached no verdict) or not-run.
	Verdict string `json:"verdict"`
	// Error is why a run could not reach a verdict, or why it failed beyond
	// its failures (too many to re-run).
	Error string `json:"error,omitempty"`
	// Failures are each failure go test reported, with how the run judged
	// it.
	Failures []RunFailure `json:"failures,omitempty"`
	// Skips are each test the run skipped, with its reason.
	Skips []Skip `json:"skips,omitempty"`
	// Coverage is each package's statement coverage, in percent.
	Coverage map[string]float64 `json:"coverage,omitempty"`
	// Missing is, for each package, what its tests skipped for lacking.
	Missing map[string][]string `json:"missing,omitempty"`
	// PackageSeconds is how long each package's tests took.
	PackageSeconds map[string]float64 `json:"package_seconds,omitempty"`
}

// RunFailure is one failure, as go test reported it and as the run judged
// it.
type RunFailure struct {
	// Package is the failing package's import path.
	Package string `json:"package"`
	// Test is empty for a package-level failure.
	Test string `json:"test,omitempty"`
	// Kind is test, build or package (FailureKind).
	Kind string `json:"kind"`
	// Outcome is failed, contention (it passed when re-run alone, so it
	// does not count against the run) or not-rerun.
	Outcome string `json:"outcome"`
	// Output is the end of what the test and its subtests printed
	// (FailureOutput), masked by the caller.
	Output string `json:"output,omitempty"`
}

// kindNames are FailureKind's names in a record.
var kindNames = map[FailureKind]string{FailedTest: "test", FailedBuild: "build", FailedPackage: "package"}

// RunFailures returns fs as records with outcome, each with the last
// maxLines of its own output passed through mask.
func RunFailures(events []Event, fs []Failure, outcome string, maxLines int, mask func(string) string) []RunFailure {
	out := make([]RunFailure, 0, len(fs))
	for _, f := range fs {
		out = append(out, RunFailure{
			Package: f.Package,
			Test:    f.Test,
			Kind:    kindNames[f.Kind],
			Outcome: outcome,
			Output:  mask(FailureOutput(events, f, maxLines)),
		})
	}
	return out
}

// WriteRun writes r as JSON to path, whole or not at all.
func WriteRun(path string, r Run) error {
	r.Schema, r.Kind = RunSchema, RunKind
	return writeJSON(path, r)
}

// ReadRun reads a Run record, refusing one of another kind or schema.
func ReadRun(data []byte) (Run, error) {
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, fmt.Errorf("reading a testgate record: %w", err)
	}
	if r.Kind != RunKind {
		return Run{}, fmt.Errorf("a record of kind %q is not a testgate run", r.Kind)
	}
	if r.Schema != RunSchema {
		return Run{}, fmt.Errorf("a testgate record of schema %d, where this reader knows %d", r.Schema, RunSchema)
	}
	return r, nil
}

// writeJSON writes v to path through a temporary file renamed over it, so a
// reader never sees half a record.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
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
