// The SFTP release gate, against a real OpenSSH server.
package sftpxfer_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sftpxfer"
)

// One real OpenSSH server shared by every container-backed test in this
// package, started once and torn down by TestMain, following
// internal/transport/ssh's requireSSHContainer.
var (
	sshdOnce       sync.Once
	sshdErr        error
	sshdServer     *testsupport.SSHD
	sshdKnownHosts string
	sshdDir        string

	// sshdGoleak snapshots testcontainers' own long-lived goroutines
	// (its reaper) once the container is up, so a test fails only for a
	// leak it introduced.
	sshdGoleak []goleak.Option
)

// TestMain tears the shared container down once, after every test.
func TestMain(m *testing.M) {
	code := m.Run()
	if sshdServer != nil {
		_ = sshdServer.Terminate(context.Background())
	}
	if sshdDir != "" {
		_ = os.RemoveAll(sshdDir)
	}
	os.Exit(code)
}

// requireSSHD starts the shared server once and returns it.
func requireSSHD(t testing.TB) *testsupport.SSHD {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the container-backed SFTP release gate in -short mode")
	}
	sshdOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		sshdServer, sshdErr = testsupport.StartSSHD(ctx)
		if sshdErr != nil {
			return
		}
		sshdDir, sshdErr = os.MkdirTemp("", "sftpxfer-gate-")
		if sshdErr != nil {
			return
		}
		sshdKnownHosts, sshdErr = sshdServer.KnownHosts(ctx, sshdDir)
		sshdGoleak = []goleak.Option{goleak.IgnoreCurrent()}
	})
	if sshdErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the sshd container (is Docker running?): %v", sshdErr)
		}
		t.Fatalf("%v", sshdErr)
	}
	return sshdServer
}

// openStore dials the shared server through the real pkg/remoteexec
// path, with host keys verified against the container's real key,
// directly or through the container acting as its own one-hop bastion,
// and opens an SFTP session over the "sftp" subsystem exactly as a
// Collection would.
func openStore(t testing.TB, sshd *testsupport.SSHD, hop bool) filexfer.Store {
	t.Helper()
	ctx := context.Background()
	auth := remoteexec.PasswordAuth(testsupport.SSHDUser, testsupport.SSHDPassword)
	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: sshdKnownHosts})

	var hops []remoteexec.Hop
	target := remoteexec.Target{Host: sshd.Host, Port: sshd.Port}
	if hop {
		hops = []remoteexec.Hop{{Target: target, Auth: auth}}
		target = remoteexec.Target{Host: "127.0.0.1", Port: testsupport.SSHDInnerPort}
	}
	conn, err := runner.Connect(ctx, hops, target, auth)
	if err != nil {
		t.Fatalf("Connect(hop=%v) error = %v", hop, err)
	}
	sub, err := conn.Subsystem(ctx, "sftp")
	if err != nil {
		_ = conn.Close()
		t.Fatalf("Subsystem(sftp) error = %v", err)
	}
	client, err := sftpxfer.Open(ctx, sub)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("sftpxfer.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = conn.Close()
	})
	return client
}

// TestSFTPReleaseGate runs the shared file-transfer conformance suite
// against a real OpenSSH server over a real SSH connection: byte-exact
// round trips (binary, NUL runs, protocol lookalikes, packet-boundary
// sizes) directly and through a real bastion hop, exact modes, size and
// limit enforcement, every lexical and physical escape refused with the
// device provably unchanged, atomic replacement under a concurrent
// reader, an abort mid-stream leaving the target intact, and 256 MiB
// each way in bounded memory.
func TestSFTPReleaseGate(t *testing.T) {
	sshd := requireSSHD(t)
	filexfertest.Run(t, filexfertest.Harness{
		Name:    "sftp",
		Open:    func(t *testing.T, hop bool) filexfer.Store { return openStore(t, sshd, hop) },
		Root:    testsupport.SSHDHome + "/xfer",
		Outside: testsupport.SSHDHome + "/outside",
		Remote: func(ctx context.Context, script string) (string, error) {
			return sshd.RootExec(ctx, script)
		},
		// Killing the session mid-Put also kills the requests that would
		// remove the private directory; the target is intact regardless.
		AbortLeavesPrivateDir: true,
	})
	goleak.VerifyNone(t, sshdGoleak...)
}

// TestSFTPReleaseGate_StatAgainstOpenSSH covers the optional Stater
// interface against the real server, including that a symlink is
// described rather than followed.
func TestSFTPReleaseGate_StatAgainstOpenSSH(t *testing.T) {
	sshd := requireSSHD(t)
	ctx := context.Background()
	root := testsupport.SSHDHome + "/stat"
	if _, err := sshd.RootExec(ctx, "rm -rf "+root+" && mkdir -p "+root+" && cd "+root+
		" && printf 12345 > f && chmod 640 f && ln -s /etc/passwd link && chown -h -R 1000:1000 "+root); err != nil {
		t.Fatal(err)
	}
	store := openStore(t, sshd, false)
	stater, ok := store.(filexfer.Stater)
	if !ok {
		t.Fatal("the SFTP Store does not implement filexfer.Stater")
	}
	f, _ := filexfer.Resolve(root, "f")
	info, err := stater.Stat(ctx, f)
	if err != nil || info.Size != 5 || info.Mode != 0o640 || info.Kind != filexfer.KindRegular {
		t.Fatalf("Stat(f) = %+v, %v", info, err)
	}
	link, _ := filexfer.Resolve(root, "link")
	info, err = stater.Stat(ctx, link)
	if err != nil || info.Kind != filexfer.KindSymlink {
		t.Fatalf("Stat(link) = %+v, %v; want a symlink described, not followed", info, err)
	}
}
