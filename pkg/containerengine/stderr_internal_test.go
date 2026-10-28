// Tests of what a session's account check and its Podman service start put
// into an error when the device's program fails: the program's own stderr,
// written by a real shell over the in-process SSH harness, on one line,
// bounded and quoted by pkg/farside (Phase 131, R1).
package containerengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// onShellWith starts the SSH harness with each of programs, a name and its
// script, found first on the harness shell's PATH, and returns a login.
func onShellWith(t *testing.T, programs map[string]string) *remoteexec.Conn {
	t.Helper()
	need.POSIXShell(t)
	bin := t.TempDir()
	for name, script := range programs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil { // #nosec G306 -- a script this test runs
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	return conn
}

// holdsFarSide fails t unless err carries out as farside.Output quotes it,
// with no raw line break.
func holdsFarSide(t *testing.T, err error, out, says string) {
	t.Helper()
	if err == nil {
		t.Fatal("the failure was not an error")
	}
	if !strings.Contains(err.Error(), farside.Output(out)) || !strings.Contains(err.Error(), says) || strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("err = %q\nwant it to hold %s", err.Error(), says)
	}
}

// idNoName is what coreutils' id writes for a login whose user ID has no
// name in the account database (a container started with --user 1000).
const idNoName = "id: cannot find name for user ID 1000\n"

// TestRemoteUser_AFailedIdSaysWhatIdSaid: id's refusal reaches the error.
func TestRemoteUser_AFailedIdSaysWhatIdSaid(t *testing.T) {
	conn := onShellWith(t, map[string]string{"id": "#!/bin/sh\nprintf 'id: cannot find name for user ID 1000\\n' >&2\nexit 1\n"})
	_, err := remoteUser(context.Background(), conn)
	holdsFarSide(t, err, idNoName, "`id -un` exited 1: \"id: cannot find name for user ID 1000\"")
}

// podmanNoRuntimeDir is what a rootless Podman writes when its login has no
// runtime directory it may use: logrus warnings, then its error.
const podmanNoRuntimeDir = "time=\"2026-10-27T10:00:00Z\" level=warning msg=\"The cgroupv2 manager is set to systemd but there is no systemd user session available\"\n" +
	"time=\"2026-10-27T10:00:00Z\" level=warning msg=\"For using systemd, you may need to log in using a user session\"\n" +
	"Error: mkdir /run/user/1000: permission denied\n"

// TestStartService_AServiceThatDidNotListenSaysWhatPodmanSaid: a Podman
// service that exits before its socket appears is refused with Podman's
// own stderr, bounded and on one line. The harness's sleep returns at once,
// so the wait for the socket ends quickly.
func TestStartService_AServiceThatDidNotListenSaysWhatPodmanSaid(t *testing.T) {
	conn := onShellWith(t, map[string]string{
		"podman": "#!/bin/sh\ncat >&2 <<'EOF'\n" + podmanNoRuntimeDir + "EOF\nexit 125\n",
		"sleep":  "#!/bin/sh\nexit 0\n",
	})
	c := &stdioCarrier{conn: conn}
	_, err := c.startService(context.Background())
	holdsFarSide(t, err, podmanNoRuntimeDir, `did not listen: "time=\"2026-10-27T10:00:00Z\" level=warning msg=\"The cgroupv2 manager`)
	// The reason is Podman's last line, after its warnings; a first-line
	// cut would have kept only a warning.
	if err != nil && !strings.Contains(err.Error(), `Error: mkdir /run/user/1000: permission denied"`) {
		t.Errorf("Podman's reason is missing: %v", err)
	}
}
