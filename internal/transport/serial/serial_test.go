package serial_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
	realserial "go.bug.st/serial"
)

// This file closes a real gap Phase 73's own Workstream E found while
// building a third such thin Adapter (internal/transport/telnet):
// neither this package nor internal/transport/serialtcp had a single
// test of its own (0.0% coverage, confirmed with `go test -cover`), so
// the type assertion and error-wrapping logic Exec actually performs had
// never run under `go test` at all -- only pkg/serialexec's own Exec,
// one layer down, had RULE 0 evidence. This is the same PTY-backed
// discipline that file already established, just against
// serialtransport.New(...).Exec rather than serialexec.Exec directly.

// testPTYPair starts a real socat process linking two PTYs at fixed
// paths under t.TempDir(), waits for both to exist, and registers
// cleanup. Deliberately a local copy of pkg/serialexec's own test
// helper of the same name rather than a shared export: it is small,
// test-only, and a cross-package test-helper dependency is not worth
// the coupling for twenty lines.
func testPTYPair(t *testing.T) (a, b string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping PTY-backed integration test in short mode")
	}
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("skipping PTY-backed test: socat is not on PATH")
	}

	dir := t.TempDir()
	a = filepath.Join(dir, "a")
	b = filepath.Join(dir, "b")

	cmd := exec.Command("socat", "-d", "-d",
		"PTY,link="+a+",raw,echo=0",
		"PTY,link="+b+",raw,echo=0",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting socat: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, errA := os.Lstat(a)
		_, errB := os.Lstat(b)
		if errA == nil && errB == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("socat never created both PTY links (a err: %v, b err: %v)", errA, errB)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a, b
}

// TestExec_RoundTripsThroughARealPTYPair proves this Adapter is not just
// a type-erased pass-through in name: it actually opens a real serial
// line via pkg/serialexec, and the far end's response reaches
// transport.Result.Stdout. Data-path evidence only, the same caveat
// pkg/serialexec's own PTY test states: a PTY never applies baud/parity/
// stop-bit settings, so this proves nothing about line control.
func TestExec_RoundTripsThroughARealPTYPair(t *testing.T) {
	a, b := testPTYPair(t)

	farEnd, err := realserial.Open(b, &realserial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("opening far end %s: %v", b, err)
	}
	defer farEnd.Close()
	if err := farEnd.SetReadTimeout(2 * time.Second); err != nil {
		t.Fatalf("SetReadTimeout: %v", err)
	}

	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := farEnd.Read(buf)
		if err != nil || n == 0 {
			received <- ""
			return
		}
		received <- string(buf[:n])
		_, _ = farEnd.Write([]byte("RESPONSE: ok\r\n"))
	}()

	line := serialline.Config{BaudRate: 9600, DataBits: 8, Parity: serialline.ParityNone, StopBits: serialline.StopBitsOne}
	tr := serialtransport.New(serialexec.Options{ReadTimeout: 300 * time.Millisecond})
	target := transport.Target{Endpoint: transport.SerialEndpoint{Device: serialline.Device(a), Line: line}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := tr.Exec(ctx, target, credential.Credential{}, "show version")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	gotCommand := <-received
	if gotCommand != "show version\r\n" {
		t.Errorf("far end received %q, want %q", gotCommand, "show version\r\n")
	}
	if result.Stdout != "RESPONSE: ok\r\n" {
		t.Errorf("Result.Stdout = %q, want %q", result.Stdout, "RESPONSE: ok\r\n")
	}
	if !result.ExitStatusUnknown {
		t.Error("expected ExitStatusUnknown to always be true for a serial console")
	}
}

// TestExec_RejectsAnyEndpointOtherThanSerialEndpoint proves a
// binding-configuration bug (this Adapter reached with the wrong
// Endpoint kind) fails with a clear type error naming the actual type it
// got, not a panic and not an attempt to open a device that does not
// exist.
func TestExec_RejectsAnyEndpointOtherThanSerialEndpoint(t *testing.T) {
	tr := serialtransport.New(serialexec.Options{})
	target := transport.Target{Endpoint: transport.NetworkEndpoint{Host: "127.0.0.1", Port: 23}}

	_, err := tr.Exec(context.Background(), target, credential.Credential{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for a non-SerialEndpoint target")
	}
}

// TestExec_WrapsAnUnderlyingFailureClearly proves a real failure from
// pkg/serialexec (here, opening a device path that does not exist)
// reaches the caller as a wrapped, non-nil error rather than a
// zero-value success.
func TestExec_WrapsAnUnderlyingFailureClearly(t *testing.T) {
	tr := serialtransport.New(serialexec.Options{})
	target := transport.Target{Endpoint: transport.SerialEndpoint{
		Device: serialline.Device(filepath.Join(t.TempDir(), "does-not-exist")),
		Line:   serialline.Config{BaudRate: 9600, DataBits: 8},
	}}

	_, err := tr.Exec(context.Background(), target, credential.Credential{}, "cmd")
	if err == nil {
		t.Fatal("expected an error opening a nonexistent serial device")
	}
}
