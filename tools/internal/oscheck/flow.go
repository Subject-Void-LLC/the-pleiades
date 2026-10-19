// Package oscheck is what the commands that run a branch's container-free
// packages on another operating system share (tools/maccheck,
// tools/bsdcheck and tools/wincheck): their settings, the commit they
// test, driving the machine through this branch's own pleiades binary, and
// the flow of a run in phases, from checking the machine to reading its
// tests' exit status. A Linux machine cannot show what another operating
// system lacks or does differently, and CI tests only Linux and macOS, and
// those only after a push.
package oscheck

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// The phases -phase names. prepare checks the machine, builds pleiades and
// sends the tree, all of which uses this machine; test runs the tests on
// the tree prepare sent, which uses only the other one, so pr-check can run
// it in the background beside the Linux passes. all is both.
const (
	PhaseAll     = "all"
	PhasePrepare = "prepare"
	PhaseTest    = "test"
)

// Options are the flags every check takes, registered by Flags.
type Options struct {
	// Optional makes a clone with no machine configured a skip rather than
	// an error, which is how pr-check calls each check.
	Optional bool
	// Commit is the commit whose tree is tested.
	Commit string
	// Parallel is how many packages testgate runs at once on the machine
	// (0: its default).
	Parallel int
	// Phase is PhaseAll, PhasePrepare or PhaseTest.
	Phase string
	// ReadyFile, with PhasePrepare, is written once the tree is on the
	// machine, so a caller knows PhaseTest has something to run.
	ReadyFile string
	// ReportOut, when set, is where the leg's record is written
	// (LegRecord), with the testgate records read back from the machine.
	ReportOut string
	// Packages are the import paths to test.
	Packages []string
}

// Flags registers the shared flags on fs; machine names the machine in
// their help ("the Mac").
func Flags(fs *flag.FlagSet, machine string) *Options {
	o := &Options{}
	fs.BoolVar(&o.Optional, "optional", false, "a clone with no machine configured is a skip rather than an error")
	fs.StringVar(&o.Commit, "commit", "HEAD", "the commit whose tree runs on "+machine)
	fs.IntVar(&o.Parallel, "p", 0, "how many packages testgate runs at once on "+machine+" (0: its default)")
	fs.StringVar(&o.Phase, "phase", PhaseAll, "prepare (check "+machine+", build pleiades, send the tree), test (run the tests on the tree prepare sent), or all")
	fs.StringVar(&o.ReadyFile, "ready-file", "", "with -phase prepare, a file written once the tree is on "+machine+", so a caller knows -phase test has something to run")
	fs.StringVar(&o.ReportOut, "report-out", "", "write the run's record to this JSON file: where it stopped, and the tests' own records read back from "+machine)
	return o
}

// importPath is what a package argument must look like, since each is
// written into the command the machine runs.
var importPath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Check refuses a phase, package or commit that cannot be run, before
// anything is read or contacted.
func (o *Options) Check() error {
	if o.Phase != PhaseAll && o.Phase != PhasePrepare && o.Phase != PhaseTest {
		return fmt.Errorf("-phase is %s, %s or %s, not %q", PhaseAll, PhasePrepare, PhaseTest, o.Phase)
	}
	for _, p := range o.Packages {
		if !importPath.MatchString(p) {
			return fmt.Errorf("%q is not an import path", p)
		}
	}
	if strings.HasPrefix(o.Commit, "-") {
		return fmt.Errorf("%q is not a commit: it begins with \"-\", which git reads as an option", o.Commit)
	}
	return nil
}

// Leg is the machine a check runs on, apart from Flow so tests can stand
// in for it.
type Leg interface {
	// Reachable answers quickly whether the machine is up at all.
	Reachable() error
	// Ready checks the machine can run the tests, preparing what the check
	// needs here first (building pleiades, a private project).
	Ready() error
	// Ship unpacks commit sha's tree on the machine.
	Ship(sha string) error
	// Test runs the packages on sha's tree and reports whether they passed,
	// writing their output to stdout. When ctx ends first it stops the run
	// on the machine.
	Test(ctx context.Context, sha string, parallel int, packages []string, stdout io.Writer) (bool, error)
}

// Flow is one check's words for its machine, and Run is its run.
type Flow struct {
	// Tool names the command, starting each line it prints ("maccheck").
	Tool string
	// Machine is how a line names the kind of machine ("the Mac").
	Machine string
	// OS names its operating system ("macOS").
	OS string
	// Target names this machine ("me@mac", "win-check").
	Target string
	// Advice ends every refusal the machine causes: how to point the check
	// at another one, and how to stop using one.
	Advice string
}

// Run runs o's phases against leg: 0 passed (or nothing to run), 1 a test
// failed, 2 the machine could not be used or the run was stopped. With
// o.ReportOut set it writes the leg's record there (LegRecord), except
// after a prepare phase that succeeded, whose test phase writes it.
func (f Flow) Run(ctx context.Context, o *Options, stdout, stderr io.Writer, leg Leg) int {
	rec := f.record(o)
	code, write := f.run(ctx, o, stdout, stderr, leg, &rec)
	if write {
		return f.writeRecord(o, rec, code, stderr)
	}
	return code
}

// record starts the leg's record for o's run.
func (f Flow) record(o *Options) LegRecord {
	return LegRecord{Tool: f.Tool, OS: f.OS, Target: f.Target, Packages: o.Packages, Started: time.Now()}
}

// writeRecord writes rec to o.ReportOut, when one is set, and returns the
// run's code, or 2 for a run that passed and whose record could not be
// written, since a caller that asked for one must not read a pass from a
// record that is missing.
func (f Flow) writeRecord(o *Options, rec LegRecord, code int, stderr io.Writer) int {
	if o.ReportOut == "" {
		return code
	}
	rec.Seconds = time.Since(rec.Started).Round(time.Second).Seconds()
	if err := WriteLegRecord(o.ReportOut, rec); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", f.Tool, err)
		if code == 0 {
			return 2
		}
	}
	return code
}

// stopped marks rec as stopped at step with err, its tests never run.
func stopped(rec *LegRecord, step string, err error) {
	rec.Verdict, rec.Step, rec.Error = flakegate.VerdictNotRun, step, err.Error()
}

// run is Run's phases, marking rec as it goes; write is false for a prepare
// phase that succeeded.
func (f Flow) run(ctx context.Context, o *Options, stdout, stderr io.Writer, leg Leg, rec *LegRecord) (code int, write bool) {
	if len(o.Packages) == 0 {
		fmt.Fprintf(stdout, "%s: the branch changes no container-free package, so nothing runs on %s\n", f.Tool, f.Machine)
		rec.Verdict = flakegate.VerdictPass
		return 0, true
	}
	sha, dirty, err := ResolveCommit(o.Commit)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", f.Tool, err)
		stopped(rec, StepCommit, err)
		return 2, true
	}
	rec.Commit = sha
	if err := leg.Reachable(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n%s\n", f.Tool, err, f.Advice)
		stopped(rec, StepReachable, err)
		return 2, true
	}
	if o.Phase != PhaseTest {
		if err := leg.Ready(); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n%s\n", f.Tool, err, f.Advice)
			stopped(rec, StepReady, err)
			return 2, true
		}
		if dirty {
			fmt.Fprintf(stdout, "%s: the working tree has uncommitted changes; %s tests the committed tree of %s, without them\n", f.Tool, f.Machine, Short(sha))
		}
		fmt.Fprintf(stdout, "%s: %d package(s) at %s on %s\n", f.Tool, len(o.Packages), Short(sha), f.Target)
		if err := leg.Ship(sha); err != nil {
			fmt.Fprintf(stderr, "%s: sending the tree to %s: %v\n", f.Tool, f.Target, err)
			stopped(rec, StepShip, err)
			return 2, true
		}
		if o.Phase == PhasePrepare {
			if o.ReadyFile != "" {
				if err := os.WriteFile(o.ReadyFile, []byte(sha+"\n"), 0o600); err != nil {
					fmt.Fprintf(stderr, "%s: %v\n", f.Tool, err)
					stopped(rec, StepShip, err)
					return 2, true
				}
			}
			fmt.Fprintf(stdout, "%s: the tree of %s is on %s; -phase test runs its tests\n", f.Tool, Short(sha), f.Target)
			return 0, false
		}
	}
	start := time.Now()
	passed, err := leg.Test(ctx, sha, o.Parallel, o.Packages, stdout)
	took := time.Since(start).Round(time.Second)
	if err != nil && ctx.Err() != nil {
		// Stopped from this side: nothing about the machine needs changing.
		fmt.Fprintf(stderr, "%s: %v (after %s)\n", f.Tool, err, took)
		rec.Verdict, rec.Step, rec.Error = flakegate.VerdictError, StepTest, err.Error()
		return 2, true
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: running the tests on %s: %v (after %s)\n%s\n", f.Tool, f.Target, err, took, f.Advice)
		rec.Verdict, rec.Step, rec.Error = flakegate.VerdictError, StepTest, err.Error()
		return 2, true
	}
	f.readRecords(leg, sha, rec, stderr)
	if !passed {
		fmt.Fprintf(stderr, "%s: the tests failed on %s (%s) in %s\n", f.Tool, f.Target, f.OS, took)
		rec.Verdict, rec.Step = flakegate.VerdictFail, StepTest
		return 1, true
	}
	fmt.Fprintf(stdout, "%s: all tests passed on %s (%s) in %s\n", f.Tool, f.Target, f.OS, took)
	rec.Verdict = flakegate.VerdictPass
	return 0, true
}

// RecordsGrace bounds reading a machine's records back after its tests.
const RecordsGrace = 2 * time.Minute

// readRecords reads the testgate records the machine wrote into rec, when
// leg can and a record was asked for. A record that cannot be read is said
// in rec.Error rather than failing a run the tests decided: the text log
// still holds what they printed.
func (f Flow) readRecords(leg Leg, sha string, rec *LegRecord, stderr io.Writer) {
	r, ok := leg.(Recorder)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RecordsGrace)
	defer cancel()
	runs, err := r.Records(ctx, sha)
	rec.Runs = runs
	if err != nil {
		fmt.Fprintf(stderr, "%s: reading the tests' records back from %s: %v\n", f.Tool, f.Target, err)
		rec.Error = "reading the tests' records back: " + err.Error()
	}
}

// StopGrace bounds stopping the tests on the machine after an interrupt,
// so a machine that stopped answering cannot hold the interrupted run open.
const StopGrace = time.Minute

// RunTests runs a test script with run, printing its output to stdout,
// and reports whether it exited 0. When ctx ends first, the local pleiades
// is gone and the tests would run on without it, so stop ends them on the
// machine, with a context of its own bounded by StopGrace.
func RunTests(ctx context.Context, target string, run func(context.Context) (Outcome, error), stop func(context.Context) error, stdout io.Writer) (bool, error) {
	out, err := run(ctx)
	if ctx.Err() != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), StopGrace)
		defer cancel()
		if stopErr := stop(stopCtx); stopErr != nil {
			return false, fmt.Errorf("interrupted, and the tests on %s could not be stopped: %v", target, stopErr)
		}
		return false, fmt.Errorf("interrupted; the tests on %s were stopped", target)
	}
	if err != nil {
		return false, err
	}
	fmt.Fprintln(stdout, strings.TrimRight(out.Output, "\r\n"))
	return out.ExitCode == 0, nil
}
