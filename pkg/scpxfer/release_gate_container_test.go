// The SCP release gate, against a real OpenSSH server.
package scpxfer_test

import (
	"bytes"
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/scpxfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sftpxfer"
)

// One real OpenSSH server shared by every container-backed test in this
// package, the same pinned image and starter pkg/sftpxfer's release gate
// uses, torn down by TestMain.
var (
	sshdOnce       sync.Once
	sshdErr        error
	sshdServer     *testsupport.SSHD
	sshdKnownHosts string
	sshdDir        string
	sshdGoleak     []goleak.Option
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
		t.Skip("skipping the container-backed SCP release gate in -short mode")
	}
	sshdOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		sshdServer, sshdErr = testsupport.StartSSHD(ctx)
		if sshdErr != nil {
			return
		}
		sshdDir, sshdErr = os.MkdirTemp("", "scpxfer-gate-")
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

// connect dials the shared server through the real pkg/remoteexec path,
// host keys verified against the container's real keys, directly or
// through the container acting as its own one-hop bastion.
func connect(t testing.TB, sshd *testsupport.SSHD, hop bool) *remoteexec.Conn {
	t.Helper()
	auth := remoteexec.PasswordAuth(testsupport.SSHDUser, testsupport.SSHDPassword)
	runner := remoteexec.New(remoteexec.Options{KnownHostsPath: sshdKnownHosts})
	var hops []remoteexec.Hop
	target := remoteexec.Target{Host: sshd.Host, Port: sshd.Port}
	if hop {
		hops = []remoteexec.Hop{{Target: target, Auth: auth}}
		target = remoteexec.Target{Host: "127.0.0.1", Port: testsupport.SSHDInnerPort}
	}
	conn, err := runner.Connect(context.Background(), hops, target, auth)
	if err != nil {
		t.Fatalf("Connect(hop=%v) error = %v", hop, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestSCPReleaseGate runs the shared file-transfer conformance suite
// against a real OpenSSH server's legacy scp over a real SSH connection:
// the same assertions, word for word, the SFTP adapter's release gate
// makes, so the port means the same thing whichever protocol carries it.
func TestSCPReleaseGate(t *testing.T) {
	sshd := requireSSHD(t)
	filexfertest.Run(t, filexfertest.Harness{
		Name: "scp",
		Open: func(t *testing.T, hop bool) filexfer.Store {
			return scpxfer.New(connect(t, sshd, hop))
		},
		Root:    testsupport.SSHDHome + "/xfer",
		Outside: testsupport.SSHDHome + "/outside",
		Remote: func(ctx context.Context, script string) (string, error) {
			return sshd.RootExec(ctx, script)
		},
		// The device script's EXIT trap removes its private directory
		// even when the session is killed mid-stream, so SCP promises
		// what SFTP cannot.
		AbortLeavesPrivateDir: false,
	})
	goleak.VerifyNone(t, sshdGoleak...)
}

// TestCrossProtocolAgreement puts a file over one protocol and gets it
// back over the other, both ways, on the same real server. A pair of
// adapters that each agreed only with itself would fail here.
func TestCrossProtocolAgreement(t *testing.T) {
	sshd := requireSSHD(t)
	ctx := context.Background()
	root := testsupport.SSHDHome + "/cross"
	if _, err := sshd.RootExec(ctx, "rm -rf "+root+" && mkdir -p "+root+" && chown 1000:1000 "+root); err != nil {
		t.Fatal(err)
	}

	scp := scpxfer.New(connect(t, sshd, false))
	conn := connect(t, sshd, false)
	sub, err := conn.Subsystem(ctx, "sftp")
	if err != nil {
		t.Fatal(err)
	}
	sftp, err := sftpxfer.Open(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sftp.Close() })

	content := bytes.Repeat([]byte("\x00C0644 1 x\n\x01\x02"), 20000)
	pairs := []struct {
		name     string
		put, get filexfer.Store
	}{
		{"sftp then scp", sftp, scp},
		{"scp then sftp", scp, sftp},
	}
	for _, pair := range pairs {
		p, err := filexfer.Resolve(root, "cross-"+pair.name[:3])
		if err != nil {
			t.Fatal(err)
		}
		if err := pair.put.Put(ctx, p, bytes.NewReader(content), int64(len(content)), 0o640); err != nil {
			t.Fatalf("%s: Put() error = %v", pair.name, err)
		}
		var got bytes.Buffer
		if _, err := pair.get.Get(ctx, p, &got, int64(len(content))); err != nil || !bytes.Equal(got.Bytes(), content) {
			t.Fatalf("%s: the other protocol read back %d bytes, %v; want the %d put", pair.name, got.Len(), err, len(content))
		}
	}
}
