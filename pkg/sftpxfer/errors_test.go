// Tests for cancellation, silent and non-SFTP peers, and the device refusing
// on its own permissions.
package sftpxfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// TestOpen_CanceledContextClosesTheChannel covers a caller whose
// deadline has already passed: nothing is sent and the channel it
// handed over is closed, since the Client would have owned it.
func TestOpen_CanceledContextClosesTheChannel(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	defer serverEnd.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, clientEnd); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(canceled) error = %v, want context.Canceled", err)
	}
	if _, err := clientEnd.Write([]byte("x")); err == nil {
		t.Error("the channel is still open after Open refused it")
	}
}

// TestOpen_SilentPeerHitsTheDeadline covers a device that accepts the
// subsystem and never answers the version handshake: the deadline, not
// the device, decides when Open gives up.
func TestOpen_SilentPeerHitsTheDeadline(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	defer serverEnd.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Open(ctx, clientEnd); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open(silent peer) error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Open took %v to give up, want about the 100ms deadline", elapsed)
	}
}

// TestOpen_NonSFTPPeerIsRefused covers a subsystem that answers with
// something other than SFTP and hangs up.
func TestOpen_NonSFTPPeerIsRefused(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go func() {
		_, _ = io.CopyN(io.Discard, serverEnd, 9) // the client's INIT
		_, _ = serverEnd.Write([]byte("not sftp\n"))
		_ = serverEnd.Close()
	}()
	if _, err := Open(context.Background(), clientEnd); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("Open(non-SFTP peer) error = %v, want a protocol failure", err)
	}
}

// TestCalls_CanceledContextSendsNothing covers begin's early refusal for
// every call.
func TestCalls_CanceledContextSendsNothing(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "f")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mark := f.rec.mark()

	if err := f.client.Put(ctx, p, strings.NewReader("x"), 1, 0o644); !errors.Is(err, context.Canceled) {
		t.Errorf("Put(canceled) error = %v", err)
	}
	if _, err := f.client.Get(ctx, p, io.Discard, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Get(canceled) error = %v", err)
	}
	if _, err := f.client.Stat(ctx, p); !errors.Is(err, context.Canceled) {
		t.Errorf("Stat(canceled) error = %v", err)
	}
	if sent := f.rec.since(mark); len(sent) != 0 {
		t.Errorf("canceled calls sent %d SFTP packets, want none", len(sent))
	}
}

// cancelingReader cancels its context on the first read and then keeps
// supplying bytes, so a transfer is mid-stream when its deadline passes.
type cancelingReader struct {
	cancel context.CancelFunc
}

// Read cancels, then fills p.
func (c *cancelingReader) Read(p []byte) (int, error) {
	c.cancel()
	for i := range p {
		p[i] = 'z'
	}
	return len(p), nil
}

// TestPut_CancelMidTransferReportsTheContext covers the deadline
// reaching a transfer already streaming: the session is closed out from
// under it, the error names the context rather than the broken pipe,
// and the target is untouched.
func TestPut_CancelMidTransferReportsTheContext(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "big.bin")
	if err := os.WriteFile(p.String(), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := f.client.Put(ctx, p, &cancelingReader{cancel: cancel}, 64<<20, 0o644)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() canceled mid-stream error = %v, want context.Canceled", err)
	}
	if got, _ := os.ReadFile(p.String()); string(got) != "old" {
		t.Errorf("target holds %d bytes after a canceled Put, want it untouched", len(got))
	}
}

// failingWriter fails every write, standing in for a caller's
// destination that goes away mid-Get.
type failingWriter struct{}

// Write always fails.
func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("destination gone") }

// TestGet_DestinationFailureIsReported covers the sink failing.
func TestGet_DestinationFailureIsReported(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "f")
	if err := os.WriteFile(p.String(), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Get(context.Background(), p, failingWriter{}, 100); err == nil || !strings.Contains(err.Error(), "destination gone") {
		t.Fatalf("Get() into a failing writer error = %v, want the writer's failure", err)
	}
}

// TestStat_RefusesAnEscape keeps Stat from being a probe outside the
// root.
func TestStat_RefusesAnEscape(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink(filepath.Join(f.dir, "outside"), filepath.Join(f.root, "out")); err != nil {
		t.Fatal(err)
	}
	_, err := f.client.Stat(context.Background(), f.resolve(t, "out/secret"))
	var cErr *filexfer.ContainmentError
	if !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentOutsideRoot {
		t.Fatalf("Stat() through a symlink out error = %v, want ContainmentOutsideRoot", err)
	}
}

// skipIfRoot skips a test that relies on permission bits being
// enforced, which they are not for the superuser.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced for root")
	}
}

// TestPermissionFailuresAreReported drives the server into refusing on
// its own filesystem permissions, which is how a real device fails, and
// checks each is reported rather than swallowed.
func TestPermissionFailuresAreReported(t *testing.T) {
	skipIfRoot(t)
	ctx := context.Background()

	t.Run("parent directory not writable", func(t *testing.T) {
		f := newFixture(t)
		ro := filepath.Join(f.root, "ro")
		if err := os.Mkdir(ro, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		err := f.client.Put(ctx, f.resolve(t, "ro/f"), strings.NewReader("x"), 1, 0o644)
		if err == nil || !strings.Contains(err.Error(), "private directory") {
			t.Fatalf("Put() into a read-only directory error = %v, want the mkdir failure", err)
		}
	})

	t.Run("file not readable", func(t *testing.T) {
		f := newFixture(t)
		p := f.resolve(t, "secret")
		if err := os.WriteFile(p.String(), []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		if _, err := f.client.Get(ctx, p, &bytes.Buffer{}, 10); err == nil || errors.Is(err, filexfer.ErrLimitExceeded) {
			t.Fatalf("Get() of an unreadable file error = %v, want the open failure", err)
		}
	})

	t.Run("directory not searchable", func(t *testing.T) {
		f := newFixture(t)
		locked := filepath.Join(f.root, "locked")
		if err := os.Mkdir(locked, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		err := f.client.Put(ctx, f.resolve(t, "locked/f"), strings.NewReader("x"), 1, 0o644)
		var cErr *filexfer.ContainmentError
		if err == nil || errors.As(err, &cErr) {
			t.Fatalf("Put() under an unsearchable directory error = %v, want the permission failure itself", err)
		}
		deeper := f.client.Put(ctx, f.resolve(t, "locked/sub/f"), strings.NewReader("x"), 1, 0o644)
		if deeper == nil || errors.As(deeper, &cErr) {
			t.Fatalf("Put() below an unsearchable directory error = %v, want the permission failure itself", deeper)
		}
	})
}
