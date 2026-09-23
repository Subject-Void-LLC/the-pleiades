// Tests that the shared sshd fixture serves what the file-transfer release
// gates exist to test.
package testsupport

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestStartSSHD_ServesSFTPAndLegacySCP is the fixture's own proof that
// it serves what the file-transfer release gates exist to test, checked
// with raw protocol bytes rather than through the adapters under test,
// so a fixture that silently lost the sftp subsystem or the scp binary
// fails here, by name, instead of turning every gate into a skip or a
// confusing failure (LESSONS_LEARNED #171, FAILURE_PATTERNS #207).
//
// It also runs the dial the gates depend on: host keys verified against
// the container's real keys through KnownHosts, directly and through
// the container acting as its own one-hop bastion.
func TestStartSSHD_ServesSFTPAndLegacySCP(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real sshd container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sshd, err := StartSSHD(ctx)
	if err != nil {
		t.Fatalf("StartSSHD: %v", err)
	}
	defer func() { _ = sshd.Terminate(context.Background()) }()

	dir := t.TempDir()
	knownHosts, err := sshd.KnownHosts(ctx, dir)
	if err != nil {
		t.Fatalf("KnownHosts: %v", err)
	}
	if data, _ := os.ReadFile(knownHosts); strings.Count(string(data), "\n") < 3 {
		t.Fatalf("known_hosts names too few keys:\n%s", data)
	}

	auth := remoteexec.PasswordAuth(SSHDUser, SSHDPassword)
	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: knownHosts})
	direct := remoteexec.Target{Host: sshd.Host, Port: sshd.Port}
	for name, dial := range map[string]func() (*remoteexec.Conn, error){
		"direct": func() (*remoteexec.Conn, error) { return runner.Connect(ctx, nil, direct, auth) },
		"one hop": func() (*remoteexec.Conn, error) {
			return runner.Connect(ctx, []remoteexec.Hop{{Target: direct, Auth: auth}},
				remoteexec.Target{Host: "127.0.0.1", Port: SSHDInnerPort}, auth)
		},
	} {
		conn, err := dial()
		if err != nil {
			t.Fatalf("%s: Connect with verified host keys: %v", name, err)
		}
		assertSFTPVersionReply(t, ctx, conn)
		assertLegacySCPSink(t, ctx, conn)
		_ = conn.Close()
	}

	if _, err := sshd.RootExec(ctx, "exit 3"); err == nil || !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("RootExec of a failing script error = %v, want it to report the exit status", err)
	}
	if out, err := sshd.RootExec(ctx, "id -u"); err != nil || strings.TrimSpace(out) != "0" {
		t.Errorf("RootExec ran as uid %q, %v; want root", out, err)
	}
}

// assertSFTPVersionReply opens the sftp subsystem and sends a raw
// SSH_FXP_INIT for protocol version 3, then requires an SSH_FXP_VERSION
// packet back: length, type 2, version 3.
func assertSFTPVersionReply(t *testing.T, ctx context.Context, conn *remoteexec.Conn) {
	t.Helper()
	sub, err := conn.Subsystem(ctx, "sftp")
	if err != nil {
		t.Fatalf("the fixture does not serve the sftp subsystem: %v", err)
	}
	defer sub.Close()
	if _, err := sub.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3}); err != nil {
		t.Fatalf("sending SSH_FXP_INIT: %v", err)
	}
	head := make([]byte, 9)
	if _, err := io.ReadFull(sub, head); err != nil {
		t.Fatalf("reading SSH_FXP_VERSION: %v", err)
	}
	if head[4] != 2 || binary.BigEndian.Uint32(head[5:9]) != 3 {
		t.Fatalf("the sftp subsystem answered %x, want an SSH_FXP_VERSION for version 3", head)
	}
}

// assertLegacySCPSink starts scp in sink mode and requires its ready
// byte, which is what proves the device still speaks legacy SCP rather
// than only OpenSSH's SFTP-backed scp client.
func assertLegacySCPSink(t *testing.T, ctx context.Context, conn *remoteexec.Conn) {
	t.Helper()
	proc, err := conn.Start(ctx, "scp -t -- /tmp")
	if err != nil {
		t.Fatalf("starting scp -t: %v", err)
	}
	defer proc.Close()
	ready := make([]byte, 1)
	if _, err := io.ReadFull(proc, ready); err != nil || ready[0] != 0 {
		t.Fatalf("scp -t answered %v, %v; want its ready byte 0", ready, err)
	}
}
