// Package ssh: a known_hosts file pinning only one of a real sshd's host keys
// verifies it, whichever type that key is.
package ssh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestSSHContainer_APinnedHostKeyOfAnyOneTypeVerifies starts the real OpenSSH
// server, which holds an RSA, an ECDSA and an ed25519 host key, and connects
// once per key with a known_hosts file naming only that key. Before the client
// offered the pinned type first, Go's default order made the server answer
// with its ECDSA key, and a file holding only the ed25519 key refused the
// connection as "key mismatch" although the key was right (measured
// 2026-09-25 against a lab VM; testsupport.SSHD.KnownHosts lists every type
// for that reason).
func TestSSHContainer_APinnedHostKeyOfAnyOneTypeVerifies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH container integration test in short mode")
	}
	ctx := context.Background()
	srv, err := testsupport.StartSSHD(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Terminate(context.Background()) })

	pub, err := srv.RootExec(ctx, "cat /config/ssh_host_keys/ssh_host_*_key.pub")
	if err != nil {
		t.Fatalf("reading the server's host keys: %v", err)
	}
	keys := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(pub), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("unexpected host key line %q", line)
		}
		keys[fields[0]] = fields[0] + " " + fields[1]
	}
	// The case is only real while the server has more than one type, and
	// the measured failure needs both ECDSA and ed25519.
	for _, want := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256"} {
		if keys[want] == "" {
			t.Fatalf("the sshd image holds host keys %v, want %s among them", keys, want)
		}
	}

	auth, err := remoteexec.AuthFrom(testsupport.SSHDUser, testsupport.SSHDPassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	target := remoteexec.Target{Host: srv.Host, Port: srv.Port}
	for keyType, entry := range keys {
		t.Run(keyType, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			line := fmt.Sprintf("[%s]:%d %s\n", srv.Host, srv.Port, entry)
			if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			conn, err := remoteexec.New(remoteexec.Options{KnownHostsPath: path}).Connect(ctx, nil, target, auth)
			if err != nil {
				t.Fatalf("connecting with only the %s host key in known_hosts: %v", keyType, err)
			}
			defer conn.Close()
			res, err := conn.Run(ctx, "echo pinned")
			if err != nil || strings.TrimSpace(res.Stdout) != "pinned" {
				t.Fatalf("running a command over the verified connection: %+v, %v", res, err)
			}
		})
	}
}
