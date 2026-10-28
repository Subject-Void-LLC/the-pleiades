// Tests that stopping a session's Podman service is bounded.
package containerengine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestStopService_IsBounded: the cleanup after a Podman service that did
// not listen ran on an unbounded context, so a stop that hung (an rm on a
// wedged filesystem) held the session forever. Both cleanups now share
// stopService and its bound. Here rm, found on the harness shell's PATH,
// hangs, and stopService must still return within its bound.
//
// The hanging rm outlives the bound, as a process on a real host would,
// and the harness's Close waits for it, so the test kills it first: left
// alone it made the test take the whole five seconds of the hang while
// stopService itself returned in its 300 ms (measured 2026-10-28), which
// read as an unbounded stop.
func TestStopService_IsBounded(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "rm.pid")
	script := "#!/bin/sh\necho $$ > '" + pidFile + "'\nexec sleep 5\n"
	if err := os.WriteFile(filepath.Join(bin, "rm"), []byte(script), 0o700); err != nil { // #nosec G306 -- a script this test runs
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	// Registered after srv.Close, so it runs before it.
	t.Cleanup(func() { killPID(t, pidFile) })
	auth, err := remoteexec.AuthFrom(srv.Username, srv.Password, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	defer func(old time.Duration) { serviceStopTimeout = old }(serviceStopTimeout)
	serviceStopTimeout = 300 * time.Millisecond
	start := time.Now()
	stopService(conn, t.TempDir())
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("stopService took %s with a hanging rm; want it held to its bound", took)
	}
}

// killPID kills the process whose ID the file at path holds, if it wrote
// one.
func killPID(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- a file this test wrote
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Errorf("the hanging rm wrote %q, not a process ID", b)
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
