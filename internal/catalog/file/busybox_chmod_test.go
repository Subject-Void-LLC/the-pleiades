// Package file_test: the file methods against BusyBox's chmod, over real SSH
// to an Alpine sshd.
package file_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// The BusyBox half of FAILURE_PATTERNS 248. pkg/remotefile sends every
// chmod as five octal digits (00755) because GNU chmod keeps a directory's
// setuid and setgid bits for a four-digit mode, so "0755" never cleared
// them and a task reported changed forever. That fix was only ever run
// against GNU coreutils. These run the real file methods, over real SSH,
// against an Alpine sshd whose chmod is BusyBox's: the image ships GNU
// coreutils in /bin, so BusyBox's applet is put ahead of it on the
// session's PATH, and the test proves that is the chmod that ran.

const (
	busyboxSSHUser     = "pleiades"
	busyboxSSHPassword = "busybox-chmod-test"
	busyboxRoot        = "/config/busybox-chmod"
)

// busyboxTarget starts the sshd container with BusyBox's chmod first on
// the PATH and returns the harness-shaped server the file tests' device
// and context helpers take.
func busyboxTarget(t *testing.T) dirServer {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testsupport.SSHDImage,
			ExposedPorts: []string{"2222/tcp"},
			Env: map[string]string{
				"PUID": "1000", "PGID": "1000", "PASSWORD_ACCESS": "true",
				"USER_NAME": busyboxSSHUser, "USER_PASSWORD": busyboxSSHPassword,
			},
			WaitingFor: wait.ForLog("done.").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting the sshd container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	if code, _, err := container.Exec(ctx, []string{"ln", "-sf", "/bin/busybox", "/usr/local/bin/chmod"}); err != nil || code != 0 {
		t.Fatalf("putting BusyBox's chmod first: exit %d, %v", code, err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return dirServer{host: host, port: int(port.Num()), username: busyboxSSHUser, password: busyboxSSHPassword}
}

// runOn runs command on the target over its own SSH connection and
// returns the trimmed output.
func runOn(t *testing.T, server dirServer, command string) string {
	t.Helper()
	auth, err := remoteexec.AuthFrom(server.username, server.password, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: server.host, Port: server.port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = conn.Close() }()
	res, err := conn.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("running %q: %v", command, err)
	}
	return strings.TrimSpace(res.Stdout)
}

// TestBusyBoxChmod_FiveDigitModesMeanWhatTheySay runs file.permissions and
// file.directory, and their checks, against BusyBox's chmod: 00755 means
// 0755 and clears a directory's setgid, a changed directory converges so
// the next check and run report nothing to do, and a new directory and a
// file get exactly the mode asked for.
func TestBusyBoxChmod_FiveDigitModesMeanWhatTheySay(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container")
	}
	server := busyboxTarget(t)
	if got := runOn(t, server, `readlink -f "$(command -v chmod)"`); got != "/bin/busybox" {
		t.Fatalf("the session's chmod is %q, not BusyBox's, so nothing below would test BusyBox", got)
	}
	runOn(t, server, fmt.Sprintf("mkdir -p %[1]s && mkdir %[1]s/setgid && chmod 2755 %[1]s/setgid && touch %[1]s/file && chmod 0600 %[1]s/file", busyboxRoot))
	if got := runOn(t, server, "stat -c %a "+busyboxRoot+"/setgid"); got != "2755" {
		t.Fatalf("the setgid fixture is %s, want 2755", got)
	}

	device := newDirDevice(server)
	call := func(method collection.Method, path, mode string) collection.Result {
		t.Helper()
		params := dirParams(path)
		params["mode"] = mode
		res, err := method(context.Background(), newDirContext(server), device, params)
		if err != nil {
			t.Fatalf("%s mode %s: %v", path, mode, err)
		}
		return res
	}

	for _, tc := range []struct {
		name     string
		check    collection.Method
		run      collection.Method
		path     string
		mode     string
		wantMode string
	}{
		{"clearing a directory's setgid", file.CheckPermissions, file.Permissions, busyboxRoot + "/setgid", "0755", "755"},
		{"a file's mode", file.CheckPermissions, file.Permissions, busyboxRoot + "/file", "0640", "640"},
		{"a new directory", file.CheckDirectory, file.Directory, busyboxRoot + "/made", "0750", "750"},
		{"a new directory keeping setgid when asked", file.CheckDirectory, file.Directory, busyboxRoot + "/shared", "2750", "2750"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !call(tc.check, tc.path, tc.mode).Changed {
				t.Errorf("the check before the run predicts no change")
			}
			if !call(tc.run, tc.path, tc.mode).Changed {
				t.Errorf("the run reports no change")
			}
			if got := runOn(t, server, "stat -c %a "+tc.path); got != tc.wantMode {
				t.Errorf("the run left mode %s, want %s", got, tc.wantMode)
			}
			if call(tc.check, tc.path, tc.mode).Changed {
				t.Errorf("the check after the run still predicts a change, so a converged task would report changed forever")
			}
			if call(tc.run, tc.path, tc.mode).Changed {
				t.Errorf("a second run reports a change")
			}
		})
	}
}
