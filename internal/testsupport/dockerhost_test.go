// Tests for the engine fixtures themselves: the SSH channel reaches the real
// engine, and each way sshd declines it is reported.
package testsupport

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// These tests are the RULE 0 evidence for remoteexec.Conn.DialUnix against
// a real OpenSSH server in front of a real Docker engine (Phase 119a): the
// channel a container host's engine is reached through, and every answer
// sshd gives when it will not open one. They live here, beside the fixture,
// as sshd_test.go's SFTP evidence does beside StartSSHD.

// dialFixture logs in to d as user with host keys verified for real.
func dialFixture(t testing.TB, ctx context.Context, d *EngineHost, user string) *remoteexec.Conn {
	t.Helper()
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("KnownHosts: %v", err)
	}
	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: knownHosts})
	conn, err := runner.Connect(ctx, nil, remoteexec.Target{Host: d.Host, Port: d.Port}, remoteexec.PasswordAuth(user, DockerHostPassword))
	if err != nil {
		t.Fatalf("Connect as %s: %v", user, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// engineDialer reaches the fixture's engine socket through conn.
func engineDialer(conn *remoteexec.Conn) containerengine.Dialer {
	return func(ctx context.Context) (net.Conn, error) { return conn.DialUnix(ctx, DockerHostSocket) }
}

// TestDockerHost_TheChannelReachesTheEngine proves a channel to the
// engine's socket, on an ordinary login, reaches the real engine: the
// identity read through it is the one the engine reports from inside.
func TestDockerHost_TheChannelReachesTheEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	d := StartDockerHost(t, EngineHostOptions{})
	conn := dialFixture(t, ctx, d, DockerHostUser)

	info, err := containerengine.ReadIdentity(ctx, engineDialer(conn), capability.EngineDocker)
	if err != nil {
		t.Fatalf("ReadIdentity through the channel: %v", err)
	}
	inside, err := d.RootExec(ctx, "docker", "info", "--format", "{{.ID}}")
	if err != nil {
		t.Fatalf("docker info inside the fixture: %v", err)
	}
	if info.ID == "" || string(info.ID) != strings.TrimSpace(inside) {
		t.Errorf("identity through the channel = %q, the engine says %q", info.ID, strings.TrimSpace(inside))
	}
}

// TestDockerHost_SSHDRefusals proves each way sshd declines the channel is
// reported with a cause an operator can act on, and that a refusal leaves
// the login usable, since the server answered rather than broke.
func TestDockerHost_SSHDRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	for _, tc := range []struct {
		name, config, user, want string
	}{
		// OpenSSH answers every one of these "connect failed", so the error
		// lists every cause; each case checks its own is among them, and
		// that the answer is the same one (the client cannot tell them
		// apart, only sshd's log can).
		{"AllowTcpForwarding no", "AllowTcpForwarding no\n", DockerHostUser, "AllowTcpForwarding no"},
		{"DisableForwarding yes", "DisableForwarding yes\n", DockerHostUser, "DisableForwarding yes"},
		{"AllowStreamLocalForwarding no", "AllowTcpForwarding local\nAllowStreamLocalForwarding no\n", DockerHostUser, "AllowStreamLocalForwarding no"},
		// The socket is root:docker 0660, and this login is not in docker.
		{"a login outside the docker group", "", DockerHostOutsider, "the login user may not open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			d := StartDockerHost(t, EngineHostOptions{SSHDConfig: tc.config})
			conn := dialFixture(t, ctx, d, tc.user)

			_, err := conn.DialUnix(ctx, DockerHostSocket)
			if err == nil {
				t.Fatal("the channel opened, want a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "connect failed") {
				t.Errorf("err = %v, want OpenSSH's connect-failed answer naming %q among its causes", err, tc.want)
			}
			if res, err := conn.Run(ctx, "true"); err != nil || res.ExitCode != 0 {
				t.Errorf("the login after a refused channel: %+v, %v; want it usable", res, err)
			}
		})
	}
}

// TestDockerHost_ARelativeSocketPathIsRefusedBeforeSSHD proves a path that
// is not absolute never reaches sshd, where its meaning would depend on the
// server's working directory.
func TestDockerHost_ARelativeSocketPathIsRefusedBeforeSSHD(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	d := StartDockerHost(t, EngineHostOptions{})
	conn := dialFixture(t, ctx, d, DockerHostUser)
	for _, path := range []string{"var/run/docker.sock", "", "/var/run/docker\x00.sock"} {
		if _, err := conn.DialUnix(ctx, path); err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Errorf("DialUnix(%q) = %v, want a refusal before sshd", path, err)
		}
	}
}
