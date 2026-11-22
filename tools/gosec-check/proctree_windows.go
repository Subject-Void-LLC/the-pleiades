//go:build windows

// This file stops gosec on Windows, where gosec-check kills gosec alone: a
// `go list` gosec started is not found and runs to its end. This has not run
// on Windows.
package main

import (
	"io"
	"os/exec"
	"time"
)

// inOwnGroup does nothing on Windows, which has no process group a signal
// reaches.
func inOwnGroup(*exec.Cmd) {}

// procTree is nothing on Windows: no process but gosec is stopped.
type procTree struct{}

// stopTree kills gosec, since Windows has no polite stop for another console
// process. A process that has already ended is not an error.
func stopTree(cmd *exec.Cmd, _ io.Writer) procTree {
	_ = cmd.Process.Kill()
	return procTree{}
}

// settle has nothing to wait for on Windows.
func (procTree) settle(time.Duration) {}

// kill has nothing more to kill on Windows.
func (procTree) kill() {}
