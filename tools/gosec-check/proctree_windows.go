//go:build windows

// This file stops gosec on Windows, where gosec-check kills gosec alone: a
// `go list` gosec started is not found and may finish on its own. gosec-check
// has not been run on Windows with an interrupt or a time limit.
package main

import (
	"io"
	"os/exec"
)

// procTree is gosec's process, the only one stopped on Windows.
type procTree struct {
	cmd *exec.Cmd
}

// stopTree kills gosec, since Windows has no polite stop for another console
// process.
func stopTree(cmd *exec.Cmd, _ io.Writer) procTree {
	t := procTree{cmd: cmd}
	t.kill()
	return t
}

// kill kills gosec. A process that has already ended is not an error.
func (t procTree) kill() {
	_ = t.cmd.Process.Kill()
}
