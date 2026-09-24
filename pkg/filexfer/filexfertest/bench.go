// The transfer benchmark harness, and the OpenSSH command-line clients it
// compares every Store against.
package filexfertest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// BenchSize is how many bytes each transfer benchmark moves per
// operation: large enough that per-connection setup is a small part of
// the number, small enough to keep a benchmark run to seconds.
const BenchSize = 64 << 20

// CLI is how a benchmark reaches the OpenSSH command-line client for
// the same server as the Store under test, so the two are compared on
// the same machine, the same container and the same payload.
type CLI struct {
	// Host, Port and User name the server.
	Host string
	Port int
	User string
	// KeyPath is a private key the server authorizes for User.
	KeyPath string
	// KnownHosts is a known_hosts file naming the server's real keys.
	KnownHosts string
}

// options returns the ssh options both clients take, host keys verified
// and nothing interactive.
func (c CLI) options() []string {
	return []string{
		"-q", "-i", c.KeyPath, "-P", strconv.Itoa(c.Port),
		"-o", "UserKnownHostsFile=" + c.KnownHosts, "-o", "StrictHostKeyChecking=yes",
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
	}
}

// SCP runs "scp -O" (legacy protocol, the same one pkg/scpxfer speaks)
// from src to dst, either of which may be user@host:path.
func (c CLI) SCP(src, dst string) error {
	args := append([]string{"-O"}, c.options()...)
	args = append(args, src, dst)
	return run(exec.Command("scp", args...)) // #nosec G204 -- benchmark harness; every argument is this test's own
}

// SFTP runs one sftp batch command ("put a b" or "get a b").
func (c CLI) SFTP(dir, batch string) error {
	path := filepath.Join(dir, "batch")
	if err := os.WriteFile(path, []byte(batch+"\n"), 0o600); err != nil {
		return err
	}
	args := append(c.options(), "-b", path, c.User+"@"+c.Host)
	return run(exec.Command("sftp", args...)) // #nosec G204 -- benchmark harness; every argument is this test's own
}

// Remote names path on the server in scp's user@host:path form.
func (c CLI) Remote(path string) string { return c.User + "@" + c.Host + ":" + path }

// run runs cmd and reports its combined output on failure.
func run(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", cmd.Path, err, out)
	}
	return nil
}

// BenchPut measures one Put of BenchSize bytes per operation, dialing a
// fresh connection each time through open, since a command-line client
// must too.
func BenchPut(b *testing.B, open func() (filexfer.Store, func()), p filexfer.Path) {
	payload := make([]byte, BenchSize)
	b.SetBytes(BenchSize)
	b.ResetTimer()
	for b.Loop() {
		store, done := open()
		if err := store.Put(context.Background(), p, bytes.NewReader(payload), BenchSize, 0o644); err != nil {
			b.Fatal(err)
		}
		done()
	}
}

// BenchGet measures one Get of BenchSize bytes per operation, dialing a
// fresh connection each time.
func BenchGet(b *testing.B, open func() (filexfer.Store, func()), p filexfer.Path) {
	b.SetBytes(BenchSize)
	b.ResetTimer()
	for b.Loop() {
		store, done := open()
		if n, err := store.Get(context.Background(), p, io.Discard, BenchSize); err != nil || n != BenchSize {
			b.Fatalf("Get() = %d, %v", n, err)
		}
		done()
	}
}
