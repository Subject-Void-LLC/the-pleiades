//go:build unix

// End-to-end SCP tests: the real device scripts, a real shell and a real scp
// binary, behind an in-process SSH server. Unix only: the device side is
// /bin/sh, and the escape tests plant a FIFO, which Windows does not have.
package scpxfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// scpFixture is an SCP client whose "device" is this machine:
// remoteexectest's in-process SSH server runs every exec request through
// the real /bin/sh, so the device-side scripts, the real kernel's cd -P
// and pwd -P, and this machine's real scp binary in sink and source mode
// all run for real. What it is not is OpenSSH's sshd, which the
// container-backed release gate covers.
type scpFixture struct {
	client  *Client
	srv     *remoteexectest.Server
	dir     string
	root    string
	outside string
}

// newSCPFixture starts the server and lays out a root beside an outside
// directory.
func newSCPFixture(t *testing.T, opts remoteexectest.Options) *scpFixture {
	t.Helper()
	if _, err := os.Stat("/usr/bin/scp"); err != nil {
		t.Skip("no scp binary on this machine to act as the device's")
	}
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(context.Background(), nil,
		remoteexec.Target{Host: srv.Host, Port: srv.Port}, remoteexec.PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	dir := t.TempDir()
	f := &scpFixture{client: New(conn), srv: srv, dir: dir,
		root: filepath.Join(dir, "root"), outside: filepath.Join(dir, "outside")}
	for _, d := range []string{f.root, f.outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// resolve builds a Path under the fixture's root.
func (f *scpFixture) resolve(t *testing.T, leaf string) filexfer.Path {
	t.Helper()
	p, err := filexfer.Resolve(f.root, leaf)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", leaf, err)
	}
	return p
}

// waitNoTemporaries polls until no private directory is left in dir:
// the device script's cleanup trap runs as its shell exits, which can
// trail the client's return by a moment.
func waitNoTemporaries(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(dir, ".pleiades-xfer-*"))
		if len(matches) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("private directories were left behind: %v", matches)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSCP_RoundTripReplaceAndMode covers the ordinary path through the
// real scripts and the real scp.
func TestSCP_RoundTripReplaceAndMode(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	ctx := context.Background()
	content := append([]byte("C0644 1 x\n\x00\x01\x02"), bytes.Repeat([]byte{0, 0xFF}, 40000)...)
	p := f.resolve(t, "image.bin")
	for _, mode := range []filexfer.Mode{0o600, 0o750} {
		if err := f.client.Put(ctx, p, bytes.NewReader(content), int64(len(content)), mode); err != nil {
			t.Fatalf("Put(mode %s) error = %v", mode, err)
		}
		info, err := os.Stat(p.String())
		if err != nil || filexfer.Mode(info.Mode().Perm()) != mode {
			t.Fatalf("mode on disk = %v, %v; want %s", info.Mode().Perm(), err, mode)
		}
	}
	var got bytes.Buffer
	n, err := f.client.Get(ctx, p, &got, int64(len(content)))
	if err != nil || n != int64(len(content)) || !bytes.Equal(got.Bytes(), content) {
		t.Fatalf("Get() = %d, %v; want the %d bytes put", n, err, len(content))
	}
	if err := f.client.Put(ctx, f.resolve(t, "empty"), strings.NewReader(""), 0, 0o644); err != nil {
		t.Fatalf("Put(empty) error = %v", err)
	}
	waitNoTemporaries(t, f.root)
}

// TestSCP_HostileNamesAreQuoted is the injection boundary: every name
// lands byte for byte as a file of that exact name, and a canary the
// name tries to create through the device's shell never appears.
func TestSCP_HostileNamesAreQuoted(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	ctx := context.Background()
	for _, leaf := range []string{
		"it's", "$(touch canary)", "`touch canary`", "a;touch canary", "a|touch canary",
		"-rf", "--help", "*", "name with spaces", "$HOME", "a&&touch canary", "'", "\"",
	} {
		p := f.resolve(t, leaf)
		if err := f.client.Put(ctx, p, strings.NewReader(leaf), int64(len(leaf)), 0o644); err != nil {
			t.Fatalf("Put(%q) error = %v", leaf, err)
		}
		if got, err := os.ReadFile(filepath.Join(f.root, leaf)); err != nil || string(got) != leaf {
			t.Fatalf("file %q on disk = %q, %v", leaf, got, err)
		}
		var back bytes.Buffer
		if _, err := f.client.Get(ctx, p, &back, 100); err != nil || back.String() != leaf {
			t.Fatalf("Get(%q) = %q, %v", leaf, back.String(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "canary")); err == nil {
		t.Fatal("a hostile name ran a command on the device")
	}
}

// TestSCP_HostileRootIsQuoted covers the inventory half of the path.
func TestSCP_HostileRootIsQuoted(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	root := filepath.Join(f.dir, "r o'ot $(touch canary) *")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := filexfer.Resolve(root, "f")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Put(context.Background(), p, strings.NewReader("ok"), 2, 0o644); err != nil {
		t.Fatalf("Put() under a hostile root error = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "f")); string(got) != "ok" {
		t.Fatalf("file under a hostile root holds %q", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(f.dir, "*", "canary")); len(matches) != 0 {
		t.Fatalf("a hostile root ran a command on the device: %v", matches)
	}
}

// TestSCP_PhysicalEscapesAreRefused plants every trap the lexical guard
// cannot see and checks each is refused with the source unread, the file
// outside unchanged, and nothing left behind.
func TestSCP_PhysicalEscapesAreRefused(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	keys := filepath.Join(f.outside, "authorized_keys")
	if err := os.WriteFile(keys, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"abs": f.outside, "rel": "../outside", "keys": keys} {
		if err := os.Symlink(target, filepath.Join(f.root, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(f.root, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(f.root, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]filexfer.Containment{
		"abs/authorized_keys": filexfer.ContainmentOutsideRoot,
		"rel/authorized_keys": filexfer.ContainmentOutsideRoot,
		"keys":                filexfer.ContainmentSymlinkLeaf,
		"adir":                filexfer.ContainmentDirectoryLeaf,
		"fifo":                filexfer.ContainmentNotRegular,
		"missing/f":           filexfer.ContainmentParentMissing,
	}
	for leaf, want := range cases {
		p := f.resolve(t, leaf)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		src := &countingReader{r: strings.NewReader("attacker key")}
		putErr := f.client.Put(ctx, p, src, 12, 0o600)
		var sink bytes.Buffer
		_, getErr := f.client.Get(ctx, p, &sink, 1<<20)
		cancel()

		var cErr *filexfer.ContainmentError
		if !errors.As(putErr, &cErr) || cErr.Reason != want {
			t.Errorf("Put(%s) error = %v, want %v", leaf, putErr, want)
		}
		if !errors.As(getErr, &cErr) || cErr.Reason != want {
			t.Errorf("Get(%s) error = %v, want %v", leaf, getErr, want)
		}
		if src.read != 0 || sink.Len() != 0 {
			t.Errorf("%s: %d source bytes read and %d destination bytes written before the refusal", leaf, src.read, sink.Len())
		}
	}
	if got, _ := os.ReadFile(keys); string(got) != "original" {
		t.Fatalf("the file outside the root holds %q", got)
	}
	waitNoTemporaries(t, f.root)
}

// TestSCP_TrailingNewlineCannotImpersonateTheRoot is the attack the
// preflight's pwd sentinel exists for: a directory named "root" plus a
// newline, reached through a symlink inside the root, would read back
// as the root itself if command substitution stripped the newline.
func TestSCP_TrailingNewlineCannotImpersonateTheRoot(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	impostor := f.root + "\n"
	if err := os.Mkdir(impostor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../root\n", filepath.Join(f.root, "link")); err != nil {
		t.Fatal(err)
	}
	err := f.client.Put(context.Background(), f.resolve(t, "link/f"), strings.NewReader("x"), 1, 0o644)
	var cErr *filexfer.ContainmentError
	if !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentOutsideRoot {
		t.Fatalf("Put() into the newline impostor error = %v, want ContainmentOutsideRoot", err)
	}
	if _, err := os.Stat(filepath.Join(impostor, "f")); err == nil {
		t.Fatal("the file was written into the impostor directory")
	}
}

// TestSCP_RefusalsAndLimits covers the remaining refusals.
func TestSCP_RefusalsAndLimits(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	ctx := context.Background()

	gone, _ := filexfer.Resolve(filepath.Join(f.dir, "gone"), "f")
	var cErr *filexfer.ContainmentError
	if err := f.client.Put(ctx, gone, strings.NewReader("x"), 1, 0o644); !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentRootMissing {
		t.Errorf("Put() under a missing root error = %v", err)
	}
	if _, err := f.client.Get(ctx, f.resolve(t, "absent"), io.Discard, 10); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get() of a missing file error = %v, want fs.ErrNotExist", err)
	}
	big := f.resolve(t, "big")
	if err := os.WriteFile(big.String(), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	var sink bytes.Buffer
	if _, err := f.client.Get(ctx, big, &sink, 999); !errors.Is(err, filexfer.ErrLimitExceeded) || sink.Len() != 0 {
		t.Errorf("Get() over the limit = %v with %d bytes written", err, sink.Len())
	}
	if err := f.client.Put(ctx, filexfer.Path{}, strings.NewReader("x"), 1, 0o644); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("Put(zero Path) error = %v", err)
	}
	if _, err := f.client.Get(ctx, filexfer.Path{}, io.Discard, 1); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("Get(zero Path) error = %v", err)
	}
	if cmds := f.srv.Commands(); len(cmds) != 3 {
		t.Errorf("the device ran %d commands, want 3: the zero Paths must never reach it", len(cmds))
	}

	keep := f.resolve(t, "keep")
	if err := os.WriteFile(keep.String(), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Put(ctx, keep, strings.NewReader("abc"), 10, 0o644); !errors.Is(err, filexfer.ErrSizeMismatch) {
		t.Errorf("Put() with a short source error = %v, want ErrSizeMismatch", err)
	}
	if got, _ := os.ReadFile(keep.String()); string(got) != "original" {
		t.Errorf("target holds %q after a failed Put", got)
	}
	waitNoTemporaries(t, f.root)
}

// TestSCP_CancelAndSinkFailures covers the caller walking away mid-Put
// and a Get destination failing mid-stream; both must return promptly
// and leave the device clean.
func TestSCP_CancelAndSinkFailures(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	p := f.resolve(t, "stream")
	if err := os.WriteFile(p.String(), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := f.client.Put(ctx, p, &cancelingSource{cancel: cancel}, 64<<20, 0o644)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() canceled mid-stream error = %v, want context.Canceled", err)
	}
	if got, _ := os.ReadFile(p.String()); string(got) != "original" {
		t.Fatalf("target holds %d bytes after a canceled Put", len(got))
	}
	waitNoTemporaries(t, f.root)

	large := f.resolve(t, "large")
	if err := os.WriteFile(large.String(), make([]byte, 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.client.Get(context.Background(), large, &failAfter{n: 1 << 20}, 16<<20)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "sink closed") {
			t.Fatalf("Get() into a failing destination error = %v, want the destination's failure", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Get() into a failing destination hung instead of closing the session")
	}
}

// cancelingSource cancels on its second read and keeps serving, so the
// Put is past the device's acceptance and streaming when it happens.
type cancelingSource struct {
	cancel context.CancelFunc
	reads  int
}

// Read serves bytes, canceling on the second call.
func (c *cancelingSource) Read(p []byte) (int, error) {
	c.reads++
	if c.reads == 2 {
		c.cancel()
		time.Sleep(50 * time.Millisecond)
	}
	for i := range p {
		p[i] = 'q'
	}
	return len(p), nil
}

// TestSCP_DeviceFailures covers the device refusing on its own
// permissions and the connection refusing the session.
func TestSCP_DeviceFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced for root")
	}
	f := newSCPFixture(t, remoteexectest.Options{})
	ro := filepath.Join(f.root, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	err := f.client.Put(context.Background(), f.resolve(t, "ro/f"), strings.NewReader("x"), 1, 0o644)
	if err == nil || !strings.Contains(err.Error(), "private directory") {
		t.Fatalf("Put() into a read-only directory error = %v, want the mktemp failure", err)
	}

	refusing := newSCPFixture(t, remoteexectest.Options{SessionLimit: remoteexectest.Limit(0)})
	if err := refusing.client.Put(context.Background(), refusing.resolve(t, "f"), strings.NewReader("x"), 1, 0o644); err == nil {
		t.Fatal("Put() on a connection refusing sessions error = nil")
	}
}

// TestSCP_EveryLexicalEscapeNeverReachesTheDevice runs the shared
// payloads: none becomes a Path, and the zero Path runs nothing.
func TestSCP_EveryLexicalEscapeNeverReachesTheDevice(t *testing.T) {
	f := newSCPFixture(t, remoteexectest.Options{})
	for _, payload := range filexfertest.EscapePayloads {
		p, err := filexfer.Resolve(f.root, payload.Leaf)
		if err == nil {
			t.Fatalf("%s: Resolve accepted %q", payload.Name, payload.Leaf)
		}
		if err := f.client.Put(context.Background(), p, strings.NewReader("x"), 1, 0o644); !errors.Is(err, filexfer.ErrUnresolvedPath) {
			t.Fatalf("%s: Put() error = %v", payload.Name, err)
		}
	}
	if cmds := f.srv.Commands(); len(cmds) != 0 {
		t.Fatalf("escape payloads ran %d commands on the device, want none", len(cmds))
	}
}
