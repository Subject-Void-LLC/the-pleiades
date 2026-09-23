// Tests for Process, against a real shell behind a real SSH exec channel.
package remoteexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// startShellServer starts remoteexectest's in-process SSH server, which
// runs every exec request through a real /bin/sh with its standard
// input and output wired straight to the channel, and connects to it
// through the real Runner.Connect path. A Process test therefore
// exercises a genuine exec request against a genuine shell rather than
// a handler that pattern-matches on the command string.
//
// Host key verification is skipped here because what is under test is
// the stream a Process carries, not the dial; knownhosts_test.go and
// runner_test.go own the dial's own guarantees.
func startShellServer(t *testing.T, opts remoteexectest.Options) (*remoteexectest.Server, *Conn) {
	t.Helper()
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the in-process SSH server: %v", err)
	}
	t.Cleanup(srv.Close)

	runner := New(Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(context.Background(), nil,
		Target{Host: srv.Host, Port: srv.Port}, PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("connecting to the in-process SSH server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, conn
}

// closeStdin sends end-of-file on a started command's standard input,
// which is what a caller does for a command that reads none: the
// harness's shell, like os/exec generally, does not report a command's
// exit until its standard input has ended.
func closeStdin(t *testing.T, proc *Process) {
	t.Helper()
	if err := proc.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() error = %v", err)
	}
}

// TestProcess_SatisfiesReadWriteCloser pins the property a protocol
// package driving a Process depends on: it can be handed to anything
// that speaks io.ReadWriteCloser, and be tested over an in-memory pipe
// through the same code path.
func TestProcess_SatisfiesReadWriteCloser(t *testing.T) {
	var _ io.ReadWriteCloser = (*Process)(nil)
}

// TestProcess_ConversesWhileTheCommandRuns is the property RunWithStdin
// cannot provide and the reason Process exists: a reply is read back
// while the command is still running and its standard input still
// open, which is the shape every acknowledgement protocol (legacy SCP's
// first of all) needs.
func TestProcess_ConversesWhileTheCommandRuns(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	proc, err := conn.Start(context.Background(), "cat")
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	defer proc.Close()

	for _, line := range []string{"first\n", "second\n"} {
		if _, err := io.WriteString(proc, line); err != nil {
			t.Fatalf("Write(%q) error = %v", line, err)
		}
		got := make([]byte, len(line))
		if _, err := io.ReadFull(proc, got); err != nil {
			t.Fatalf("reading %q back while the command still runs: %v", line, err)
		}
		if string(got) != line {
			t.Errorf("read back %q, want %q", got, line)
		}
	}

	if err := proc.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() error = %v", err)
	}
	if rest, err := io.ReadAll(proc); err != nil || len(rest) != 0 {
		t.Fatalf("ReadAll() after CloseWrite = %q, %v; want no more output and a clean end", rest, err)
	}
	if code, err := proc.Wait(); err != nil || code != 0 {
		t.Errorf("Wait() = %d, %v; want 0, nil once cat saw end-of-file", code, err)
	}
}

// TestProcess_WaitReportsNonZeroExitAsStatusNotError pins the same rule
// Conn.Run follows: a command that ran and failed is information.
func TestProcess_WaitReportsNonZeroExitAsStatusNotError(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	proc, err := conn.Start(context.Background(), "exit 7")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proc.Close()
	closeStdin(t, proc)

	if _, err := io.ReadAll(proc); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	code, err := proc.Wait()
	if err != nil || code != 7 {
		t.Fatalf("Wait() = %d, %v; want 7, nil", code, err)
	}
	// ssh.Session.Wait would block forever on a second call; the cached
	// answer is what makes calling Wait twice safe.
	if again, err := proc.Wait(); err != nil || again != 7 {
		t.Errorf("second Wait() = %d, %v; want the same 7, nil", again, err)
	}
}

// TestProcess_StderrIsSeparateFromStdout proves the two streams are not
// merged, which a protocol reading acknowledgement bytes off standard
// output depends on: one diagnostic line on stdout would corrupt it.
func TestProcess_StderrIsSeparateFromStdout(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	proc, err := conn.Start(context.Background(), "printf out; printf err >&2; exit 3")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	closeStdin(t, proc)
	out, err := io.ReadAll(proc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if code, err := proc.Wait(); err != nil || code != 3 {
		t.Fatalf("Wait() = %d, %v; want 3, nil", code, err)
	}
	proc.Close()

	if string(out) != "out" {
		t.Errorf("stdout = %q, want %q", out, "out")
	}
	if got := proc.Stderr(); got != "err" {
		t.Errorf("Stderr() = %q, want %q", got, "err")
	}
}

// TestProcess_StderrIsBoundedAndMarkedTruncated shares its bound with
// Subsystem, since both are the one stream underneath.
func TestProcess_StderrIsBoundedAndMarkedTruncated(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	proc, err := conn.Start(context.Background(), "head -c 40000 /dev/zero | tr '\\000' x >&2")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	closeStdin(t, proc)
	if _, err := io.ReadAll(proc); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	proc.Close()

	got := proc.Stderr()
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("Stderr() of %d bytes is not marked truncated", len(got))
	}
	if body := strings.TrimSuffix(got, "... (truncated)"); len(body) != maxStreamStderrBytes {
		t.Errorf("Stderr() retained %d bytes, want exactly %d", len(body), maxStreamStderrBytes)
	}
}

// TestProcess_ContextGovernsTheWholeSession proves a deadline reaches a
// command that has decided to run forever, through Read and Wait alike.
func TestProcess_ContextGovernsTheWholeSession(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := conn.Start(ctx, "sleep 3")
	if err != nil {
		cancel()
		t.Fatalf("Start() error = %v", err)
	}
	defer proc.Close()

	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	if _, err := io.ReadFull(proc, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel = %v, want it to wrap context.Canceled", err)
	}
	if code, err := proc.Wait(); !errors.Is(err, context.Canceled) || code != -1 {
		t.Errorf("Wait() after cancel = %d, %v; want -1 and an error wrapping context.Canceled", code, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancel took %v to reach a sleeping command, want well under its 3s", elapsed)
	}
}

// TestProcess_RefusedSessionIsAnError reaches the branch where the
// connection is healthy and the server refuses the session channel
// itself, a real protocol-level refusal rather than an injected error.
func TestProcess_RefusedSessionIsAnError(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{SessionLimit: remoteexectest.Limit(0)})

	proc, err := conn.Start(context.Background(), "true")
	if err == nil {
		proc.Close()
		t.Fatal("Start() on a server refusing every session error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("Start() error = %q, want it to name the failed session open", err)
	}
}

// TestProcess_ErrorsNeverCarryTheCommandText pins a deliberate choice:
// a started command can hold a runbook-authored remote path, and an
// error message is logged and shown where that value does not belong.
func TestProcess_ErrorsNeverCarryTheCommandText(t *testing.T) {
	_, conn := startShellServer(t, remoteexectest.Options{})

	const marker = "/srv/secret-looking-path"
	proc, err := conn.Start(context.Background(), "cat "+QuoteArg(marker))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	proc.Close()

	_, err = io.WriteString(proc, "anything")
	if err == nil {
		t.Fatal("Write() to a closed process error = nil, want a refusal")
	}
	if strings.Contains(err.Error(), marker) {
		t.Errorf("Write() error = %q, want it to name the stream without the command text", err)
	}
	if !strings.Contains(err.Error(), "remote command") {
		t.Errorf("Write() error = %q, want it to say it was a remote command", err)
	}
	if err := proc.CloseWrite(); err == nil {
		t.Error("CloseWrite() on a closed process error = nil, want a refusal")
	}
}

// TestProcess_StreamsInBoundedMemory is the property that makes a
// multi-gigabyte image transferable at all: 64 MiB crosses a real SSH
// exec channel through a real cat and back, generated on the way in and
// hashed on the way out, and this process's heap never grows by as much
// as half the payload. A Process that buffered either direction would
// need at least one whole copy of the 64 MiB resident at once.
//
// The bound is deliberately half the payload rather than a tight
// number. HeapInuse counts garbage the collector has not reclaimed yet
// alongside live data, so a tight bound would measure the collector's
// pacing and whatever the rest of the package run left behind (it did:
// 25.8 MB against a 24 MiB bound in a full run, while passing alone).
// Half the payload is what separates streaming from buffering, which is
// the claim. The collector is also run more eagerly for the duration so
// garbage stays small next to that bound.
func TestProcess_StreamsInBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 64 MiB through a real shell")
	}
	_, conn := startShellServer(t, remoteexectest.Options{})

	const payload = 64 << 20
	const bound = payload / 2

	defer debug.SetGCPercent(debug.SetGCPercent(25))

	proc, err := conn.Start(context.Background(), "cat")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proc.Close()

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var sampling sync.WaitGroup
	sampling.Add(1)
	go func() {
		defer sampling.Done()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()

	sent := sha256.New()
	writeErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(proc, io.TeeReader(io.LimitReader(patternReader{}, payload), sent))
		if err == nil {
			err = proc.CloseWrite()
		}
		writeErr <- err
	}()

	received := sha256.New()
	n, err := io.Copy(received, proc)
	close(stop)
	sampling.Wait()
	if err != nil {
		t.Fatalf("reading the stream back: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("writing the stream: %v", err)
	}
	if n != payload {
		t.Fatalf("read back %d bytes, want %d", n, payload)
	}
	if !bytes.Equal(sent.Sum(nil), received.Sum(nil)) {
		t.Fatal("the bytes read back differ from the bytes sent")
	}
	if code, err := proc.Wait(); err != nil || code != 0 {
		t.Fatalf("Wait() = %d, %v; want 0, nil", code, err)
	}

	if grew := int64(peak.Load()) - int64(base.HeapInuse); grew > bound {
		t.Errorf("heap in use grew by %d bytes while streaming %d, want at most %d", grew, payload, bound)
	}
}

// patternReader is an endless deterministic byte source, so a large
// payload is generated as it is read and never exists in memory whole.
// Every byte value appears, including NUL, so nothing along the way can
// be treating the stream as text.
type patternReader struct{}

// Read fills p with a repeating 0 to 255 byte ramp.
func (patternReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i)
	}
	return len(p), nil
}

// TestProcess_LeavesNoGoroutinesBehind covers the two goroutines each
// stream starts (the context watchdog and the standard error drain),
// following subsystem_test.go's local-snapshot goleak convention.
func TestProcess_LeavesNoGoroutinesBehind(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the in-process SSH server: %v", err)
	}
	defer srv.Close()
	runner := New(Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(context.Background(), nil,
		Target{Host: srv.Host, Port: srv.Port}, PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	leakOpts := goleak.IgnoreCurrent()

	proc, err := conn.Start(context.Background(), "cat")
	if err != nil {
		conn.Close()
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := io.WriteString(proc, "ping"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := io.ReadFull(proc, make([]byte, 4)); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	proc.Close()

	goleak.VerifyNone(t, leakOpts)
	conn.Close()
}
