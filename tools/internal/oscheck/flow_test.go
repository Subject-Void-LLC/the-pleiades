// Tests for the flow every check shares, through a stand-in for the
// machine, and for reading a test run's exit status.
package oscheck

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLeg stands in for the machine, recording which steps ran.
type fakeLeg struct {
	reachable, ready, ship error
	passed                 bool
	testErr                error
	steps                  []string
	sha                    string
	parallel               int
	packages               []string
}

func (f *fakeLeg) Reachable() error { f.steps = append(f.steps, "reachable"); return f.reachable }
func (f *fakeLeg) Ready() error     { f.steps = append(f.steps, "ready"); return f.ready }
func (f *fakeLeg) Ship(sha string) error {
	f.steps = append(f.steps, "ship")
	f.sha = sha
	return f.ship
}
func (f *fakeLeg) Test(_ context.Context, sha string, parallel int, packages []string, _ io.Writer) (bool, error) {
	f.steps = append(f.steps, "test")
	f.sha, f.parallel, f.packages = sha, parallel, packages
	return f.passed, f.testErr
}

// options parses args with Flags, as a check's main does.
func options(t *testing.T, args ...string) *Options {
	t.Helper()
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	o := Flags(fs, "the box")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	o.Packages = fs.Args()
	return o
}

// flow is the words the flow tests read back.
var flow = Flow{Tool: "xcheck", Machine: "the box", OS: "BoxOS", Target: "box-1", Advice: "  To stop testing on a box: delete it"}

// TestCheck refuses a phase, package or commit that cannot run, before
// anything is contacted.
func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-phase", "later", "a/b"}, `not "later"`},
		{[]string{"a b; rm -rf /"}, "is not an import path"},
		{[]string{"--", "-leading/dash"}, "is not an import path"},
		{[]string{"-commit", "--output=x", "a/b"}, "which git reads as an option"},
		{[]string{"-phase", "vuln"}, `needs -govulncheck naming a release (v1.6.0), not ""`},
		{[]string{"-phase", "vuln", "-govulncheck", "latest"}, `not "latest"`},
		{[]string{"-phase", "vuln", "-govulncheck", "v1.6.0; rm -rf /"}, "needs -govulncheck naming a release"},
		{[]string{"-phase", "vuln", "-govulncheck", "v1.6.0", "-report-out", "x.json"}, "writes no record"},
		{[]string{"-phase", "test", "-govulncheck", "v1.6.0", "a/b"}, "-govulncheck is for -phase vuln"},
	} {
		err := options(t, tc.args...).Check()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	if err := options(t, "-phase", "test", "github.com/x/a", "github.com/x/b").Check(); err != nil {
		t.Errorf("a good run was refused: %v", err)
	}
	if err := options(t, "-phase", "vuln", "-govulncheck", "v1.6.0").Check(); err != nil {
		t.Errorf("a good scan was refused: %v", err)
	}
}

// fakeScanner is a machine that can also run govulncheck.
type fakeScanner struct {
	*fakeLeg
	clean   bool
	err     error
	version string
}

func (f *fakeScanner) Vuln(_ context.Context, sha, version string, _ io.Writer) (bool, error) {
	f.steps = append(f.steps, "vuln")
	f.sha, f.version = sha, version
	return f.clean, f.err
}

// TestRun_Vuln drives the vuln phase: it scans the tree prepare sent, with
// no packages named and no readiness check or sending of its own, and
// exits 2 for a machine that cannot scan or be used (which pr-check answers
// by scanning here), 1 for a scan that failed, and 0 for one that passed.
func TestRun_Vuln(t *testing.T) {
	inRepo(t)
	vuln := []string{"-phase", "vuln", "-govulncheck", "v1.6.0"}
	for _, tc := range []struct {
		name      string
		leg       Leg
		wantCode  int
		wantSteps string
		want      []string
	}{
		{"not a scanner", &fakeLeg{}, 2, "", []string{"the box cannot run govulncheck"}},
		{"unreachable", &fakeScanner{fakeLeg: &fakeLeg{reachable: errors.New("box-1 did not answer")}}, 2, "reachable", []string{"did not answer", "delete it"}},
		{"the scan breaks", &fakeScanner{fakeLeg: &fakeLeg{}, err: errors.New("lost")}, 2, "reachable vuln", []string{"running govulncheck on box-1: lost"}},
		{"it finds something", &fakeScanner{fakeLeg: &fakeLeg{}}, 1, "reachable vuln", []string{"govulncheck v1.6.0 failed on the tree of", "on box-1, in "}},
		{"it passes", &fakeScanner{fakeLeg: &fakeLeg{}, clean: true}, 0, "reachable vuln", []string{"govulncheck v1.6.0 passed on the tree of", "on box-1, in "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := flow.Run(context.Background(), options(t, vuln...), &stdout, &stderr, tc.leg)
			out := stdout.String() + stderr.String()
			var steps []string
			switch leg := tc.leg.(type) {
			case *fakeLeg:
				steps = leg.steps
			case *fakeScanner:
				steps = leg.steps
			}
			if code != tc.wantCode || strings.Join(steps, " ") != tc.wantSteps {
				t.Fatalf("exit %d after %v, want %d after %q\n%s", code, steps, tc.wantCode, tc.wantSteps, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output does not say %q:\n%s", w, out)
				}
			}
		})
	}
	leg := &fakeScanner{fakeLeg: &fakeLeg{}, clean: true}
	if code := flow.Run(context.Background(), options(t, vuln...), io.Discard, io.Discard, leg); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if sha, _, _ := ResolveCommit("HEAD"); leg.sha != sha || leg.version != "v1.6.0" {
		t.Errorf("the scan got %q and %q, want HEAD's full hash and v1.6.0", leg.sha, leg.version)
	}
}

// TestRun_Phases drives each phase and outcome: nothing to run, each step
// failing (the ones the machine causes end with the advice), a failure, a
// pass, prepare alone and test alone.
func TestRun_Phases(t *testing.T) {
	inRepo(t)
	pkgs := []string{"github.com/x/a"}
	for _, tc := range []struct {
		name      string
		args      []string
		leg       *fakeLeg
		wantCode  int
		wantSteps string
		want      []string
	}{
		{"no packages", nil, &fakeLeg{}, 0, "", []string{"changes no container-free package, so nothing runs on the box"}},
		{"not a commit", append([]string{"-commit", "nosuchref"}, pkgs...), &fakeLeg{}, 2, "", []string{`"nosuchref" is not a commit`}},
		{"unreachable", pkgs, &fakeLeg{reachable: errors.New("box-1 did not answer")}, 2, "reachable", []string{"did not answer", "delete it"}},
		{"not ready", pkgs, &fakeLeg{ready: errors.New("box-1 has no go")}, 2, "reachable ready", []string{"has no go", "delete it"}},
		{"the tree cannot be sent", pkgs, &fakeLeg{ship: errors.New("disk full")}, 2, "reachable ready ship", []string{"sending the tree to box-1: disk full"}},
		{"a test fails", pkgs, &fakeLeg{}, 1, "reachable ready ship test", []string{"the tests failed on box-1 (BoxOS) in "}},
		{"the run breaks", pkgs, &fakeLeg{testErr: errors.New("lost")}, 2, "reachable ready ship test", []string{"running the tests on box-1: lost", "delete it"}},
		{"all pass", pkgs, &fakeLeg{passed: true}, 0, "reachable ready ship test", []string{"1 package(s) at", "on box-1", "all tests passed on box-1 (BoxOS) in "}},
		{"prepare", append([]string{"-phase", "prepare"}, pkgs...), &fakeLeg{}, 0, "reachable ready ship", []string{"-phase test runs its tests"}},
		{"test", append([]string{"-phase", "test"}, pkgs...), &fakeLeg{passed: true}, 0, "reachable test", []string{"all tests passed on box-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := flow.Run(context.Background(), options(t, tc.args...), &stdout, &stderr, tc.leg)
			out := stdout.String() + stderr.String()
			if code != tc.wantCode || strings.Join(tc.leg.steps, " ") != tc.wantSteps {
				t.Fatalf("exit %d after %v, want %d after %q\n%s", code, tc.leg.steps, tc.wantCode, tc.wantSteps, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output does not say %q:\n%s", w, out)
				}
			}
		})
	}
}

// TestRun_HandsTheLegItsRun: Ship and Test get the commit's full hash, and
// Test the parallelism and packages asked for; a dirty tree is named, since
// its changes are not what runs.
func TestRun_HandsTheLegItsRun(t *testing.T) {
	dir := inRepo(t)
	writeFile(t, dir, "uncommitted.txt", "x")
	leg := &fakeLeg{passed: true}
	var stdout, stderr bytes.Buffer
	if code := flow.Run(context.Background(), options(t, "-p", "3", "github.com/x/a", "github.com/x/b"), &stdout, &stderr, leg); code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, &stdout, &stderr)
	}
	sha, _, _ := ResolveCommit("HEAD")
	if leg.sha != sha || leg.parallel != 3 || strings.Join(leg.packages, " ") != "github.com/x/a github.com/x/b" {
		t.Errorf("the leg ran %q, -p %d, %v", leg.sha, leg.parallel, leg.packages)
	}
	if !strings.Contains(stdout.String(), "uncommitted changes; the box tests the committed tree of "+Short(sha)) {
		t.Errorf("a dirty tree is not named:\n%s", &stdout)
	}
}

// TestRun_ReadyFileOnlyWhenTheTreeWasSent: prepare writes the ready file
// once the tree is on the machine, and not when nothing runs or sending
// failed, so a caller starts -phase test only when there is a tree to test.
func TestRun_ReadyFileOnlyWhenTheTreeWasSent(t *testing.T) {
	inRepo(t)
	ready := filepath.Join(t.TempDir(), "ready")
	var stdout, stderr bytes.Buffer
	run := func(leg *fakeLeg, args ...string) {
		t.Helper()
		flow.Run(context.Background(), options(t, append([]string{"-phase", "prepare", "-ready-file", ready}, args...)...), &stdout, &stderr, leg)
	}
	run(&fakeLeg{})
	run(&fakeLeg{ship: errors.New("refused")}, "github.com/x/a")
	if FileExists(ready) {
		t.Fatal("a ready file was written with nothing on the machine")
	}
	run(&fakeLeg{}, "github.com/x/a")
	if !FileExists(ready) {
		t.Fatalf("a tree sent wrote no ready file:\n%s%s", &stdout, &stderr)
	}
}

// TestRun_AnInterruptIsNotTheMachinesFault: tests stopped from this side
// end with the leg's word for it and no advice to change the machine.
func TestRun_AnInterruptIsNotTheMachinesFault(t *testing.T) {
	inRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	leg := &fakeLeg{testErr: errors.New("interrupted; the tests on box-1 were stopped")}
	var stdout, stderr bytes.Buffer
	code := flow.Run(ctx, options(t, "-phase", "test", "github.com/x/a"), &stdout, &stderr, leg)
	if code != 2 || !strings.Contains(stderr.String(), "were stopped") || strings.Contains(stderr.String(), "delete it") {
		t.Errorf("exit %d:\n%s", code, &stderr)
	}
}

// TestRunTests prints the run's output and passes on exit status 0 only,
// and stops the run on the machine when ctx ends first, saying whether
// that worked.
func TestRunTests(t *testing.T) {
	stopped := false
	stop := func(context.Context) error { stopped = true; return nil }
	answer := func(out Outcome, err error) func(context.Context) (Outcome, error) {
		return func(context.Context) (Outcome, error) { return out, err }
	}
	for _, tc := range []struct {
		name       string
		out        Outcome
		err        error
		wantPassed bool
		wantErr    string
		wantPrint  string
	}{
		{"passed", Outcome{Output: "ok a\n"}, nil, true, "", "ok a"},
		{"failed", Outcome{Output: "FAIL a\r\n", ExitCode: 1}, nil, false, "", "FAIL a"},
		{"the call failed", Outcome{}, errors.New("exec.command: refused"), false, "refused", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			passed, err := RunTests(context.Background(), "box-1", answer(tc.out, tc.err), stop, &stdout)
			if passed != tc.wantPassed || (tc.wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("passed %v, err %v", passed, err)
			}
			if !strings.Contains(stdout.String(), tc.wantPrint) {
				t.Errorf("printed %q", &stdout)
			}
		})
	}
	if stopped {
		t.Fatal("a run that ended by itself was stopped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunTests(ctx, "box-1", answer(Outcome{}, context.Canceled), stop, io.Discard); !stopped || err == nil || !strings.Contains(err.Error(), "interrupted; the tests on box-1 were stopped") {
		t.Fatalf("interrupted: stopped %v, %v", stopped, err)
	}
	failStop := func(context.Context) error { return errors.New("no answer") }
	if _, err := RunTests(ctx, "box-1", answer(Outcome{}, context.Canceled), failStop, io.Discard); err == nil || !strings.Contains(err.Error(), "could not be stopped: no answer") {
		t.Fatalf("a stop that failed: %v", err)
	}
}
