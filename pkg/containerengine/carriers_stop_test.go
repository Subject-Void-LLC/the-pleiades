// Tests that stopping a session's Podman service is bounded.
package containerengine

import (
	"context"
	"os"
	"path/filepath"
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
func TestStopService_IsBounded(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "rm"), []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil { // #nosec G306 -- a script this test runs
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
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
