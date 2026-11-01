//go:build !windows

// The RULE 0 test that SIGTERM ends a real external program's method
// context (MainProgram's signal.NotifyContext) instead of killing the
// program. The loader sends SIGTERM when a task's own context ends, and the
// method must get to stop what it started on a device, a detached command
// above all, which outlives a killed program. Windows has no SIGTERM for a
// child, and the loader runs programs on Linux only.
package external_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestProgram_SIGTERMEndsTheMethodsContext: a method waiting on its context
// returns, and its answer reaches file descriptor 3, once the program is
// sent SIGTERM.
func TestProgram_SIGTERMEndsTheMethodsContext(t *testing.T) {
	bin := buildEchoProgram(t)
	req := echoRequest(t, "", "pw", "pw", map[string]any{"wait_for_cancel": true})

	// #nosec G204 -- bin is the path this test just built into its own
	// temporary directory, and the argument is the SDK's own command word.
	cmd := exec.Command(bin, external.CommandInvoke)
	cmd.Env = []string{}
	if raceEnabled {
		cmd.Env = []string{"GORACE=atexit_sleep_ms=0"}
	}
	cmd.Stdin = bytes.NewReader(req)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	responseRead, responseWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer responseRead.Close()
	cmd.ExtraFiles = []*os.File{responseWrite}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = responseWrite.Close() // the child holds its own copy
	respCh := make(chan wire.ChildResponse, 1)
	go func() {
		var resp wire.ChildResponse
		_ = json.NewDecoder(responseRead).Decode(&resp)
		respCh <- resp
	}()

	// The method says when it is waiting, so the signal lands inside it.
	waiting := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "waiting" {
				close(waiting)
				break
			}
		}
		for scanner.Scan() { // drain, so the child never blocks on stderr
		}
	}()
	select {
	case <-waiting:
	case <-time.After(childTimeout):
		_ = cmd.Process.Kill()
		t.Fatal("the method never started waiting")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("the program did not exit cleanly after SIGTERM (%v): the signal killed it instead of ending the method's context", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the program did not exit after SIGTERM")
	}
	resp := <-respCh
	if !strings.Contains(resp.Error, "stopped: context canceled") {
		t.Fatalf("response error = %q; want the method's own stop, from a context the SIGTERM ended", resp.Error)
	}
}
