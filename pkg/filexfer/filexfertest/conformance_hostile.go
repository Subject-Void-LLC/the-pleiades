// The conformance suite's hostile half: escapes, atomic replacement under a
// concurrent reader, an abort mid-stream, and a large file in bounded memory.
package filexfertest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// deviceSnapshot lists every entry under the root and the outside
// directory with its type, size and content hash, as the device sees
// them, so a before and after comparison shows any change at all.
func deviceSnapshot(t *testing.T, h Harness) string {
	t.Helper()
	return remote(t, h, fmt.Sprintf(
		"cd / && find %s %s -printf '%%p %%y %%s\\n' | sort && find %s %s -type f -exec sha256sum {} + | sort",
		quote(h.Root), quote(h.Outside), quote(h.Root), quote(h.Outside)))
}

// lexicalEscape runs every shared payload. None can become a Path, so
// the only thing a caller could hand the Store is the zero Path, which
// every Store refuses; the device is unchanged afterward.
func lexicalEscape(t *testing.T, h Harness) {
	store := h.Open(t, false)
	before := deviceSnapshot(t, h)
	for _, payload := range EscapePayloads {
		p, err := filexfer.Resolve(h.Root, payload.Leaf)
		if err == nil {
			t.Fatalf("%s: Resolve accepted the %q payload %q", h.Name, payload.Name, payload.Leaf)
		}
		if err := store.Put(context.Background(), p, strings.NewReader("x"), 1, 0o644); !errors.Is(err, filexfer.ErrUnresolvedPath) {
			t.Fatalf("%s: Put() with a refused Path error = %v, want ErrUnresolvedPath", h.Name, err)
		}
		if _, err := store.Get(context.Background(), p, io.Discard, 1); !errors.Is(err, filexfer.ErrUnresolvedPath) {
			t.Fatalf("%s: Get() with a refused Path error = %v, want ErrUnresolvedPath", h.Name, err)
		}
	}
	if after := deviceSnapshot(t, h); after != before {
		t.Errorf("%s: the device changed during refused escapes:\nbefore:\n%s\nafter:\n%s", h.Name, before, after)
	}
}

// physicalEscape plants the traps a lexical guard cannot see, each an
// ordinary name the device's own filesystem redirects, and proves each
// is refused with the outside file unchanged, no private directory
// left, and nothing written to a Get's destination.
func physicalEscape(t *testing.T, h Harness) {
	store := h.Open(t, false)
	keys := path.Join(h.Outside, "authorized_keys")
	remote(t, h, fmt.Sprintf(
		"printf 'ssh-ed25519 AAAA original' > %[1]s && chown 1000:1000 %[1]s && chmod 600 %[1]s && "+
			"cd %[2]s && ln -s %[3]s abs-out && ln -s ../%[4]s rel-out && ln -s %[1]s keys && "+
			"mkdir adir && mkfifo fifo && mkdir -p deep/er && ln -s ../../../%[4]s deep/er/out && "+
			"chown -h -R 1000:1000 %[2]s",
		quote(keys), quote(h.Root), quote(h.Outside), quote(path.Base(h.Outside))))
	before := deviceSnapshot(t, h)

	cases := []struct {
		leaf string
		want filexfer.Containment
	}{
		{"abs-out/authorized_keys", filexfer.ContainmentOutsideRoot},
		{"rel-out/authorized_keys", filexfer.ContainmentOutsideRoot},
		{"deep/er/out/authorized_keys", filexfer.ContainmentOutsideRoot},
		{"keys", filexfer.ContainmentSymlinkLeaf},
		{"adir", filexfer.ContainmentDirectoryLeaf},
		{"fifo", filexfer.ContainmentNotRegular},
	}
	for _, tc := range cases {
		p := resolve(t, h, tc.leaf)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		putErr := store.Put(ctx, p, strings.NewReader("attacker key"), 12, 0o600)
		var sink bytes.Buffer
		_, getErr := store.Get(ctx, p, &sink, 1<<20)
		cancel()

		var cErr *filexfer.ContainmentError
		if !errors.As(putErr, &cErr) || cErr.Reason != tc.want {
			t.Errorf("%s: Put(%s) error = %v, want %v", h.Name, tc.leaf, putErr, tc.want)
		}
		if !errors.As(getErr, &cErr) || cErr.Reason != tc.want {
			t.Errorf("%s: Get(%s) error = %v, want %v", h.Name, tc.leaf, getErr, tc.want)
		}
		if sink.Len() != 0 {
			t.Errorf("%s: Get(%s) wrote %d bytes before refusing", h.Name, tc.leaf, sink.Len())
		}
	}
	if after := deviceSnapshot(t, h); after != before {
		t.Errorf("%s: the device changed during refused physical escapes:\nbefore:\n%s\nafter:\n%s", h.Name, before, after)
	}
	remote(t, h, fmt.Sprintf("cd %s && rm -rf abs-out rel-out keys adir fifo deep", quote(h.Root)))
}

// replaceKeepsReadersWhole replaces one file over and over while a
// reader on the device hashes it in a tight loop, in one long-running
// device-side script so the reads are not throttled by a container exec
// each. Every hash the reader sees must be one of the versions written,
// never a partial file, and the two must genuinely overlap: the test
// fails if the reader finished before most replaces had happened.
func replaceKeepsReadersWhole(t *testing.T, h Harness) {
	store := h.Open(t, false)
	p := resolve(t, h, "atomic.bin")
	const versions, size, reads = 6, 256 << 10, 400
	written := map[string]bool{}
	contents := make([][]byte, versions)
	for i := range contents {
		contents[i] = bytes.Repeat([]byte{byte('a' + i)}, size)
		written[hashOf(contents[i])] = true
	}
	if err := store.Put(context.Background(), p, bytes.NewReader(contents[0]), size, 0o644); err != nil {
		t.Fatal(err)
	}

	var out string
	var readerErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, readerErr = h.Remote(context.Background(), fmt.Sprintf(
			"i=0; while [ $i -lt %d ]; do sha256sum %s | cut -c1-64; i=$((i+1)); done",
			reads, quote(p.String())))
	}()

	replaces := 0
	for writing := true; writing; {
		select {
		case <-done:
			writing = false
		default:
			v := contents[replaces%versions]
			if err := store.Put(context.Background(), p, bytes.NewReader(v), size, 0o644); err != nil {
				<-done
				t.Fatalf("%s: Put(replace %d) error = %v", h.Name, replaces, err)
			}
			replaces++
		}
	}

	if readerErr != nil {
		t.Fatalf("%s: the device-side reader failed, which a missing file mid-replace would cause: %v", h.Name, readerErr)
	}
	seen := strings.Fields(out)
	if len(seen) != reads {
		t.Fatalf("%s: the device-side reader reported %d hashes, want %d", h.Name, len(seen), reads)
	}
	if replaces < 20 {
		t.Fatalf("%s: only %d replaces overlapped %d reads, too few to show anything", h.Name, replaces, reads)
	}
	for _, sum := range seen {
		if !written[sum] {
			t.Fatalf("%s: a reader saw a file matching no version written (%s), a partial replace", h.Name, sum)
		}
	}
	t.Logf("%s: %d device-side reads overlapped %d replaces, every one a whole version", h.Name, reads, replaces)
}

// cancelAfter cancels its context once it has served limit bytes, then
// keeps serving, so a Put is mid-stream when its session is closed.
type cancelAfter struct {
	cancel context.CancelFunc
	limit  int64
	served int64
}

// Read serves 'q' bytes, canceling once past the limit.
func (c *cancelAfter) Read(p []byte) (int, error) {
	if c.served >= c.limit {
		c.cancel()
	}
	for i := range p {
		p[i] = 'q'
	}
	c.served += int64(len(p))
	return len(p), nil
}

// abortMidStream kills a Put mid-stream and checks the target still
// holds its old content and, where the protocol can promise it, that no
// private directory is left.
func abortMidStream(t *testing.T, h Harness) {
	store := h.Open(t, false)
	p := resolve(t, h, "abort.txt")
	if err := store.Put(context.Background(), p, strings.NewReader("original"), 8, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := store.Put(ctx, p, &cancelAfter{cancel: cancel, limit: 1 << 20}, 256<<20, 0o644)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%s: Put() killed mid-stream error = %v, want context.Canceled", h.Name, err)
	}
	// The device finishes its own side asynchronously (SCP's script runs
	// its cleanup trap when its input ends), so the checks poll briefly.
	deadline := time.Now().Add(10 * time.Second)
	for {
		content := remote(t, h, "cat "+quote(p.String()))
		leftover := strings.TrimSpace(remote(t, h, "find "+quote(h.Root)+" -name '.pleiades-xfer-*' -print"))
		if content == "original" && (leftover == "" || h.AbortLeavesPrivateDir) {
			if leftover != "" {
				perm := remote(t, h, "stat -c %a "+leftover)
				if strings.TrimSpace(perm) != "700" {
					t.Errorf("%s: an abort left %s with mode %s, want a private 700 directory", h.Name, leftover, perm)
				}
				remote(t, h, "rm -rf "+leftover)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: after an abort the target holds %d bytes and %q was left behind", h.Name, len(content), leftover)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// largeFile moves 256 MiB each way, hashing on the fly on both ends, and
// asserts the heap never grew by as much as a quarter of it: a Store
// that buffered either direction would need the whole file resident.
func largeFile(t *testing.T, h Harness) {
	if testing.Short() {
		t.Skip("moves 256 MiB each way")
	}
	const size = 256 << 20
	const bound = size / 4
	store := h.Open(t, false)
	p := resolve(t, h, "large.bin")

	defer debug.SetGCPercent(debug.SetGCPercent(25))
	sent := sha256.New()
	// crypto/rand is an endless stream, so the payload is produced as it
	// is read and never exists in memory whole.
	src := io.TeeReader(io.LimitReader(rand.Reader, size), sent)
	grewPut := peakHeapGrowth(func() {
		if err := store.Put(context.Background(), p, src, size, 0o644); err != nil {
			t.Fatalf("%s: Put(256 MiB) error = %v", h.Name, err)
		}
	})
	want := hex.EncodeToString(sent.Sum(nil))
	if got := remoteHash(t, h, p.String()); got != want {
		t.Fatalf("%s: the 256 MiB file hashes to %s on the device, want %s", h.Name, got, want)
	}

	received := sha256.New()
	var n int64
	grewGet := peakHeapGrowth(func() {
		var err error
		n, err = store.Get(context.Background(), p, hashingDiscard{received}, size)
		if err != nil {
			t.Fatalf("%s: Get(256 MiB) error = %v", h.Name, err)
		}
	})
	if n != size || hex.EncodeToString(received.Sum(nil)) != want {
		t.Fatalf("%s: Get returned %d bytes hashing differently from what was put", h.Name, n)
	}
	t.Logf("%s: peak heap growth moving %d MiB: put %d KiB, get %d KiB", h.Name, size>>20, grewPut>>10, grewGet>>10)
	for dir, grew := range map[string]uint64{"put": grewPut, "get": grewGet} {
		if grew > bound {
			t.Errorf("%s: %s grew the heap by %d bytes, want at most %d (streaming, not buffering)", h.Name, dir, grew, bound)
		}
	}
	remote(t, h, "rm -f "+quote(p.String()))
}

// hashingDiscard hashes what it is written and keeps none of it.
type hashingDiscard struct{ h hash.Hash }

// Write hashes p.
func (d hashingDiscard) Write(p []byte) (int, error) { return d.h.Write(p) }

// peakHeapGrowth runs fn while sampling HeapInuse, and returns the peak
// growth over the heap in use when it started, or zero if the heap
// never grew past that.
func peakHeapGrowth(fn func()) uint64 {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var sampling sync.WaitGroup
	sampling.Go(func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	})
	fn()
	close(stop)
	sampling.Wait()
	if p := peak.Load(); p > base.HeapInuse {
		return p - base.HeapInuse
	}
	return 0
}
