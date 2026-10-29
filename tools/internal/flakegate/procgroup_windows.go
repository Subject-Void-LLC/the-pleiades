//go:build windows

// Windows has no process groups a signal can reach, so an observed go test
// is stopped by ending the go command itself; its test binaries end when
// their output pipe does, which go test closes as it goes.
package flakegate

import (
	"errors"
	"os"
	"os/exec"
)

// ownGroup does nothing on Windows.
func ownGroup(*exec.Cmd) {}

// SignalGroup ends cmd, whatever sig is: Windows delivers no signal to
// another process, so a stop is a kill.
func SignalGroup(cmd *exec.Cmd, _ os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("no process to signal")
	}
	return cmd.Process.Kill()
}
