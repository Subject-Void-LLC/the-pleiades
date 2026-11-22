// This file runs gosec so that stopping gosec-check stops everything gosec
// started.
//
// gosec runs `go list`, and `go list -export` runs compilers. Each `go list`
// leads a process group of its own (golang.org/x/tools v0.52.0,
// internal/gocommand/invoke_unix.go, setProcessGroup), and gosec ends them
// only when it cancels its own work, which it does not do when it is killed.
// So killing gosec, or gosec's process group, leaves them running: a stopped
// gate once left 18 `go list` processes holding about 5 GB (FAILURE_PATTERNS
// 997 and 1337). On an interrupt or past the time limit gosec-check therefore
// freezes gosec, so it starts nothing more, finds every process gosec started,
// and stops each one and each process group they lead (proctree_unix.go).
//
// gosec stays in gosec-check's own process group, as it always was, so a
// signal sent to that group still reaches it. What no program can stop is
// SIGKILL to gosec-check itself: then gosec, and every `go list` it started,
// is left running, as before.
package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// stopGrace is how long gosec and what it started have to end after a polite
// stop before they are killed.
const stopGrace = 10 * time.Second

// pipeGrace bounds the wait for gosec's output to close once gosec has
// exited, in case a process it left holds it open (exec.Cmd.WaitDelay).
const pipeGrace = 2 * time.Second

// errStopped is returned when the run was interrupted.
var errStopped = errors.New("gosec was stopped because gosec-check was interrupted")

// errTimeLimit is wrapped by the error returned when the run passed its
// deadline.
var errTimeLimit = errors.New("gosec was stopped because it ran past its time limit")

// runGroup starts cmd and waits for it. When stop is closed, or deadline
// passes (a zero deadline never does), it stops cmd and every process cmd
// started, politely first, and returns errStopped or a time limit error,
// writing to warn anything it could not do. Otherwise it returns what
// cmd.Wait returned.
func runGroup(cmd *exec.Cmd, stop <-chan struct{}, deadline time.Time, warn io.Writer) error {
	cmd.WaitDelay = pipeGrace
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// A nil channel never fires, which is what no deadline means.
	var expired <-chan time.Time
	if !deadline.IsZero() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		expired = timer.C
	}

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
		why = fmt.Errorf("%w, %s", errTimeLimit, deadline.Format(time.RFC3339))
	}

	// Ask everything to end, then kill what has not: gosec, when it has not
	// ended within stopGrace, and in any case what it started, which may
	// outlast it.
	tree := stopTree(cmd, warn)
	select {
	case <-done:
		tree.kill()
	case <-time.After(stopGrace):
		tree.kill()
		<-done
	}
	return why
}
