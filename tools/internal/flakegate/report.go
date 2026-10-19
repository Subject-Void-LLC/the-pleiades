// Reading what a test run did besides pass and fail: which tests it
// skipped and why, and how much of each package it covered. Both come out
// of the same go test -json event stream the classification reads, so a
// gate reports them from the one run it already paid for.
package flakegate

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gate"
)

// Skip is one test go test reported as skipped, with the reason the test
// gave.
type Skip struct {
	Package string
	Test    string
	Reason  string
}

// skipNoise matches the lines go test itself writes around a test's own
// output, which are not the reason.
var skipNoise = regexp.MustCompile(`^(=== (RUN|PAUSE|CONT|NAME)|--- SKIP:)`)

// skipLocation is the "file_test.go:12: " prefix t.Skip puts before its
// message. A bare t.Skip() prints the prefix alone.
var skipLocation = regexp.MustCompile(`^\S+\.go:\d+:\s*`)

// Skips returns every test the run skipped, in the order they finished,
// each with the first line of the message its t.Skip gave. A package with
// no test files is not a skip: go test reports that with no test name, and
// it is left out.
//
// This is what keeps a green run honest. A test that needs Docker, a real
// Windows host or a tool this machine lacks skips rather than fails, and a
// skip prints nothing in a normal run, so "every test passed" and "half
// the gates never ran" used to look identical.
func Skips(events []Event) []Skip {
	type key struct{ pkg, test string }
	output := map[key][]string{}
	var skips []Skip
	for _, evt := range events {
		if evt.Test == "" {
			continue
		}
		k := key{evt.Package, evt.Test}
		switch evt.Action {
		case "output":
			output[k] = append(output[k], evt.Output)
		case "skip":
			skips = append(skips, Skip{Package: evt.Package, Test: evt.Test, Reason: skipReason(output[k])})
			delete(output, k)
		case "pass", "fail":
			delete(output, k)
		}
	}
	return skips
}

// skipReason is the first line of a skipped test's own output that is not
// go test's framing, without its source location.
func skipReason(lines []string) string {
	for _, raw := range lines {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || skipNoise.MatchString(line) {
				continue
			}
			// A bare t.Skip() leaves only its location, which is not a
			// reason; keep looking, and say so if there is none.
			if reason := skipLocation.ReplaceAllString(line, ""); reason != "" {
				return reason
			}
		}
	}
	return "no reason given"
}

// SkipLedger formats skips for a person: grouped by reason, largest group
// first, naming up to limit tests in each and counting the rest. It
// returns the empty string when nothing was skipped. The grouping is
// pkg/gate's Ledger, which gate.run's skipped checks share.
func SkipLedger(skips []Skip, limit int) string {
	entries := make([]gate.SkipEntry, len(skips))
	for i, s := range skips {
		entries[i] = gate.SkipEntry{Name: shortPackage(s.Package) + " " + s.Test, Reason: s.Reason}
	}
	return gate.Ledger("test(s)", entries, limit)
}

// shortPackage drops this module's own path prefix from an import path,
// which every package here shares and which only makes a ledger wider.
func shortPackage(pkg string) string {
	return strings.TrimPrefix(pkg, "github.com/Subject-Void-LLC/the-pleiades/")
}

// requirementSkip matches the reason internal/testsupport.Require gives a
// skip, "needs <what>: <why>", capturing what the machine lacked.
// TestMissing_ReadsTheReasonRequireGives holds the two to one format.
var requirementSkip = regexp.MustCompile(`^needs ([a-z0-9][a-z0-9_-]*): `)

// Missing returns, for each package, what its tests skipped for lacking
// (testsupport.Require's skips), sorted and without repeats. A package in
// it was measured without something its tests use, so its coverage is
// lower for a reason that is not its code: a floor set on a machine with
// LocalStack cannot be checked by a run without it.
func Missing(skips []Skip) map[string][]string {
	seen := map[string]map[string]bool{}
	for _, s := range skips {
		m := requirementSkip.FindStringSubmatch(s.Reason)
		if m == nil {
			continue
		}
		if seen[s.Package] == nil {
			seen[s.Package] = map[string]bool{}
		}
		seen[s.Package][m[1]] = true
	}
	out := make(map[string][]string, len(seen))
	for pkg, needs := range seen {
		for need := range needs {
			out[pkg] = append(out[pkg], need)
		}
		sort.Strings(out[pkg])
	}
	return out
}

// Measurement is what testgate -coverage-out writes and coverage-check
// -measured reads: each package's coverage, and what each package's tests
// lacked while it was measured.
type Measurement struct {
	Coverage map[string]float64  `json:"coverage"`
	Missing  map[string][]string `json:"missing,omitempty"`
	// Seconds is how long each package's tests took (Elapsed), which
	// testgate -shard balances the container packages by.
	Seconds map[string]float64 `json:"seconds,omitempty"`
}

// Elapsed returns how long each package's test binary ran, from its own
// pass or fail event, keyed by import path. A package whose binary never ran
// is absent.
func Elapsed(events []Event) map[string]float64 {
	out := map[string]float64{}
	for _, evt := range events {
		if evt.Test == "" && evt.Package != "" && (evt.Action == "pass" || evt.Action == "fail") {
			out[evt.Package] = evt.Elapsed
		}
	}
	return out
}

// coveragePercent matches go test's "coverage: 91.5% of statements".
var coveragePercent = regexp.MustCompile(`coverage:\s+([\d.]+)% of statements`)

// Coverage returns each package's statement coverage as go test printed it
// in the run's own events, keyed by import path. A package with nothing to
// instrument ("[no statements]") prints no number and is absent, as is a
// package whose test binary never ran; a caller that expects a number for
// a package must treat its absence as "not measured", never as a pass.
func Coverage(events []Event) map[string]float64 {
	out := map[string]float64{}
	for _, evt := range events {
		if evt.Action != "output" || evt.Package == "" {
			continue
		}
		m := coveragePercent.FindStringSubmatch(evt.Output)
		if m == nil {
			continue
		}
		pct, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		out[evt.Package] = pct
	}
	return out
}

// FailureOutput returns the last maxLines lines f's own test and its
// subtests printed, or for a package-level failure (a build failure, a
// timeout, a crash) what the package printed outside any test. A gate that
// names a failure without it sends the reader to rerun the test just to
// learn why, which on a CI runner they cannot do.
//
// A subtest's events carry its own name (TestX/case), and a table test's
// reason is always in one: matching f.Test alone printed a failed table
// test's "--- FAIL" line and not one word of which case failed or why.
func FailureOutput(events []Event, f Failure, maxLines int) string {
	var lines []string
	for _, evt := range events {
		if evt.Action != "output" || evt.Package != f.Package || !ownedBy(evt.Test, f.Test) {
			continue
		}
		lines = append(lines, strings.TrimRight(evt.Output, "\n"))
	}
	tail, cut := gate.LastLines(lines, maxLines)
	if cut == 0 {
		return strings.Join(lines, "\n")
	}
	// The last lines are often what ran after the failure: cleanup,
	// container teardown, a broker's log dumped by a helper. When none of
	// them came from the test's own file, the last line that did is kept
	// as well, since that is usually the failure itself; without it a
	// tolerated failure in internal/topology printed fifteen lines of
	// teardown and not one word of why.
	if !slices.ContainsFunc(tail, testMessage.MatchString) {
		for i := cut - 1; i >= 0; i-- {
			if testMessage.MatchString(lines[i]) {
				head := []string{fmt.Sprintf("... %d earlier line(s) left out; the last the test's own file printed:", cut-1), lines[i], "..."}
				return strings.Join(append(head, tail...), "\n")
			}
		}
	}
	return strings.Join(append([]string{gate.Omitted(cut)}, tail...), "\n")
}

// ownedBy reports whether an event's test is test itself or one of its
// subtests; an empty test is the package's own output, outside any test.
func ownedBy(eventTest, test string) bool {
	return eventTest == test || test != "" && strings.HasPrefix(eventTest, test+"/")
}

// testMessage matches the first line of a message a test file printed
// through t.Log, t.Error or t.Fatal: its file and line, then the text.
var testMessage = regexp.MustCompile(`^\s*\S+_test\.go:\d+: `)
