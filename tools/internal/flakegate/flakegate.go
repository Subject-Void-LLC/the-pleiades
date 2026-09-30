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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
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

	// ImportPath is how a BUILD event names its package, and it is a
	// different field from Package on purpose. `go help buildjson`: "The
	// ImportPath field ... matches the ... TestEvent.FailedBuild field of
	// go test -json. Note that it does not match TestEvent.Package."
	// Without this declared, every build failure was recorded against the
	// empty string.
	ImportPath string `json:"ImportPath"`
}

// buildPackage is the package a build event is about.
//
// A build event spells the package two ways depending on which file failed
// to compile: "example.com/p [example.com/p.test]" when a non-test file
// did, and "example.com/p.test" when a test file did. Both are stripped
// back to the import path, so a build failure is reported under the same
// name every other failure uses.
func buildPackage(evt Event) string {
	path := evt.ImportPath
	if path == "" {
		// A shape this package has not observed. The Package field is the
		// only other candidate and is usually empty here, which is worse
		// than useless -- but guessing is worse than reporting what came.
		path = evt.Package
	}
	if i := strings.Index(path, " ["); i >= 0 {
		path = path[:i]
	}
	return strings.TrimSuffix(path, ".test")
}

// dedupeByPackage keeps the first failure per package, preserving order.
func dedupeByPackage(failures []Failure) []Failure {
	seen := make(map[string]bool, len(failures))
	out := failures[:0]
	for _, f := range failures {
		if seen[f.Package] {
			continue
		}
		seen[f.Package] = true
		out = append(out, f)
	}
	return out
}

// Failure is one (package, test) pair go test itself reported as failed.
// Test is empty for a package-level failure with no corresponding
// per-test failure event, which is what a build failure (FailedBuild set,
// or Action == "build-fail") or an otherwise-crashed test binary looks
// like.
type Failure struct {
	Package string
	Test    string

	// Kind separates three things go test reports differently and this
	// package used to merge, with a consequence worth stating: a package
	// that TIMED OUT was classified as a build failure, so the isolation
	// pass skipped it and printed "build failed". A timeout under parallel
	// load is the single commonest contention symptom there is -- it is
	// what flaky-packages.json and FAILURE_PATTERNS #61 are mostly about --
	// so the pass was declining to re-run the exact shape it exists for.
	Kind FailureKind
}

// FailureKind is what go test actually reported.
type FailureKind int

// The kinds. FailedTest is the zero value because it is the ordinary case
// and because a Failure built by a caller that predates this field is a
// named test.
const (
	// FailedTest is a named test that failed. Re-runnable on its own.
	FailedTest FailureKind = iota

	// FailedBuild is a package that did not compile. Never re-run and
	// never tolerated: it did not lose a race.
	FailedBuild

	// FailedPackage is a package-level failure with no named test: a
	// timeout, or a panic that took the test binary down before any test
	// could be blamed. Re-runnable, but only as a whole package.
	FailedPackage
)

// String names a kind for a message.
func (k FailureKind) String() string {
	switch k {
	case FailedBuild:
		return "build failed"
	case FailedPackage:
		return "the package's test binary failed or timed out"
	default:
		return "failed"
	}
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
	// Which packages already have a build failure, so the package-level
	// fail that accompanies one is not counted a second time as a timeout.
	builtFailed := make(map[string]bool)

	for _, evt := range events {
		switch {
		case evt.Action == "build-fail":
			// A build event names the package in ImportPath and leaves
			// Package empty -- `go help buildjson` says so explicitly --
			// so reading evt.Package here produced a nameless failure
			// beside the real one, and reported one broken package as two.
			pkg := buildPackage(evt)
			builtFailed[pkg] = true
			buildFailures = append(buildFailures, Failure{Package: pkg, Kind: FailedBuild})
		case evt.Action == "fail" && evt.FailedBuild != "":
			builtFailed[evt.Package] = true
			buildFailures = append(buildFailures, Failure{Package: evt.Package, Kind: FailedBuild})
		case evt.Action == "fail" && evt.Test != "":
			testFailures[evt.Package] = append(testFailures[evt.Package], evt.Test)
		case evt.Action == "fail" && evt.Test == "":
			packageFailedWithNoTest[evt.Package] = true
		}
	}

	// A package that failed with no test to blame is a timeout or a panic,
	// not a compile error, and it is re-runnable as a whole package.
	var packageFailures []Failure
	for pkg := range packageFailedWithNoTest {
		if len(testFailures[pkg]) == 0 && !builtFailed[pkg] {
			packageFailures = append(packageFailures, Failure{Package: pkg, Kind: FailedPackage})
		}
	}

	// One entry per broken package. The build-fail event and the fail
	// event that accompanies it name the same package, so without this a
	// single compile error is reported twice.
	buildFailures = dedupeByPackage(buildFailures)

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
	hard = append(hard, packageFailures...)

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
	return RunGoTestJSONPackages(args, []string{"./..."}, echo)
}

// RunGoTestJSONPackages is RunGoTestJSON over the named packages rather
// than the whole module, so one test tier (the container-free packages, or
// one shard of the container ones) can run through the same event stream,
// the same classification and the same isolation pass as the whole suite.
// packages must not be empty: an empty list would make go test run the
// package in the current directory, which is never what a caller means.
func RunGoTestJSONPackages(args, packages []string, echo io.Writer) ([]Event, error) {
	if len(packages) == 0 {
		return nil, errors.New("no packages to test")
	}
	full := append([]string{"test"}, args...)
	full = append(full, "-json")
	full = append(full, packages...)

	// #nosec G204 -- args is always a fixed literal slice built at each
	// caller's own call site (testgate's main.go, coverage-check's
	// measureCoverageTolerant), and packages are import paths the Makefile
	// lists or `go list` printed, never derived from user input or an
	// environment variable; gosec cannot see through the parameters to
	// confirm that.
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

// maxIsolationRetries bounds how many failures the isolation pass will
// re-run.
//
// A run with more failures than this is not a contended machine losing a
// race; it is a change that broke something, and re-running that many one at
// a time would cost more wall clock than the whole suite. Past the bound the
// failures are reported AS NOT RE-RUN, under their own heading, rather than
// folded in with the ones that were: a gate that says a test failed twice
// when it was only asked once is worse than one with no isolation pass at
// all, because the sentence is what a reader acts on.
//
// Thirty rather than a dozen because a loaded machine really does produce
// more than a dozen. One container that fails to start takes its whole
// conformance suite with it, so a single provisioning loss can be seven
// failures on its own, and a bound that trips on that turns the pass off
// exactly when it is most needed.
const maxIsolationRetries = 30

// MaxIsolationRetries is the bound, exported so the gate can name the
// number in the sentence it prints rather than repeating the literal.
const MaxIsolationRetries = maxIsolationRetries

// TopLevel is the parent test a name belongs to, which is the unit the
// isolation pass re-runs.
//
// A subtest cannot be re-run without its parent, and `-run` patterns for
// one are an escaping problem with no upside: the parent is what owns the
// fixture that a contended machine failed to build, so running it is what
// answers the question.
func TopLevel(test string) string {
	if i := strings.IndexByte(test, '/'); i >= 0 {
		return test[:i]
	}
	return test
}

// Isolate re-runs each failed test on its own and reports which ones
// failed again.
//
// This is the evidence the static waiver list cannot supply. A package
// named in flaky-packages.json is tolerated on the strength of a reason
// somebody wrote once, which stays true only as long as the package keeps
// failing for that reason -- and the failure mode this repository has
// actually shipped is a real defect arriving inside a tolerated package and
// being warned about, run after run, by a gate that never looked again.
//
// Re-running settles it per run rather than per entry. A test that passes
// alone failed because of what else was running; a test that fails alone
// fails, whatever any list says about its package. So a confirmed failure
// is HARD even when the package is listed, which is strictly stricter than
// the list on its own, and an unlisted failure that passes alone is a
// warning, which is the only direction this loosens.
//
// What it cannot do is separate contention from a genuine concurrency bug:
// both fail together and pass alone. That limit is worth stating rather
// than implying, because it is the one shape this pass will wave through.
func Isolate(failures []Failure, args []string, echo io.Writer) (confirmed, contention, notRun []Failure, err error) {
	// One re-run per parent test, since a parent and three of its subtests
	// are one fixture and one answer.
	seen := make(map[string]bool, len(failures))
	var targets []Failure
	for _, f := range failures {
		if f.Kind == FailedBuild {
			// Never re-run and never tolerated: a package that does not
			// compile did not lose a race.
			confirmed = append(confirmed, f)
			continue
		}
		// A package-level failure is re-run as a WHOLE package, with no
		// -run pattern, because there is no test to name. This is the
		// shape a timeout takes, and a timeout under parallel load is the
		// commonest contention symptom there is -- so skipping it, as this
		// pass first did, was declining to examine the very failure it
		// exists for.
		target := Failure{Package: f.Package, Kind: f.Kind}
		if f.Kind == FailedTest {
			target.Test = TopLevel(f.Test)
		}
		key := target.Package + "\x00" + target.Test
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, target)
	}

	if len(targets) > maxIsolationRetries {
		if echo != nil {
			fmt.Fprintf(echo, "flakegate: %d distinct test failures, more than the %d this pass re-runs; none were re-run\n",
				len(targets), maxIsolationRetries)
		}
		sortFailures(confirmed)
		sortFailures(targets)
		return confirmed, nil, targets, nil
	}

	for _, target := range targets {
		if echo != nil {
			fmt.Fprintf(echo, "flakegate: re-running %s %s alone\n", target.Package, target.Test)
		}
		verdict, runErr := runAlone(target, args)
		switch {
		case runErr != nil, verdict == isolationInconclusive:
			// The re-run did not answer the question: it could not be
			// started, or it produced no verdict for this test at all --
			// a build error in the isolated run, a -run pattern that
			// matched nothing, a binary that died before reporting.
			//
			// Reported as confirmed, because the alternative is tolerating
			// a failure on the strength of a check that did not happen.
			// Said out loud, because "failed again when re-run alone" is
			// not what happened and this gate's whole value is that its
			// sentences are true.
			if echo != nil {
				fmt.Fprintf(echo, "flakegate: re-running %s %s did not produce a verdict (%v); treating it as confirmed\n",
					target.Package, target.Test, runErr)
			}
			confirmed = append(confirmed, target)
		case verdict == isolationPassed:
			contention = append(contention, target)
		default:
			confirmed = append(confirmed, target)
		}
	}

	sortFailures(confirmed)
	sortFailures(contention)
	return confirmed, contention, nil, nil
}

// isolationVerdict is what one re-run established.
type isolationVerdict int

const (
	// isolationInconclusive means the re-run answered nothing about this
	// target: it did not build, nothing matched, or the binary died before
	// reporting a result. Never treated as a pass.
	isolationInconclusive isolationVerdict = iota
	isolationPassed
	isolationFailed
)

// runAlone re-runs one target and reports what happened.
//
// It reads go test's own -json stream rather than its exit status, and that
// is the difference between a verdict and a guess. A non-zero exit means
// "something went wrong", which covers the test failing, the package not
// compiling, a -run pattern matching nothing, and the toolchain itself
// falling over -- and only the first of those is evidence about the test.
// Inferring from the exit code alone reported a link step that ran out of
// memory as "this test failed again", which is a sentence that sends
// somebody looking for a defect the run never examined.
//
// The same flags the full run used, so the re-run is the same test under
// the same race detector and the same build tags; only the scope changes.
func runAlone(target Failure, args []string) (isolationVerdict, error) {
	full := []string{"test", "-json"}
	full = append(full, args...)
	if !hasCountFlag(args) {
		full = append(full, "-count=1")
	}
	if target.Test != "" {
		full = append(full, "-run", "^"+regexp.QuoteMeta(target.Test)+"$")
	}
	full = append(full, target.Package)

	// #nosec G204 -- args is the caller's own fixed literal slice, and the
	// package and test names come from go test's own -json output for this
	// module, never from user input. The test name is regexp-quoted and
	// anchored, so it cannot widen the selection either.
	cmd := exec.Command("go", full...)
	cmd.Stderr = io.Discard

	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		// Could not start the toolchain at all.
		return isolationInconclusive, err
	}

	return verdictFrom(out, target), nil
}

// verdictFrom reads a re-run's event stream for this target's own result.
//
// A package-level target takes the package's verdict; a named test takes
// its own, ignoring the package result around it. A build failure in the
// re-run is inconclusive rather than a failure, because it says the tree
// changed under us and nothing about the test.
func verdictFrom(stream []byte, target Failure) isolationVerdict {
	verdict := isolationInconclusive

	for _, line := range bytes.Split(stream, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.Action == "build-fail" || evt.FailedBuild != "" {
			return isolationInconclusive
		}
		if evt.Package != target.Package {
			continue
		}

		// A named test answers for itself. Its subtests do not: a parent
		// that fails because one subtest did is still a failure, and the
		// parent's own event carries that.
		if target.Test != "" && evt.Test != target.Test {
			continue
		}
		if target.Test == "" && evt.Test != "" {
			continue
		}

		switch evt.Action {
		case "pass":
			verdict = isolationPassed
		case "fail":
			verdict = isolationFailed
		}
	}
	return verdict
}

// hasCountFlag reports whether the caller already fixed a count, so the
// re-run does not append a second one.
func hasCountFlag(args []string) bool {
	for _, a := range args {
		if a == "-count" || strings.HasPrefix(a, "-count=") {
			return true
		}
	}
	return false
}

// FirstSentence is the opening sentence of a waiver reason.
//
// The reasons in flaky-packages.json are paragraphs by design -- each is a
// record of what was observed and when -- and printing one in full for
// every tolerated failure buries the failures under the explanations. The
// whole reason is one file away.
//
// It breaks at ". " rather than at "." because these reasons are full of
// dotted names. Cutting at the first period alone reported four of the
// nineteen real entries as a citation fragment: "FAILURE_PATTERNS.",
// "CLAUDE.", "internal/ent/conformance_backends_test.". A period with no
// space after it is inside a name, not between sentences.
//
// A reason with no sentence break is returned whole rather than cut at an
// arbitrary width: a long line is a nuisance, a silently truncated reason
// is misinformation.
func FirstSentence(reason string) string {
	if i := strings.Index(reason, ". "); i >= 0 {
		return reason[:i+1]
	}
	// A reason that is exactly one sentence, ending in a period with
	// nothing after it.
	if strings.HasSuffix(reason, ".") && strings.Count(reason, ".") == 1 {
		return reason
	}
	if i := strings.IndexByte(reason, '\n'); i >= 0 {
		return strings.TrimSpace(reason[:i])
	}
	return reason
}
