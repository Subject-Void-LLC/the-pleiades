//go:build !windows

// Tests for a command whose context ends (conn_watch.go): nothing is sent on
// a context already done, a failed read of standard input is never sent as
// its end, and a silent connection cannot hold a cancelled call past the
// abandon grace, whether it waits on a session open or on a standard input
// copy the remote stopped granting window for. That a cancelled command never
// comes back as a completed one needs a server that reports a status after
// the client's close, which this package's in-process server never does:
// internal/transport/ssh proves it against a real OpenSSH sshd.
package remoteexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// connectThroughProxy connects to srv through a stalling proxy and returns
// the connection and the proxy's freeze.
func connectThroughProxy(t *testing.T) (*Conn, func()) {
	t.Helper()
	srv := startPoolServer(t)
	port, freeze := stallingProxy(t, srv.Addr())
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, Target{Host: "127.0.0.1", Port: port}, PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, freeze
}

// TestRun_AContextAlreadyDoneSendsNothing: a context that ended before Run is
// refused before any session opens, so the command never runs (the marker it
// would write stays absent), on the attached path and the detached one.
func TestRun_AContextAlreadyDoneSendsNothing(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"attached", ctx},
		{"detached", WithDetach(ctx)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ran")
			_, err := conn.RunWithStdin(tc.ctx, "touch "+QuoteArg(marker), nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run = %v; want context.Canceled", err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatal("the command ran on a context that was already done")
			}
		})
	}
	if got := srv.Commands(); len(got) != 0 {
		t.Fatalf("the server received %d commands, want none: %q", len(got), got)
	}
}

// TestRunWithStdin_AFailedReadIsNotAnEndOfInput: a reader failing mid-stream
// fails the call with that read error, rather than sending an end of input
// and reporting the command's 0 for the prefix it got.
func TestRunWithStdin_AFailedReadIsNotAnEndOfInput(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	broken := errors.New("disk read failed")
	in := io.MultiReader(strings.NewReader("first half\n"), &failingReader{err: broken})
	res, err := conn.RunWithStdin(context.Background(), "cat", in)
	if !errors.Is(err, broken) {
		t.Fatalf("RunWithStdin = %+v, %v; want the read error", res, err)
	}
}

// failingReader fails every read with err.
type failingReader struct{ err error }

// Read fails.
func (r *failingReader) Read([]byte) (int, error) { return 0, r.err }

// TestRunWithStdin_ACopyTheRemoteStoppedGrantingIsBounded: a cancelled
// command whose server stops answering while a large standard input is still
// being copied returns within the abandon grace: the copy is blocked waiting
// for window the server never grants, and only closing the connection ends
// it.
func TestRunWithStdin_ACopyTheRemoteStoppedGrantingIsBounded(t *testing.T) {
	conn, freeze := connectThroughProxy(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.RunWithStdin(ctx, "trap '' TERM; sleep 60", bytes.NewReader(make([]byte, 8<<20)))
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	freeze()
	began := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunWithStdin = %v; want context.Canceled", err)
		}
		if took := time.Since(began); took > abandonGrace+3*time.Second {
			t.Fatalf("RunWithStdin took %s after its cancel; want about the abandon grace (%s)", took, abandonGrace)
		}
	case <-time.After(abandonGrace + 15*time.Second):
		t.Fatal("RunWithStdin never returned: its standard input copy was waited on")
	}
}

// TestRun_ASilentConnectionCannotHoldASessionOpen: on a connection that stopped
// answering before the command (a pooled one whose NAT dropped the flow),
// opening the session waits on a reply that never comes; the cancellation
// still returns within the abandon grace.
func TestRun_ASilentConnectionCannotHoldASessionOpen(t *testing.T) {
	conn, freeze := connectThroughProxy(t)
	freeze()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(ctx, "true")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run = %v; want context.DeadlineExceeded", err)
		}
	case <-time.After(abandonGrace + 10*time.Second):
		t.Fatal("Run never returned: the session open was waited on")
	}
}

// TestRun_ADetachedCommandHoldingANULIsRefused: a NUL is refused before the
// detached path sends anything, as wire refuses it on the attached one.
func TestRun_ADetachedCommandHoldingANULIsRefused(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.Run(WithDetach(context.Background()), "echo a\x00; touch /tmp/b")
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("Run = %v; want the NUL refused", err)
	}
	if got := srv.Commands(); len(got) != 0 {
		t.Fatalf("the server received %d commands, want none: %q", len(got), got)
	}
}
