//go:build windows

// Windows has no process groups a signal can reach, so an observed go test
// is stopped with taskkill /T, which ends the go command and every process
// under it: its test binaries, compiles and links. Ending the go command
// alone (TerminateProcess) would leave a test binary blocked on a wait
// running, with its memory, on the machine that could least spare it.
package flakegate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// ownGroup does nothing on Windows.
func ownGroup(*exec.Cmd) {}

// SignalGroup ends cmd and every process under it, whatever sig is: Windows
// delivers no signal to another process, so a stop is a kill of the tree,
// through taskkill /T /F, with the go command alone killed if taskkill
// itself cannot run.
func SignalGroup(cmd *exec.Cmd, _ os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("no process to signal")
	}
	out, err := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput() // #nosec G204 -- a pid this process started
	if err == nil {
		return nil
	}
	if kerr := cmd.Process.Kill(); kerr != nil {
		return fmt.Errorf("taskkill: %v: %s; and killing the go command: %v", err, out, kerr)
	}
	return nil
}
