// This file runs gosec so that stopping gosec-check stops gosec and, on
// Linux and macOS, what gosec started.
//
// gosec runs `go list`, and `go list -export` runs compilers. Each `go list`
// leads a process group of its own (golang.org/x/tools v0.52.0,
// internal/gocommand/invoke_unix.go, setProcessGroup), and gosec ends them
// only when it cancels its own work, which it never does when it is killed:
// it has no signal handler. So killing gosec, or any group gosec is in,
// leaves them running: a stopped gate once left 18 `go list` processes
// holding about 5 GB (FAILURE_PATTERNS 997 and 1337).
//
// gosec therefore runs in a process group of its own. A signal sent to
// gosec-check's group (a Ctrl-C, make, a watchdog) reaches gosec-check and
// not gosec, and gosec-check, on that signal or past the time limit, freezes
// gosec, lists what it started, ends gosec, and stops each process it
// started and each group they lead (proctree_unix.go). On Windows only gosec
// is stopped (proctree_windows.go). The stop path has run on Linux; it has not
// run on macOS or Windows.
//
// Limits. When ps cannot list the processes, only gosec is stopped, with a
// warning. If gosec-check is killed by SIGKILL, nothing of this runs: gosec,
// in its own group, keeps scanning until it next writes to the closed
// output, and every `go list` runs to its end. If gosec-check dies in the
// milliseconds between freezing gosec and ending it (one ps run), gosec stays
// frozen until someone ends it.
package main

import (
	"errors"
	"io"
	"os/exec"
	"time"
)

// stopGrace is how long what gosec started has to end after a polite stop
// before it is killed.
const stopGrace = 10 * time.Second

// pipeGrace bounds the wait for gosec's output to close once gosec has
// exited, in case a process it left holds it open (exec.Cmd.WaitDelay).
const pipeGrace = 2 * time.Second

// errStopped is returned when the run was interrupted.
var errStopped = errors.New("gosec was stopped because gosec-check was interrupted")

// errTimeLimit is returned when the run passed its time limit.
var errTimeLimit = errors.New("gosec was stopped because it ran past its time limit")

// runGroup starts cmd in a process group of its own and waits for it. When
// stop is closed, or expired fires (a nil channel never does), it ends cmd
// and stops what cmd started, politely first, and returns errStopped or
// errTimeLimit, writing to warn anything it could not do. Otherwise it
// returns what cmd.Wait returned.
func runGroup(cmd *exec.Cmd, stop <-chan struct{}, expired <-chan time.Time, warn io.Writer) error {
	inOwnGroup(cmd)
	cmd.WaitDelay = pipeGrace
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var why error
	select {
	case err := <-done:
		if errors.Is(err, exec.ErrWaitDelay) {
			// gosec exited cleanly, and a process outside it held its
			// output open past pipeGrace; the output gosec wrote is whole.
			return nil
		}
		return err
	case <-stop:
		why = errStopped
	case <-expired:
		why = errTimeLimit
	}

	// End gosec, ask what it started to end, give that stopGrace, then
	// kill whatever of it is still there.
	tree := stopTree(cmd, warn)
	tree.settle(stopGrace)
	tree.kill()
	<-done
	return why
}
