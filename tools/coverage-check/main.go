// Command coverage-check runs the full test suite with -cover and fails
// if any package's coverage drops below the floor recorded for it in
// coverage-floor.json at the repo root. This is the coverage ratchet the
// Phase 0 CI harness item's coverage policy decision describes
// (.SPECIFICATION/IMPLEMENTATION.md): no package may regress, 90%
// (AGENTS.md's stated minimum) is the target for new and touched code,
// and a package with no recorded floor yet is reported, not failed, so
// adding a new package never blocks CI by itself.
//
// Usage: go run ./tools/coverage-check [-tolerant]
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// flakyPackagesPath is the repo-relative path to the waiver file
// -tolerant mode reads, the same constant tools/testgate uses for the
// same reason (this tool is invoked from the repo root).
const flakyPackagesPath = "flaky-packages.json"

// tolerance absorbs floating point rounding in go test's own printed
// percentage; a package must drop by more than this to count as a real
// regression.
const tolerance = 0.05

// goTestTimeout replaces go test's 10-minute per-package default for
// this tool's own suite run, matching what the Makefile's test and
// test-race targets pass. See GO_TEST_TIMEOUT in the Makefile for the
// arithmetic behind the value; the short version is that container
// startup timeouts are now explicit and internal/lock starts seven
// containers in one package, so 10 minutes is no longer enough headroom
// to surface a real "container failed to start" error instead of a
// goroutine-dump panic.
//
// It is a compile-time constant rather than a value read from the
// environment the Makefile could export. Reading it from the environment
// would put caller-controlled text into an exec.Command argument, which
// is a genuine taint (gosec G204/G702) and not one worth waiving to save
// a constant. TestGoTestTimeoutMatchesMakefile keeps the two copies
// honest, the same way internal/testsupport keeps docker-compose.yml and
// its image pins honest.
const goTestTimeout = "30m"

var (
	pkgPattern     = regexp.MustCompile(`github\.com/Subject-Void-LLC/the-pleiades/\S*`)
	percentPattern = regexp.MustCompile(`coverage:\s+([\d.]+)%`)
)

type floorFile struct {
	Floors   map[string]float64 `json:"floors"`
	Excluded map[string]string  `json:"excluded"`
}

func main() {
	tolerant := flag.Bool("tolerant", false, "warn instead of fail on a test failure confined to a flaky-packages.json package (used by push-gate, never by ci)")
	measured := flag.Bool("measured", false, "check floors against the coverage files named as arguments (testgate -full -coverage-out) instead of running the suite")
	flag.Parse()

	var files []string
	if *measured {
		files = flag.Args()
		if len(files) == 0 {
			fmt.Fprintln(os.Stderr, "coverage-check: -measured names no coverage files; a gate that measured nothing has nothing to check")
			os.Exit(1)
		}
	}
	if err := run(*tolerant, files); err != nil {
		fmt.Fprintln(os.Stderr, "coverage-check:", err)
		os.Exit(1)
	}
}

func run(tolerant bool, measuredFiles []string) error {
	floors, err := loadFloorFile("coverage-floor.json")
	if err != nil {
		return fmt.Errorf("loading coverage-floor.json: %w", err)
	}

	var measured map[string]float64
	switch {
	case len(measuredFiles) > 0:
		measured, err = readMeasured(measuredFiles)
	case tolerant:
		measured, err = measureCoverageTolerant()
	default:
		measured, err = measureCoverage()
	}
	if err != nil {
		return err
	}

	var regressions []string
	var newPackages []string
	seen := make(map[string]bool, len(measured))

	pkgs := make([]string, 0, len(measured))
	for pkg := range measured {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		pct := measured[pkg]
		seen[pkg] = true

		if _, excluded := floors.Excluded[pkg]; excluded {
			continue
		}
		floor, ok := floors.Floors[pkg]
		if !ok {
			newPackages = append(newPackages, fmt.Sprintf("%s: %.1f%% (no floor recorded yet)", pkg, pct))
			continue
		}
		if pct+tolerance < floor {
			regressions = append(regressions, fmt.Sprintf("%s: %.1f%% dropped below its floor of %.1f%%", pkg, pct, floor))
		}
	}

	if len(newPackages) > 0 {
		fmt.Println("coverage-check: packages with no recorded floor (informational, not a failure):")
		for _, n := range newPackages {
			fmt.Println("  " + n)
		}
	}

	// Only the -measured path can tell "not measured" from "has no
	// floor": it is handed one whole gate's numbers. A run that measures
	// itself stops at the first failing test instead (strict) or skips
	// the packages it could not read (the old tolerant path).
	if len(measuredFiles) > 0 {
		regressions = append(regressions, unmeasured(floors, measured)...)
	}

	if len(regressions) > 0 {
		fmt.Fprintln(os.Stderr, "coverage-check: regressions below the recorded floor:")
		for _, r := range regressions {
			fmt.Fprintln(os.Stderr, "  "+r)
		}
		return fmt.Errorf("%d package(s) regressed", len(regressions))
	}

	fmt.Printf("coverage-check: %d package(s) measured, none below their recorded floor\n", len(seen))
	return nil
}

func loadFloorFile(path string) (*floorFile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repo-relative path, not user input
	if err != nil {
		return nil, err
	}
	var f floorFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// measureCoverage runs the full suite with -cover and parses each
// package's reported percentage. A package go test reports with no
// numeric percentage at all (a test-only package with nothing to
// instrument, "[no statements]") is simply absent from the result, not
// reported as 0%, since coverage-floor.json's own "excluded" entries for
// those packages document why deliberately rather than this tool
// silently treating "nothing to measure" the same as "measured and
// found lacking."
func measureCoverage() (map[string]float64, error) {
	cmd := exec.Command("go", "test", "./...", "-cover", "-count=1", "-timeout", goTestTimeout)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Snapshot stdout before parsing it. Two things would otherwise
	// combine to throw away the only useful diagnostic when the suite
	// fails: go test reports test failures on stdout, not stderr, and the
	// scanner below reads directly from the buffer, consuming it. Reading
	// the copy leaves the original intact for the error built at the end.
	out := stdout.String()

	results, err := parseCoverageOutput(out)
	if err != nil {
		return nil, err
	}

	if runErr != nil {
		return nil, fmt.Errorf("go test ./... failed (a test failure, not a coverage question, must be fixed first): %v\n%s", runErr, failureOutput(out, stderr.String()))
	}
	return results, nil
}

// measureCoverageTolerant is measureCoverage's -tolerant counterpart: it
// runs the identical suite (-cover -count=1, same goTestTimeout) through
// flakegate.RunGoTestJSON instead of a bare exec.Command, so a test
// failure confined to a flaky-packages.json package is a warning rather
// than a reason to discard every coverage number the run produced. A
// package with a tolerated failure is not excluded from the returned map
// on that basis alone: go test still prints a "coverage: X% of
// statements" line for a package whose tests failed (verified directly
// against a real deliberately-failing package before relying on it), so
// the number parseCoverageOutput finds is real and worth keeping. Only a
// hard failure (a failure outside flaky-packages.json, or any build
// failure) still aborts the whole measurement, exactly like
// measureCoverage does for any failure at all.
func measureCoverageTolerant() (map[string]float64, error) {
	tolerated, err := flakegate.LoadTolerated(flakyPackagesPath)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", flakyPackagesPath, err)
	}

	args := []string{"-cover", "-count=1", "-timeout", goTestTimeout}
	events, waitErr := flakegate.RunGoTestJSON(args, os.Stdout)
	listed, warned := flakegate.Classify(events, tolerated)

	// The same isolation pass testgate applies, and applying it here is not
	// tidiness: without it the two tools reached OPPOSITE verdicts on one
	// failure inside a single `make push-gate`. A package that is not in
	// flaky-packages.json and lost a race was tolerated by testgate on the
	// evidence of a re-run, and then hard-failed here a few minutes later
	// because this function had only the list to go on. Two gates in one
	// run disagreeing about the same test is worse than either answer.
	failures := append(append([]flakegate.Failure{}, listed...), warned...)
	confirmed, contention, notRun, isoErr := flakegate.Isolate(failures, args, os.Stdout)
	if isoErr != nil {
		return nil, fmt.Errorf("re-running failures in isolation: %w", isoErr)
	}
	hard := append(append([]flakegate.Failure{}, confirmed...), notRun...)
	warned = contention

	var out strings.Builder
	for _, evt := range events {
		out.WriteString(evt.Output)
	}

	results, err := parseCoverageOutput(out.String())
	if err != nil {
		return nil, err
	}

	if len(warned) > 0 {
		fmt.Printf("coverage-check: %d test failure(s) passed when re-run alone, coverage still measured where a number was printed:\n", len(warned))
		for pkg := range flakegate.FailedPackages(warned) {
			fmt.Printf("  %s\n", pkg)
		}
	}

	if len(hard) > 0 {
		fmt.Fprintf(os.Stderr, "coverage-check: %d failure(s) failed again when re-run alone, could not be re-run, or did not compile; see output above:\n", len(hard))
		for _, f := range hard {
			if f.Test == "" {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Kind)
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Package, f.Test)
		}
		return nil, fmt.Errorf("%d failure(s) confirmed in isolation", len(hard))
	}

	if waitErr != nil && len(events) == 0 {
		return nil, fmt.Errorf("go test produced no output at all: %w", waitErr)
	}
	return results, nil
}

// parseCoverageOutput scans text (either a real go test invocation's raw
// stdout, or the reconstructed concatenation of every go test -json
// event's own Output field, which carries the identical human-readable
// lines go test always prints even in -json mode) for each package's own
// "coverage: X% of statements" line. A package go test reports with no
// numeric percentage at all (a test-only package with nothing to
// instrument, "[no statements]") is simply absent from the result, not
// reported as 0%, since coverage-floor.json's own "excluded" entries for
// those packages document why deliberately rather than this tool silently
// treating "nothing to measure" the same as "measured and found lacking."
func parseCoverageOutput(text string) (map[string]float64, error) {
	results := make(map[string]float64)
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "coverage:") {
			continue
		}
		pkg := pkgPattern.FindString(line)
		if pkg == "" {
			continue
		}
		m := percentPattern.FindStringSubmatch(line)
		if m == nil {
			continue // "[no statements]": nothing numeric to record
		}
		pct, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		results[pkg] = pct
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning go test output: %w", err)
	}
	return results, nil
}

// failureOutput assembles what someone reading a red build actually
// needs when `go test` itself failed rather than merely reporting low
// coverage: which test failed and why.
//
// It exists because reporting stderr alone (what this tool did until a
// flaky test made the gap obvious) reliably printed nothing at all. `go
// test` writes build errors to stderr but test failures -- the "--- FAIL"
// block, the assertion message, any panic and its stack -- to stdout, so
// the common case produced an "exit status 1" with no indication of which
// of a hundred packages had failed, and the only way forward was to
// re-run the whole suite by hand and hope the flake recurred.
//
// Only the per-package "ok" and "no test files" lines are dropped, since
// those are the one category that cannot carry a diagnostic. Everything
// else is passed through verbatim rather than matched against a set of
// known failure shapes: a filter that has to recognize a failure in order
// to show it is exactly the thing that fails on the unfamiliar failure,
// which is the one worth seeing.
func failureOutput(stdout, stderr string) string {
	var b strings.Builder
	if s := strings.TrimSpace(stderr); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "?   \t") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
