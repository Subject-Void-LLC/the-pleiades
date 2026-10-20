// Command testgate runs the repository's tests through one policy for a
// failure. With -strict it fails on any failure. Without it, every failure
// is re-run alone (tools/internal/flakegate.Isolate): one that passes alone
// lost a race and is reported as contention, one that fails alone fails,
// and more distinct failures than flakegate.MaxIsolationRetries fail
// without a re-run. flaky-packages.json decides nothing; its reasons are
// printed beside a tolerated failure in a package it lists.
//
// Since Phase 118 it is part of every gate. `make ci` runs it with
// -strict, which re-runs nothing and fails on any failure; `make push-gate`
// and the pull-request CI jobs run it without, applying the re-run-alone
// rule above; and the nightly CI run is strict again, so what the tolerant
// runs forgive as contention is still counted somewhere. The receipt a
// local gate writes records which gate ran, so a pass tolerated here is
// never read as one `make ci` gave.
//
// # Tiers, shards and one pass instead of three (Phase 118)
//
// With -full it runs the one pass that replaced test-race,
// test-integration and coverage's own run: -race, the integration tag,
// coverage and a fresh run of every test, writing each package's coverage
// with -coverage-out for coverage-check -measured, so the suite runs once
// where it used to run three times. It takes an explicit package list, so
// the Makefile can hand it one tier (the container-free packages, or the
// container ones) and CI can divide a tier across machines with -shard.
// -strict turns the re-run-alone tolerance off: `make ci` and the nightly
// CI run judge that way. Every run ends by listing what it skipped, and on
// GitHub Actions writes that, the failures that failed the run and the
// tolerated ones to the job summary, each failure with its own output,
// whatever the outcome: a skipped gate and a passing one print the same
// nothing, and a job's log needs admin rights to read where its summary does
// not. What it prints and writes is masked through internal/redact first
// (mask.go). -race=false leaves the race detector out, for a machine that
// cannot build with it (tools/wincheck's Windows box).
//
// Usage: go run ./tools/testgate [-integration | -full | -repeat n] [-strict] [-race=false] [-p n]
// [-shard k/n] [-coverage-out file] [-list] [package ...]
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// goTestTimeout replaces go test's 10-minute per-package default, matching
// the Makefile's own GO_TEST_TIMEOUT. It exists twice for the identical
// reason tools/coverage-check's own goTestTimeout constant does (that
// file's own doc comment has the full reasoning): a Makefile cannot read a
// Go constant, and reading this one back out of the environment would put
// caller-controlled text into an exec.Command argument for no benefit.
// TestGoTestTimeoutMatchesMakefile keeps the two copies honest.
const goTestTimeout = "30m"

// flakyPackagesPath is the repo-relative path to the waiver file, a
// constant rather than a flag: this tool has exactly one caller (the
// Makefile's push-gate target), invoked from the repo root, the same
// assumption gosec-check's loadWaivers makes about gosec-waivers.json.
const flakyPackagesPath = "flaky-packages.json"

func main() {
	var o options
	flag.BoolVar(&o.integration, "integration", false, "run with -tags integration -count=1, matching the Makefile's test-integration target")
	flag.BoolVar(&o.full, "full", false, "the one pass: -race, -tags integration, -cover and -count=1")
	flag.BoolVar(&o.strict, "strict", false, "fail on any failure, with no re-run alone")
	race := flag.Bool("race", true, "use the race detector in the passes that do (-race=false where it cannot build, as on Windows with no C toolchain)")
	flag.IntVar(&o.repeat, "repeat", 0, "run every test this many times in one process, untagged and without -race (test-repeat)")
	flag.IntVar(&o.parallel, "p", 0, "how many packages go test runs at once (go test -p); 0 keeps its default")
	flag.StringVar(&o.shard, "shard", "", "k/n: test only this shard of the named packages")
	flag.StringVar(&o.coverageOut, "coverage-out", "", "write each package's coverage to this JSON file (needs -full)")
	flag.StringVar(&o.reportOut, "report-out", "", "write the run's record (verdict, failures with their output, skips, coverage) to this JSON file, whatever the outcome")
	list := flag.Bool("list", false, "print the packages this run would test, one per line, and test nothing")
	checkDurations := flag.Bool("durations-check", false, "hold the -coverage-out measurements named as arguments to durations.json: fail on a measured package it has no time for, warn on one whose time moved a lot")
	flag.Parse()
	o.packages = flag.Args()
	o.noRace = !*race

	// -durations-check keeps -shard balanced as packages are added and
	// their tests grow: a package durations.json lacks would be balanced as
	// the median, whatever it really takes.
	if *checkDurations {
		if err := durationsCheck(os.Stdout, flag.Args(), durations()); err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(1)
		}
		return
	}

	// -list answers "which packages does this shard own?" for a step that
	// needs the answer without running the tests, such as pulling the
	// images a shard starts.
	if *list {
		packages, err := o.selected()
		if err != nil {
			fmt.Fprintln(os.Stderr, "testgate:", err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(packages, "\n"))
		return
	}

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "testgate:", err)
		os.Exit(1)
	}
}

func run(o options) (err error) {
	// The record is written however the run ends, so a reader always has one
	// (see record.go).
	rec := &flakegate.Run{OS: runtime.GOOS, Arch: runtime.GOARCH, Packages: o.packages, Started: time.Now()}
	defer func() { err = finishRecord(o.reportOut, rec, err) }()

	if err := o.validate(); err != nil {
		return err
	}
	maskEnvironment()
	pieces, err := o.pieces()
	if err != nil {
		return err
	}
	packages := make([]string, 0, len(pieces))
	for _, p := range pieces {
		packages = append(packages, p.pkg)
	}
	rec.Packages = packages
	tolerated, err := flakegate.LoadTolerated(flakyPackagesPath)
	if err != nil {
		return fmt.Errorf("loading %s: %w", flakyPackagesPath, err)
	}
	args := o.goTestArgs()
	label := fmt.Sprintf("testgate %s", strings.Join(args, " "))
	if o.shard != "" {
		label += fmt.Sprintf(", shard %s (%d packages)", o.shard, len(packages))
	}
	rec.Label = label

	// Every raw JSON line is echoed to this process's own stdout as it is
	// read, so a slow package's progress is visible live rather than
	// silent until the whole run finishes.
	events, waitErr, err := runPieces(args, pieces)
	if err != nil {
		return err
	}
	argsFor := piecewise(args, pieces)
	// Built with cgo off (Windows with no C compiler), a test that opens a
	// SQLite database fails on go-sqlite3's stub before it checks anything:
	// a skip that needs cgo, never a pass and never a failure of the code.
	if cgoOff() {
		events = flakegate.CgoStubSkips(events)
	}
	skips := scrubSkips(flakegate.Skips(events))

	// Written before anything is judged, so a run that fails still leaves
	// the measurement it made; coverage-check never runs after a failed
	// gate step, so this cannot turn a failure into a pass.
	coverage := flakegate.Coverage(events)
	missing := flakegate.Missing(skips)
	rec.Skips, rec.Coverage, rec.Missing, rec.PackageSeconds = skips, coverage, missing, flakegate.Elapsed(events)
	if err := wholeCoverageOnly(pieces, coverage); err != nil {
		return err
	}
	measurement := func() flakegate.Measurement {
		return flakegate.Measurement{Coverage: coverage, Missing: missing, Seconds: flakegate.Elapsed(events),
			TestSeconds: flakegate.TestElapsed(events, divided(o.packages))}
	}
	if o.coverageOut != "" {
		if err := writeCoverage(o.coverageOut, measurement()); err != nil {
			return err
		}
	}

	listed, warned := flakegate.Classify(events, tolerated)
	if o.strict {
		failures := append(listed, warned...)
		summarize(label, skips, nil, failures, events)
		printOutput(events, failures)
		rec.Failures = flakegate.RunFailures(events, failures, flakegate.OutcomeFailed, failureLines, scrub)
		return judgeStrict(failures, waitErr, len(events))
	}

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
	confirmed, contention, notRun, err := flakegate.IsolateWith(failures, func(f flakegate.Failure) []string {
		if f.Test != "" {
			return args // the re-run names the test itself
		}
		return argsFor(f.Package)
	}, testOutput)
	if err != nil {
		return fmt.Errorf("re-running failures in isolation: %w", err)
	}

	// A package whose failure was contention has a coverage number from the
	// run where that test stopped partway, so fewer of its statements ran,
	// and a floor check would read contention as a coverage drop (it did:
	// internal/ent/migrate, 2026-09-30). Its real number comes from running
	// the whole package again, alone; a package that fails even then has
	// failed, and joins confirmed.
	if o.coverageOut != "" && len(contention) > 0 {
		failedAgain := remeasure(argsFor, contention, coverage)
		confirmed = append(confirmed, failedAgain...)
		rec.Coverage = coverage
		if err := writeCoverage(o.coverageOut, measurement()); err != nil {
			return err
		}
	}

	rec.Failures = flakegate.RunFailures(events, confirmed, flakegate.OutcomeFailed, failureLines, scrub)
	rec.Failures = append(rec.Failures, flakegate.RunFailures(events, notRun, flakegate.OutcomeNotRerun, failureLines, scrub)...)
	rec.Failures = append(rec.Failures, flakegate.RunFailures(events, contention, flakegate.OutcomeContention, contentionLines, scrub)...)

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
		// Why each one failed, since a real concurrency bug also fails
		// together and passes alone, and only its own output can tell the
		// two apart.
		for _, f := range contention {
			if out := flakegate.FailureOutput(events, f, contentionLines); out != "" {
				fmt.Printf("  why %s %s failed under load:\n%s\n\n", f.Package, f.Test, indent(scrub(out)))
			}
		}
	}

	if len(notRun) > 0 {
		summarize(label, skips, contention, notRun, events)
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
		summarize(label, skips, contention, confirmed, events)
		printOutput(events, confirmed)
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

	summarize(label, skips, contention, nil, events)
	if len(contention) == 0 {
		fmt.Println("testgate: all tests passed")
	} else {
		fmt.Println("testgate: passed (every failure above passed when re-run alone)")
	}
	return nil
}
