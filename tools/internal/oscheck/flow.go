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
// failed, 2 the machine could not be used or the run was stopped.
func (f Flow) Run(ctx context.Context, o *Options, stdout, stderr io.Writer, leg Leg) int {
	if len(o.Packages) == 0 {
		fmt.Fprintf(stdout, "%s: the branch changes no container-free package, so nothing runs on %s\n", f.Tool, f.Machine)
		return 0
	}
	sha, dirty, err := ResolveCommit(o.Commit)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", f.Tool, err)
		return 2
	}
	if err := leg.Reachable(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n%s\n", f.Tool, err, f.Advice)
		return 2
	}
	if o.Phase != PhaseTest {
		if err := leg.Ready(); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n%s\n", f.Tool, err, f.Advice)
			return 2
		}
		if dirty {
			fmt.Fprintf(stdout, "%s: the working tree has uncommitted changes; %s tests the committed tree of %s, without them\n", f.Tool, f.Machine, Short(sha))
		}
		fmt.Fprintf(stdout, "%s: %d package(s) at %s on %s\n", f.Tool, len(o.Packages), Short(sha), f.Target)
		if err := leg.Ship(sha); err != nil {
			fmt.Fprintf(stderr, "%s: sending the tree to %s: %v\n", f.Tool, f.Target, err)
			return 2
		}
		if o.Phase == PhasePrepare {
			if o.ReadyFile != "" {
				if err := os.WriteFile(o.ReadyFile, []byte(sha+"\n"), 0o600); err != nil {
					fmt.Fprintf(stderr, "%s: %v\n", f.Tool, err)
					return 2
				}
			}
			fmt.Fprintf(stdout, "%s: the tree of %s is on %s; -phase test runs its tests\n", f.Tool, Short(sha), f.Target)
			return 0
		}
	}
	start := time.Now()
	passed, err := leg.Test(ctx, sha, o.Parallel, o.Packages, stdout)
	took := time.Since(start).Round(time.Second)
	if err != nil && ctx.Err() != nil {
		// Stopped from this side: nothing about the machine needs changing.
		fmt.Fprintf(stderr, "%s: %v (after %s)\n", f.Tool, err, took)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: running the tests on %s: %v (after %s)\n%s\n", f.Tool, f.Target, err, took, f.Advice)
		return 2
	}
	if !passed {
		fmt.Fprintf(stderr, "%s: the tests failed on %s (%s) in %s\n", f.Tool, f.Target, f.OS, took)
		return 1
	}
	fmt.Fprintf(stdout, "%s: all tests passed on %s (%s) in %s\n", f.Tool, f.Target, f.OS, took)
	return 0
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
