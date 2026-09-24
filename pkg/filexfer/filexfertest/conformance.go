// The release-gate conformance suite every filexfer.Store runs against a real
// device: round trips, modes, size and limit refusals.
package filexfertest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// Harness is what a filexfer.Store implementation's release gate hands
// Run: a way to reach a real device, and a way to inspect that device
// that does not go through the Store under test.
type Harness struct {
	// Name labels the protocol in failure messages.
	Name string

	// Open returns a Store connected to the device, directly when hop is
	// false and through a one-hop bastion chain when it is true. The
	// Store must be closed by t's cleanup.
	Open func(t *testing.T, hop bool) filexfer.Store

	// Root is the transfer root on the device. Run creates it fresh
	// through Remote before any subtest.
	Root string

	// Outside is a directory on the device beside Root, never inside
	// it, that the escape tests aim at.
	Outside string

	// Remote runs a POSIX shell script on the device as root through a
	// path independent of the Store (a container exec, never the
	// protocol under test) and returns its output, or an error for a
	// non-zero exit. It is how a test plants a trap and checks the
	// aftermath. It must be safe to call from several goroutines.
	Remote func(ctx context.Context, script string) (string, error)

	// AbortLeavesPrivateDir is true for a protocol whose Put, when its
	// session is killed mid-stream, cannot remove its own private
	// temporary directory (SFTP: the cleanup requests need the session
	// the abort just closed). The target is intact either way; this only
	// says whether an empty 0700 directory may remain.
	AbortLeavesPrivateDir bool
}

// Run is the conformance suite every filexfer.Store runs against a real
// device. Each subtest checks the device through Remote, never through
// the Store alone, so a Store that agreed with itself while writing the
// wrong bytes would still fail.
func Run(t *testing.T, h Harness) {
	remote(t, h, fmt.Sprintf("rm -rf %s %s && mkdir -p %s %s && chown -R %s %s %s",
		quote(h.Root), quote(h.Outside), quote(h.Root), quote(h.Outside),
		"1000:1000", quote(h.Root), quote(h.Outside)))

	t.Run("RoundTrip", func(t *testing.T) { roundTrip(t, h, false) })
	t.Run("RoundTripThroughOneHop", func(t *testing.T) { roundTrip(t, h, true) })
	t.Run("ModeIsExact", func(t *testing.T) { modeIsExact(t, h) })
	t.Run("SizeMismatchLeavesTargetUntouched", func(t *testing.T) { sizeMismatch(t, h) })
	t.Run("GetLimitAndAbsence", func(t *testing.T) { getLimitAndAbsence(t, h) })
	t.Run("LexicalEscapeNeverReachesTheDevice", func(t *testing.T) { lexicalEscape(t, h) })
	t.Run("PhysicalEscapeIsRefusedBeforeAnyContent", func(t *testing.T) { physicalEscape(t, h) })
	t.Run("ReplaceKeepsReadersWhole", func(t *testing.T) { replaceKeepsReadersWhole(t, h) })
	t.Run("AbortMidStreamLeavesTargetIntact", func(t *testing.T) { abortMidStream(t, h) })
	t.Run("StreamsALargeFileInBoundedMemory", func(t *testing.T) { largeFile(t, h) })
}

// remote runs script through h.Remote and fails t on an error. Call it
// only from the test's own goroutine.
func remote(t *testing.T, h Harness, script string) string {
	t.Helper()
	out, err := h.Remote(context.Background(), script)
	if err != nil {
		t.Fatalf("%s: device script failed: %v", h.Name, err)
	}
	return out
}

// quote single-quotes s for the device's shell, the same grammar
// pkg/remoteexec.QuoteArg uses (duplicated rather than imported so this
// package's only dependency on the code under test is the port).
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// resolve builds a Path under h.Root, failing t on a refusal.
func resolve(t *testing.T, h Harness, leaf string) filexfer.Path {
	t.Helper()
	p, err := filexfer.Resolve(h.Root, leaf)
	if err != nil {
		t.Fatalf("Resolve(%q, %q) error = %v", h.Root, leaf, err)
	}
	return p
}

// remoteHash returns the SHA-256 of a file on the device as the device
// computes it.
func remoteHash(t *testing.T, h Harness, p string) string {
	t.Helper()
	fields := strings.Fields(remote(t, h, "sha256sum "+quote(p)))
	if len(fields) == 0 {
		t.Fatalf("sha256sum of %s printed nothing", p)
	}
	return fields[0]
}

// hashOf returns the SHA-256 of b in sha256sum's format.
func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// payloads are the round-trip contents: every byte value, runs of NUL,
// content that looks like SCP's own wire protocol (a C record, an ack
// byte, an error byte), an empty file, and sizes either side of the
// 32 KiB packet and copy-buffer size several layers use.
func payloads() map[string][]byte {
	var binary bytes.Buffer
	for i := 0; i < 256; i++ {
		binary.WriteByte(byte(i))
	}
	binary.Write(make([]byte, 4096))
	binary.WriteString("C0644 5 x\n\x00\x01scp: forged\n\x02E\nD0755 0 d\n")
	random := make([]byte, 100_000)
	// crypto/rand rather than a seeded generator: the content only has
	// to be unpredictable enough that no layer could be compressing,
	// caching or pattern-matching it, and it is compared with itself.
	_, _ = rand.Read(random)
	return map[string][]byte{
		"empty":         {},
		"binary":        binary.Bytes(),
		"all-nul":       make([]byte, 65536),
		"packet-minus":  bytes.Repeat([]byte{0x5A}, 32767),
		"packet":        bytes.Repeat([]byte{0x5B}, 32768),
		"packet-plus":   bytes.Repeat([]byte{0x5C}, 32769),
		"random-100k":   random,
		"single-byte":   {0x00},
		"trailing-nl":   []byte("line\n"),
		"no-trailing":   []byte("line"),
		"scp-lookalike": []byte("\x00\x00\x00C0644 1 x\n"),
	}
}

// roundTrip puts every payload, checks the device's own hash and mode,
// gets it back, and compares byte for byte.
func roundTrip(t *testing.T, h Harness, hop bool) {
	store := h.Open(t, hop)
	ctx := context.Background()
	for name, content := range payloads() {
		leaf := "rt-" + name
		if hop {
			leaf = "hop-" + name
		}
		p := resolve(t, h, leaf)
		if err := store.Put(ctx, p, bytes.NewReader(content), int64(len(content)), 0o640); err != nil {
			t.Fatalf("%s: Put(%s) error = %v", h.Name, name, err)
		}
		if got, want := remoteHash(t, h, p.String()), hashOf(content); got != want {
			t.Fatalf("%s: %s on the device hashes to %s, want %s", h.Name, name, got, want)
		}
		if mode := strings.TrimSpace(remote(t, h, "stat -c %a "+quote(p.String()))); mode != "640" {
			t.Errorf("%s: %s has mode %s on the device, want 640", h.Name, name, mode)
		}
		var got bytes.Buffer
		n, err := store.Get(ctx, p, &got, int64(len(content)))
		if err != nil || n != int64(len(content)) || !bytes.Equal(got.Bytes(), content) {
			t.Fatalf("%s: Get(%s) = %d bytes, %v; want the %d bytes put", h.Name, name, n, err, len(content))
		}
	}
	assertNoPrivateDirs(t, h)
}

// modeIsExact proves the mode does not depend on the account's umask.
func modeIsExact(t *testing.T, h Harness) {
	store := h.Open(t, false)
	for _, mode := range []filexfer.Mode{0o600, 0o644, 0o755, 0o777, 0o400} {
		p := resolve(t, h, "mode-"+mode.String())
		if err := store.Put(context.Background(), p, strings.NewReader("m"), 1, mode); err != nil {
			t.Fatalf("%s: Put(mode %s) error = %v", h.Name, mode, err)
		}
		got := strings.TrimSpace(remote(t, h, "stat -c %a "+quote(p.String())))
		if want := strings.TrimLeft(mode.String(), "0"); got != want {
			t.Errorf("%s: mode on the device = %s, want %s", h.Name, got, want)
		}
	}
}

// sizeMismatch is "exactly size bytes or nothing" against a real device.
func sizeMismatch(t *testing.T, h Harness) {
	store := h.Open(t, false)
	p := resolve(t, h, "keep.txt")
	if err := store.Put(context.Background(), p, strings.NewReader("original"), 8, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		src  io.Reader
		size int64
	}{
		"short": {strings.NewReader("abc"), 10},
		"long":  {bytes.NewReader(make([]byte, 300_000)), 200_000},
	} {
		err := store.Put(context.Background(), p, tc.src, tc.size, 0o644)
		if !errors.Is(err, filexfer.ErrSizeMismatch) {
			t.Errorf("%s: %s Put() error = %v, want ErrSizeMismatch", h.Name, name, err)
		}
	}
	if got := remote(t, h, "cat "+quote(p.String())); got != "original" {
		t.Errorf("%s: target holds %q after failed Puts, want it untouched", h.Name, got)
	}
	assertNoPrivateDirs(t, h)
}

// getLimitAndAbsence covers Get's two refusals.
func getLimitAndAbsence(t *testing.T, h Harness) {
	store := h.Open(t, false)
	p := resolve(t, h, "limit.bin")
	remote(t, h, "head -c 1000 /dev/zero > "+quote(p.String()))
	var got bytes.Buffer
	if _, err := store.Get(context.Background(), p, &got, 999); !errors.Is(err, filexfer.ErrLimitExceeded) || got.Len() != 0 {
		t.Errorf("%s: Get() over the limit error = %v with %d bytes written; want ErrLimitExceeded and nothing", h.Name, err, got.Len())
	}
	if _, err := store.Get(context.Background(), resolve(t, h, "absent"), &got, 10); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: Get() of a missing file error = %v, want fs.ErrNotExist", h.Name, err)
	}
}

// assertNoPrivateDirs fails t if a Put left a private directory in the
// root.
func assertNoPrivateDirs(t *testing.T, h Harness) {
	t.Helper()
	out := remote(t, h, "find "+quote(h.Root)+" -name '.pleiades-xfer-*' -print")
	if strings.TrimSpace(out) != "" {
		t.Errorf("%s: private directories were left behind:\n%s", h.Name, out)
	}
}
