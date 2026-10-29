//go:build unix

// Process groups, where the system has them: an observed go test starts in
// one of its own, so a signal to the group reaches every process under it.
package flakegate

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// OwnGroup makes cmd start in a process group of its own, so a signal to
// the group that started it (a terminal's interrupt, a gate's stop) does
// not reach it; its starter ends it through its context instead.
func OwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// SignalGroup sends sig to cmd's whole process group (go test and every
// process under it: test binaries, compiles, links) when cmd was started in
// one by an observed run, else to cmd alone. A command that has not started
// or has ended is an error.
func SignalGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("no process to signal")
	}
	s, ok := sig.(syscall.Signal)
	if ok && cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		return syscall.Kill(-cmd.Process.Pid, s)
	}
	return cmd.Process.Signal(sig)
}
