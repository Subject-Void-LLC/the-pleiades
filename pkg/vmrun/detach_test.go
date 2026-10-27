// Tests that a command which leaves a child holding its output, as vmrun's
// start leaves vmware-vmx, still returns through detachScript.
package vmrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// forker writes a stand-in vmrun that prints a line, leaves a child running
// with its standard output inherited (as vmware-vmx is), and exits with code.
func forker(t *testing.T, code string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh: the VMware host this script runs on is a Mac or Linux")
	}
	path := filepath.Join(t.TempDir(), "vmrun")
	body := "#!/bin/sh\necho \"Error: started and left a child\"\nsleep 30 &\nexit " + code + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
		t.Fatal(err)
	}
	return path
}

func TestDetachScript_ReturnsWhileTheChildStillHoldsItsOutput(t *testing.T) {
	for name, code := range map[string]int{"success": 0, "failure": 255} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			// Output reads through a pipe, as an SSH session's channel is read.
			out, err := exec.CommandContext(ctx, "/bin/sh", "-c", detachScript, "sh", forker(t, map[int]string{0: "0", 255: "255"}[code])).Output()
			if took := time.Since(start); took > 5*time.Second {
				t.Fatalf("took %s: the wrapper waited for the child", took)
			}
			got := 0
			if ee, ok := err.(*exec.ExitError); ok {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if got != code || !strings.Contains(string(out), "started and left a child") {
				t.Fatalf("exit %d output %q; want exit %d and the tool's own line", got, out, code)
			}
		})
	}
}

// The control: without the wrapper, reading the same command's output through a
// pipe does not return while the child lives. If this ever returns promptly the
// stand-in no longer reproduces the hazard and the test above proves nothing.
func TestDetachScript_TheHazardIsReal(t *testing.T) {
	// No context deadline: only WaitDelay ends the wait, after the process has
	// exited and its child still holds the pipe. A remoteexec session has no
	// such delay and would wait for the child itself.
	cmd := exec.Command(forker(t, "0"))
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	_, _ = cmd.Output()
	if took := time.Since(start); took < 1900*time.Millisecond {
		t.Fatalf("the bare command returned in %s, so the stand-in does not hold its output open", took)
	}
}
