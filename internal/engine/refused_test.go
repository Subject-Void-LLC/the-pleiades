// Tests that a task stops sending a login its device refused: an until task
// is not tried again, and a loop does not run its later items, whichever
// transport refused and whether the refusal crossed a process boundary. Two
// of them refuse through a real SSH server and count the passwords it was
// sent, where a lockout policy counts them.
package engine_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// refusals are the failures every transport reports for a refused login or
// identity, each wrapped the way a method returns it.
var refusals = map[string]error{
	"an SSH password": fmt.Errorf("exec.command: device %q: %w", "a", &remoteexec.DialError{Addr: "a:22",
		Err: errors.New("ssh handshake: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain")}),
	"an SSH host key": fmt.Errorf("exec.command: %w", &remoteexec.DialError{Addr: "a:22", Err: &knownhosts.KeyError{}}),
	"a WinRM credential": fmt.Errorf("win.feature: %w", &winrmexec.NotStartedError{
		Err: &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}}),
	"a Telnet login":                  fmt.Errorf("net.cli.command: %w at a:23", telnetexec.ErrLoginRefused),
	"one that crossed from its child": wire.ChildResponse{Error: "exec.command: unable to authenticate", Refused: true}.Failure("exec.command: unable to authenticate"),
}

// failingWith is a method that always fails with err.
func failingWith(err error) func(int, *engine.Task) (engine.ActionResult, error) {
	return func(int, *engine.Task) (engine.ActionResult, error) { return engine.ActionResult{}, err }
}

// failureText is the first failure of a run's results.
func failureText(t *testing.T, result engine.RunResult) string {
	t.Helper()
	for _, n := range result.Nodes {
		if n.Err != nil {
			return n.Err.Error()
		}
	}
	t.Fatal("the run has no failure")
	return ""
}

// TestUntil_ARefusedLoginIsNotTriedAgain proves an until task stops at a
// run whose login was refused, whichever transport refused it, and says why
// it stopped. It used to run all six times retries: 5 allows, sending the
// refused login each time.
func TestUntil_ARefusedLoginIsNotTriedAgain(t *testing.T) {
	for name, err := range refusals {
		t.Run(name, func(t *testing.T) {
			if !engine.Refused(err) || engine.Unreached(err) {
				t.Fatalf("Refused = %v, Unreached = %v; want a refusal, not an unreached device", engine.Refused(err), engine.Unreached(err))
			}
			s, result, _ := runStepper(t, collection.ModeExecute, `id: r
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.ok == true"]
    retries: 5
    delay: 0
`, failingWith(err))
			if len(s.calls) != 1 {
				t.Fatalf("the method ran %d times, want once: the refused login was sent again", len(s.calls))
			}
			if !result.HasErrors() {
				t.Fatal("the refused run did not fail the task")
			}
			if text := failureText(t, result); !strings.Contains(text, "not tried again for until (5 more tries were allowed)") {
				t.Errorf("the failure %q does not say why the task was not tried again", text)
			}
		})
	}
}

// TestUntil_AnUnreachedDeviceIsStillTriedAgain proves only a refusal stops
// the tries: a device that did not answer may answer next time.
func TestUntil_AnUnreachedDeviceIsStillTriedAgain(t *testing.T) {
	unreached := &remoteexec.DialError{Addr: "a:22", Err: errors.New("tcp dial: connection refused")}
	s, _, _ := runStepper(t, collection.ModeExecute, `id: u
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.ok == true"]
    retries: 2
    delay: 0
`, failingWith(unreached))
	if len(s.calls) != 3 {
		t.Fatalf("the method ran %d times, want 3: an unreached device is tried again", len(s.calls))
	}
}

// TestLoop_ARefusedLoginEndsTheLoop proves a loop runs no item after one
// whose login was refused, and says how many it did not run. Every item
// used to run, each sending the refused login.
func TestLoop_ARefusedLoginEndsTheLoop(t *testing.T) {
	for _, tc := range []struct {
		items, note string
		calls       int
	}{
		{"[a, b, c, d]", "the loop's 3 later items were not run", 1},
		{"[a, b]", "the loop's last item was not run", 1},
	} {
		t.Run(tc.items, func(t *testing.T) {
			s, result, _ := runStepper(t, collection.ModeExecute, `id: l
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: `+tc.items+`
    register: r
`, failingWith(refusals["an SSH password"]))
			if len(s.calls) != tc.calls {
				t.Fatalf("the method ran %d times (%v), want %d", len(s.calls), s.calls, tc.calls)
			}
			if text := failureText(t, result); !strings.Contains(text, tc.note) {
				t.Errorf("the failure %q does not say %q", text, tc.note)
			}
		})
	}
}

// TestLoop_UntilInsideALoopStopsBoth proves a looped until task refused on
// its first item runs once in all, neither trying again nor moving on.
func TestLoop_UntilInsideALoopStopsBoth(t *testing.T) {
	s, _, _ := runStepper(t, collection.ModeExecute, `id: lu
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: [a, b, c]
    register: r
    until: ["result.r.ok == true"]
    retries: 3
    delay: 0
`, failingWith(refusals["a WinRM credential"]))
	if len(s.calls) != 1 {
		t.Fatalf("the method ran %d times (%v), want once", len(s.calls), s.calls)
	}
}

// TestLoop_AnOrdinaryFailureDoesNotEndTheLoop proves the loop's own rule
// stands for every other failure: each item runs.
func TestLoop_AnOrdinaryFailureDoesNotEndTheLoop(t *testing.T) {
	s, _, _ := runStepper(t, collection.ModeExecute, `id: lo
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: [a, b, c]
`, failingWith(errors.New("command exited 1")))
	if len(s.calls) != 3 {
		t.Fatalf("the method ran %d times, want 3: an ordinary failure does not end a loop", len(s.calls))
	}
}

// TestRefusedLogin_ARealServerIsSentItOnce runs an until task and a loop
// whose method logs in to a real SSH server with a password it refuses,
// and counts the passwords the server was sent: one per task, where the
// tries and the items used to send four and three.
func TestRefusedLogin_ARealServerIsSentItOnce(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	login := func(int, *engine.Task) (engine.ActionResult, error) {
		conn, err := runner.Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, remoteexec.PasswordAuth(srv.Username, "wrong"))
		if err != nil {
			return engine.ActionResult{}, fmt.Errorf("rbtest.step: %w", err)
		}
		_ = conn.Close()
		return engine.ActionResult{}, nil
	}
	for name, runbook := range map[string]string{
		"until": "    register: r\n    until: [\"result.r.ok == true\"]\n    retries: 3\n    delay: 0\n",
		"loop":  "    loop: [a, b, c]\n",
	} {
		t.Run(name, func(t *testing.T) {
			before := srv.Refusals()
			_, result, _ := runStepper(t, collection.ModeExecute, "id: real\nhosts: web\ntasks:\n  - rbtest.step:\n      note: x\n"+runbook, login)
			if !result.HasErrors() {
				t.Fatal("a refused login did not fail the task")
			}
			if got := srv.Refusals() - before; got != 1 {
				t.Fatalf("the server refused %d passwords for one task, want 1", got)
			}
		})
	}
}

// TestRunResult_SawRefusal proves a run reports a refusal among its tasks'
// runs, whichever transport refused and whether a rescue or ignore_errors
// answered it, since a rerun of the whole runbook would send that login
// again; and reports none for a run whose failures are all ordinary.
func TestRunResult_SawRefusal(t *testing.T) {
	ordinary := engine.NodeResult{Err: errors.New("command exited 1")}
	for name, err := range refusals {
		for how, n := range map[string]engine.NodeResult{
			"unanswered": {Err: err},
			"ignored":    {Err: err, Ignored: true},
			"rescued":    {Err: err, RescuedBy: "recover"},
		} {
			if !(engine.RunResult{Nodes: []engine.NodeResult{ordinary, n, {}}}).SawRefusal() {
				t.Errorf("%s, %s: a run with a refused login did not report it", name, how)
			}
		}
	}
	if (engine.RunResult{Nodes: []engine.NodeResult{ordinary, {}}}).SawRefusal() {
		t.Error("a run whose only failure was ordinary reported a refusal")
	}
	if (engine.RunResult{}).SawRefusal() {
		t.Error("an empty run reported a refusal")
	}
}
