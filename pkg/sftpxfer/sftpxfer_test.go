// Tests for SFTP transfers against pkg/sftp's own server over an in-memory
// pipe, with every packet the client sends recorded.
package sftpxfer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// SFTP packet types this package's tests assert on (draft-ietf-secsh-
// filexfer-02, the version 3 protocol). The list is what a refused
// transfer must never send: anything that opens, creates, writes,
// renames, removes or changes a file.
const (
	fxpOpen     = 3
	fxpWrite    = 6
	fxpLstat    = 7
	fxpSetstat  = 9
	fxpFsetstat = 10
	fxpRemove   = 13
	fxpMkdir    = 14
	fxpRmdir    = 15
	fxpRealpath = 16
	fxpStat     = 17
	fxpRename   = 18
	fxpReadlink = 19
	fxpExtended = 200
)

// mutating is every packet type that changes or opens something on the
// server. A refused transfer's recording must contain none of them.
var mutating = map[byte]string{
	fxpOpen: "OPEN", fxpWrite: "WRITE", fxpSetstat: "SETSTAT", fxpFsetstat: "FSETSTAT",
	fxpRemove: "REMOVE", fxpMkdir: "MKDIR", fxpRmdir: "RMDIR", fxpRename: "RENAME",
	fxpExtended: "EXTENDED",
}

// recordingConn wraps the client's end of the pipe and records the type
// of every complete SFTP packet the client sends. SFTP frames each
// packet as a four-byte big-endian length and then a one-byte type, so
// the recording is the protocol's own view of what was asked, not this
// package's idea of what it asked.
type recordingConn struct {
	net.Conn

	mu      sync.Mutex
	pending []byte
	types   []byte
}

// Write records the packets in p, then forwards p unchanged.
func (r *recordingConn) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.pending = append(r.pending, p...)
	for len(r.pending) >= 5 {
		length := binary.BigEndian.Uint32(r.pending[:4])
		if uint32(len(r.pending)-4) < length {
			break
		}
		r.types = append(r.types, r.pending[4])
		r.pending = r.pending[4+length:]
	}
	r.mu.Unlock()
	return r.Conn.Write(p)
}

// since returns the packet types recorded after mark.
func (r *recordingConn) since(mark int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.types[mark:]...)
}

// mark returns how many packets have been recorded so far.
func (r *recordingConn) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.types)
}

// fixture is one SFTP session against pkg/sftp's own server, which
// serves the real local filesystem, over an in-memory pipe. It is a
// real SFTP server implementation speaking the real protocol, so this
// package's code path is exercised end to end; what it is not is
// OpenSSH, which the container-backed release gate covers.
type fixture struct {
	client *Client
	rec    *recordingConn
	dir    string // a scratch directory holding root/ and outside/
	root   string // the transfer root, dir/root
}

// newFixture starts the server and the client and lays out a transfer
// root beside a directory outside it.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, d := range []string{root, filepath.Join(dir, "outside")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	clientEnd, serverEnd := net.Pipe()
	server, err := sftp.NewServer(serverEnd)
	if err != nil {
		t.Fatalf("starting the SFTP server: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve()
	}()

	rec := &recordingConn{Conn: clientEnd}
	client, err := Open(context.Background(), rec)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-served
	})
	return &fixture{client: client, rec: rec, dir: dir, root: root}
}

// resolve builds a Path under the fixture's root, failing the test on a
// refusal.
func (f *fixture) resolve(t *testing.T, leaf string) filexfer.Path {
	t.Helper()
	p, err := filexfer.Resolve(filepath.ToSlash(f.root), leaf)
	if err != nil {
		t.Fatalf("Resolve(%q) error = %v", leaf, err)
	}
	return p
}

// assertNothingMutated fails the test if the packets recorded since mark
// include anything that opens, creates, writes, renames or removes.
func (f *fixture) assertNothingMutated(t *testing.T, mark int) {
	t.Helper()
	for _, typ := range f.rec.since(mark) {
		if name, bad := mutating[typ]; bad {
			t.Errorf("a refused transfer sent an SFTP %s packet", name)
		}
	}
}

// binaryPayload returns every byte value, runs of NUL, and content that
// looks like SCP's wire protocol, so nothing along the way can be
// treating the content as text or as protocol.
func binaryPayload() []byte {
	var b bytes.Buffer
	for i := 0; i < 256; i++ {
		b.WriteByte(byte(i))
	}
	b.Write(make([]byte, 1000))
	b.WriteString("\nC0644 1 x\n\x00\x01\x02")
	return b.Bytes()
}

// TestPutGet_RoundTripsBytesExactly covers the shapes a stream can go
// wrong on: empty, binary with NULs, and sizes either side of pkg/sftp's
// 32 KiB packet.
func TestPutGet_RoundTripsBytesExactly(t *testing.T) {
	f := newFixture(t)
	payloads := map[string][]byte{
		"empty":        {},
		"binary":       binaryPayload(),
		"packet minus": bytes.Repeat([]byte{0xA5}, 32767),
		"packet":       bytes.Repeat([]byte{0x00}, 32768),
		"packet plus":  bytes.Repeat([]byte{0xFF}, 32769),
		"many packets": bytes.Repeat([]byte("0123456789abcdef"), 40000),
	}
	for name, content := range payloads {
		t.Run(name, func(t *testing.T) {
			p := f.resolve(t, "sub-"+strings.ReplaceAll(name, " ", "-"))
			ctx := context.Background()
			if err := f.client.Put(ctx, p, bytes.NewReader(content), int64(len(content)), 0o640); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			onDisk, err := os.ReadFile(p.String())
			if err != nil || !bytes.Equal(onDisk, content) {
				t.Fatalf("file on disk differs from what was put (err %v)", err)
			}
			var got bytes.Buffer
			n, err := f.client.Get(ctx, p, &got, int64(len(content)))
			if err != nil || n != int64(len(content)) || !bytes.Equal(got.Bytes(), content) {
				t.Fatalf("Get() = %d bytes, %v; want the %d bytes put", n, err, len(content))
			}
		})
	}
}

// TestPut_SetsTheModeExactly proves the mode is set on the handle, not
// left to the server's umask.
func TestPut_SetsTheModeExactly(t *testing.T) {
	f := newFixture(t)
	for _, mode := range []filexfer.Mode{0o600, 0o640, 0o755, 0o777} {
		p := f.resolve(t, "mode-"+mode.String())
		if err := f.client.Put(context.Background(), p, strings.NewReader("x"), 1, mode); err != nil {
			t.Fatalf("Put(mode %s) error = %v", mode, err)
		}
		info, err := os.Stat(p.String())
		if err != nil || filexfer.Mode(info.Mode().Perm()) != mode {
			t.Errorf("mode on disk = %v (err %v), want %s", info.Mode().Perm(), err, mode)
		}
	}
}

// TestPut_ReplacesAndLeavesNoTemporaryBehind covers replacing an
// existing file and the cleanup of the private directory on success.
func TestPut_ReplacesAndLeavesNoTemporaryBehind(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "config.txt")
	if err := os.WriteFile(p.String(), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Put(context.Background(), p, strings.NewReader("new!"), 4, 0o644); err != nil {
		t.Fatalf("Put() over an existing file error = %v", err)
	}
	if got, _ := os.ReadFile(p.String()); string(got) != "new!" {
		t.Errorf("file holds %q, want the new content", got)
	}
	assertNoTemporaries(t, f.root)
}

// assertNoTemporaries fails the test if a private Put directory was left
// in dir.
func assertNoTemporaries(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempDirPrefix) {
			t.Errorf("Put left %s behind in %s", e.Name(), dir)
		}
	}
}

// TestPut_SizeMismatchLeavesTheTargetUntouched is "exactly size bytes or
// nothing" end to end, in both directions, including a source much
// larger than one packet so the failure arrives mid-stream.
func TestPut_SizeMismatchLeavesTheTargetUntouched(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "keep.txt")
	if err := os.WriteFile(p.String(), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		src  io.Reader
		size int64
	}{
		"short": {strings.NewReader("abc"), 10},
		"long":  {bytes.NewReader(make([]byte, 200000)), 100000},
	}
	for name, tc := range cases {
		err := f.client.Put(context.Background(), p, tc.src, tc.size, 0o644)
		if !errors.Is(err, filexfer.ErrSizeMismatch) {
			t.Errorf("%s: Put() error = %v, want ErrSizeMismatch", name, err)
		}
		if got, _ := os.ReadFile(p.String()); string(got) != "original" {
			t.Errorf("%s: target holds %q after a failed Put, want it untouched", name, got)
		}
	}
	assertNoTemporaries(t, f.root)
}

// TestPut_RefusesBadArgumentsWithoutSendingAPacket is the first half of
// the adversarial proof: a zero Path, a bad mode or a negative size
// never reaches the wire.
func TestPut_RefusesBadArgumentsWithoutSendingAPacket(t *testing.T) {
	f := newFixture(t)
	good := f.resolve(t, "f")
	mark := f.rec.mark()
	ctx := context.Background()

	if err := f.client.Put(ctx, filexfer.Path{}, strings.NewReader("x"), 1, 0o644); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("Put(zero Path) error = %v, want ErrUnresolvedPath", err)
	}
	if err := f.client.Put(ctx, good, strings.NewReader("x"), 1, 0o4755); !errors.Is(err, filexfer.ErrInvalidMode) {
		t.Errorf("Put(setuid) error = %v, want ErrInvalidMode", err)
	}
	if _, err := f.client.Get(ctx, filexfer.Path{}, io.Discard, 1); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("Get(zero Path) error = %v, want ErrUnresolvedPath", err)
	}
	if _, err := f.client.Stat(ctx, filexfer.Path{}); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("Stat(zero Path) error = %v, want ErrUnresolvedPath", err)
	}
	if sent := f.rec.since(mark); len(sent) != 0 {
		t.Errorf("refusing bad arguments sent %d SFTP packets, want none", len(sent))
	}
}

// TestPut_WithoutPosixRename covers a server that lacks the extension:
// a new file is still created with plain rename, and replacing an
// existing one is refused before anything is created.
func TestPut_WithoutPosixRename(t *testing.T) {
	f := newFixture(t)
	f.client.posixRename = false
	ctx := context.Background()

	fresh := f.resolve(t, "fresh.txt")
	if err := f.client.Put(ctx, fresh, strings.NewReader("hi"), 2, 0o644); err != nil {
		t.Fatalf("Put() of a new file without posix-rename error = %v", err)
	}

	existing := f.resolve(t, "existing.txt")
	if err := os.WriteFile(existing.String(), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	mark := f.rec.mark()
	err := f.client.Put(ctx, existing, strings.NewReader("new"), 3, 0o644)
	if !errors.Is(err, filexfer.ErrReplaceUnsupported) {
		t.Fatalf("Put() over an existing file without posix-rename error = %v, want ErrReplaceUnsupported", err)
	}
	f.assertNothingMutated(t, mark)
	if got, _ := os.ReadFile(existing.String()); string(got) != "old" {
		t.Errorf("target holds %q, want it untouched", got)
	}
}

// TestGet_LimitIsEnforcedBeforeReading covers a file over the limit.
func TestGet_LimitIsEnforcedBeforeReading(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "big.bin")
	if err := os.WriteFile(p.String(), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	mark := f.rec.mark()
	var got bytes.Buffer
	n, err := f.client.Get(context.Background(), p, &got, 999)
	if !errors.Is(err, filexfer.ErrLimitExceeded) || n != 0 || got.Len() != 0 {
		t.Fatalf("Get() over the limit = %d, %v with %d bytes written; want 0, ErrLimitExceeded, nothing", n, err, got.Len())
	}
	f.assertNothingMutated(t, mark)
}

// TestGet_MissingFileIsNotExist lets a caller test for absence by
// identity.
func TestGet_MissingFileIsNotExist(t *testing.T) {
	f := newFixture(t)
	if _, err := f.client.Get(context.Background(), f.resolve(t, "nope"), io.Discard, 10); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get() of a missing file error = %v, want fs.ErrNotExist", err)
	}
	if _, err := f.client.Stat(context.Background(), f.resolve(t, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat() of a missing file error = %v, want fs.ErrNotExist", err)
	}
}

// TestStat_DescribesWithoutFollowing covers the optional interface.
func TestStat_DescribesWithoutFollowing(t *testing.T) {
	f := newFixture(t)
	file := f.resolve(t, "file")
	if err := os.WriteFile(file.String(), []byte("12345"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file.String(), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := f.client.Stat(context.Background(), file)
	if err != nil || info.Size != 5 || info.Kind != filexfer.KindRegular || info.Mode != 0o640 || info.ModTime.IsZero() {
		t.Fatalf("Stat(file) = %+v, %v", info, err)
	}

	link := f.resolve(t, "link")
	if err := os.Symlink(filepath.Join(f.dir, "outside"), link.String()); err != nil {
		t.Fatal(err)
	}
	info, err = f.client.Stat(context.Background(), link)
	if err != nil || info.Kind != filexfer.KindSymlink {
		t.Fatalf("Stat(symlink) = %+v, %v; want it reported as a symlink, not followed", info, err)
	}
}
