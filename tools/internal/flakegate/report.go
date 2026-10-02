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
// returns the empty string when nothing was skipped.
func SkipLedger(skips []Skip, limit int) string {
	if len(skips) == 0 {
		return ""
	}
	groups := map[string][]Skip{}
	for _, s := range skips {
		groups[s.Reason] = append(groups[s.Reason], s)
	}
	reasons := make([]string, 0, len(groups))
	for r := range groups {
		reasons = append(reasons, r)
	}
	sort.Slice(reasons, func(a, b int) bool {
		if len(groups[reasons[a]]) != len(groups[reasons[b]]) {
			return len(groups[reasons[a]]) > len(groups[reasons[b]])
		}
		return reasons[a] < reasons[b]
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d test(s) skipped, so they are not evidence of anything in this run:\n", len(skips))
	for _, r := range reasons {
		g := groups[r]
		fmt.Fprintf(&b, "\n  %d: %s\n", len(g), r)
		for i, s := range g {
			if i == limit {
				fmt.Fprintf(&b, "      and %d more\n", len(g)-limit)
				break
			}
			fmt.Fprintf(&b, "      %s %s\n", shortPackage(s.Package), s.Test)
		}
	}
	return b.String()
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

// FailureOutput returns the last maxLines lines f's own test printed, or
// for a package-level failure (a build failure, a timeout, a crash) what
// the package printed outside any test. A gate that names a failure
// without it sends the reader to rerun the test just to learn why, which
// on a CI runner they cannot do.
func FailureOutput(events []Event, f Failure, maxLines int) string {
	var lines []string
	for _, evt := range events {
		if evt.Action != "output" || evt.Package != f.Package || evt.Test != f.Test {
			continue
		}
		lines = append(lines, strings.TrimRight(evt.Output, "\n"))
	}
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	cut := len(lines) - maxLines
	tail := lines[cut:]
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
	head := fmt.Sprintf("... %d earlier line(s) left out", cut)
	return strings.Join(append([]string{head}, tail...), "\n")
}

// testMessage matches the first line of a message a test file printed
// through t.Log, t.Error or t.Fatal: its file and line, then the text.
var testMessage = regexp.MustCompile(`^\s*\S+_test\.go:\d+: `)
