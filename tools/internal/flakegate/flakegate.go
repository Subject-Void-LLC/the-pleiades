// Package flakegate classifies a go test -json event stream's failures
// against flaky-packages.json at the repo root, the shared decision both
// tools/testgate (test-race/test-integration) and tools/coverage-check's
// own -tolerant mode use so the two tools cannot silently disagree about
// which packages are known-flaky or what counts as "never tolerated"
// (a build failure). See flaky-packages.json's own header comment for the
// full policy and FAILURE_PATTERNS.md #61 for the incident behind it.
//
// Nothing in this package is reachable from outside this module: it lives
// under tools/internal, and both callers are themselves tools/ commands
// this module owns, mirroring gosec-check/coverage-check's own
// repo-root-relative-path assumptions.
package flakegate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Event is one line of go test -json's (test2json's) own output stream.
// Only the fields a caller of this package actually needs are declared.
type Event struct {
	Action      string `json:"Action"`
	Package     string `json:"Package"`
	Test        string `json:"Test"`
	Output      string `json:"Output"`
	FailedBuild string `json:"FailedBuild"`
}

// Failure is one (package, test) pair go test itself reported as failed.
// Test is empty for a package-level failure with no corresponding
// per-test failure event, which is what a build failure (FailedBuild set,
// or Action == "build-fail") or an otherwise-crashed test binary looks
// like.
type Failure struct {
	Package string
	Test    string
}

type flakyPackagesFile struct {
	Packages []flakyPackageEntry `json:"packages"`
}

type flakyPackageEntry struct {
	ImportPath string   `json:"import_path"`
	Reason     string   `json:"reason"`
	Tests      []string `json:"tests,omitempty"`
}

// Tolerance is one entry's decision: which failures in a package are
// downgraded to a warning, and why.
//
// Tests narrows the entry to the tests actually observed to flake. Every
// entry in flaky-packages.json already names its tests in prose, because
// the file's own policy is that an entry records evidence rather than a
// guess; this is that prose made machine-readable, not a new policy.
//
// An empty Tests tolerates the whole package, which is what every entry
// did before this field existed. That is deliberately not a second policy
// but an unnarrowed entry, the same way the file treats a missing CLASS:
// line as an absence of work rather than a third category.
type Tolerance struct {
	Reason string

	// Tests are the top-level test names this entry covers. Empty means
	// the entry has not been narrowed and covers every test in the
	// package.
	Tests map[string]bool
}

// Covers reports whether this entry tolerates a failure of the named test.
//
// Matched on the top-level name, so tolerating "TestGrandIntegration"
// tolerates its subtests too: go test reports a failing subtest as
// "Parent/Sub", and an entry recording that a test flakes is recording
// something about the test, not about which of its cases lost the race
// that time.
func (t Tolerance) Covers(test string) bool {
	if len(t.Tests) == 0 {
		return true
	}
	top := test
	if i := strings.IndexByte(top, '/'); i >= 0 {
		top = top[:i]
	}
	return t.Tests[top]
}

// LoadTolerated reads flaky-packages.json at path and returns its entries
// keyed by import path, so a lookup during Classify is a single map
// access.
func LoadTolerated(path string) (map[string]Tolerance, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller-fixed repo-relative path, not user input
	if err != nil {
		return nil, err
	}
	var f flakyPackagesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	out := make(map[string]Tolerance, len(f.Packages))
	for _, p := range f.Packages {
		tol := Tolerance{Reason: p.Reason}
		if len(p.Tests) > 0 {
			tol.Tests = make(map[string]bool, len(p.Tests))
			for _, name := range p.Tests {
				tol.Tests[name] = true
			}
		}
		out[p.ImportPath] = tol
	}
	return out, nil
}

// Classify sorts every failure event's own package into hard (blocks the
// caller) or warned (printed, does not block), given tolerated, the
// packages flaky-packages.json lists. It is a pure function of events and
// tolerated, deliberately: both callers' own tests feed it synthetic go
// test -json event streams directly, rather than needing a real, possibly
// slow or itself-flaky go test invocation to exercise this decision.
//
// A listed package tolerates only the tests its entry NAMES, when it names
// any. That narrowing exists because the package-wide version of this hid a
// total regression for a whole session: every job in the system hung, four
// tests/e2e tests failed on it in five consecutive runs, and each run
// printed "passed (warnings above)" because one entry covered the whole
// package (FAILURE_PATTERNS.md #222). An entry that names its tests turns a
// failure in a test nobody has ever seen flake back into a hard failure,
// which is the question a reader of that file would expect it to answer.
//
// A build failure (Action == "build-fail", or a "fail" event carrying
// FailedBuild) is always hard, regardless of tolerated: a package that
// does not compile is never merely "flaky." A package-level "fail" with
// no corresponding per-test failure and no FailedBuild marker is treated
// the same way, since every real go test invocation this package has been
// verified against shapes a failure as one of those two, and a third
// shape is not one this package should guess about tolerating.
func Classify(events []Event, tolerated map[string]Tolerance) (hard, warned []Failure) {
	var buildFailures []Failure
	testFailures := make(map[string][]string) // package -> test names that failed
	packageFailedWithNoTest := make(map[string]bool)

	for _, evt := range events {
		switch {
		case evt.Action == "build-fail":
			buildFailures = append(buildFailures, Failure{Package: evt.Package})
		case evt.Action == "fail" && evt.FailedBuild != "":
			buildFailures = append(buildFailures, Failure{Package: evt.Package})
		case evt.Action == "fail" && evt.Test != "":
			testFailures[evt.Package] = append(testFailures[evt.Package], evt.Test)
		case evt.Action == "fail" && evt.Test == "":
			packageFailedWithNoTest[evt.Package] = true
		}
	}

	for pkg := range packageFailedWithNoTest {
		if len(testFailures[pkg]) == 0 {
			buildFailures = append(buildFailures, Failure{Package: pkg})
		}
	}

	for pkg, tests := range testFailures {
		entry, listed := tolerated[pkg]
		for _, test := range tests {
			f := Failure{Package: pkg, Test: test}
			if listed && entry.Covers(test) {
				warned = append(warned, f)
			} else {
				hard = append(hard, f)
			}
		}
	}
	hard = append(hard, buildFailures...)

	sortFailures(hard)
	sortFailures(warned)
	return hard, warned
}

// FailedPackages returns the set of package import paths named by fs, for
// a caller that only needs to know which packages had a failure of a
// given kind rather than which specific tests.
func FailedPackages(fs []Failure) map[string]bool {
	out := make(map[string]bool, len(fs))
	for _, f := range fs {
		out[f.Package] = true
	}
	return out
}

func sortFailures(fs []Failure) {
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].Package != fs[j].Package {
			return fs[i].Package < fs[j].Package
		}
		return fs[i].Test < fs[j].Test
	})
}

// RunGoTestJSON runs `go test <args...> -json ./...` and decodes every
// line of its JSON output stream. When echo is non-nil, it writes one
// line per PACKAGE-level completion event ("ok  <pkg>" or "FAIL <pkg>"),
// the same volume plain `go test ./...` (no -v, no -json) prints by
// default, so a slow suite's progress is still visible live without
// reproducing every one of go test -json's own per-subtest RUN/PASS
// events verbatim.
//
// This package used to tee the complete raw JSON stream to echo instead,
// on the reasoning that live progress beats going silent for the twenty
// minutes a full suite can take. It was real progress visibility and it
// was also tens of thousands of lines for a single ./... run (one JSON
// object per subtest, not per package): `.githooks/pre-push` piping that
// volume through `git push`'s own hook-output channel twice reproduced a
// SIGPIPE that killed the push immediately after the hook itself had
// already printed "all checks passed" -- the hook succeeded and the push
// still never reached the remote. Cutting the volume back down to
// approximately one line per package (roughly 150 for this module, not
// tens of thousands) removed the reproduction.
//
// Its own error return is go test's exit status, not a usage error: go
// test exits non-zero whenever any test fails, which is the ordinary,
// expected case a caller classifies with Classify rather than treats as a
// failure to even run the suite.
func RunGoTestJSON(args []string, echo io.Writer) ([]Event, error) {
	full := append([]string{"test"}, args...)
	full = append(full, "-json", "./...")

	// #nosec G204 -- args is always a fixed literal slice built at each
	// caller's own call site (testgate's main.go, coverage-check's
	// measureCoverageTolerant), never derived from user input, an
	// environment variable, or anything else outside this module's own
	// source; gosec cannot see through the parameter to confirm that.
	cmd := exec.Command("go", full...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("attaching stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting go test: %w", err)
	}

	var events []Event
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			// A line that is not valid JSON is not itself a failure this
			// package should classify: go test -json's own contract is one
			// JSON object per line, so anything else is either a scanner
			// artifact or output this package does not need to understand,
			// not evidence of a test outcome either way.
			continue
		}
		events = append(events, evt)

		if echo != nil && evt.Test == "" && (evt.Action == "pass" || evt.Action == "fail") {
			status := "ok  "
			if evt.Action == "fail" {
				status = "FAIL"
			}
			fmt.Fprintf(echo, "%s\t%s\n", status, evt.Package)
		}
	}
	scanErr := scanner.Err()

	waitErr := cmd.Wait()
	if scanErr != nil {
		return events, fmt.Errorf("reading go test output: %w", scanErr)
	}
	return events, waitErr
}
