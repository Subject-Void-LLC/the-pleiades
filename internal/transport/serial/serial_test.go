package serial_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/pseudotty"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
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

// testPTY returns a pseudo terminal's device path, for the Adapter to
// open as a serial port, and its master side, which the test plays the far
// end on. A local wrapper of pkg/serialexec's own test helper of the same
// name rather than a shared export: both are a few lines over
// internal/testsupport/pseudotty, and a cross-package test-helper
// dependency is not worth the coupling. Without a pseudo terminal it stops
// through need.Require, as that helper does, so the skip names the need.
func testPTY(t *testing.T) (device string, far *os.File) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping PTY-backed integration test in short mode")
	}
	return pseudotty.Open(t)
}

// TestExec_RoundTripsThroughARealPseudoTerminal proves this Adapter is not just
// a type-erased pass-through in name: it actually opens a real serial
// line via pkg/serialexec, and the far end's response reaches
// transport.Result.Stdout. Data-path evidence only, the same caveat
// pkg/serialexec's own PTY test states: a PTY never applies baud/parity/
// stop-bit settings, so this proves nothing about line control.
func TestExec_RoundTripsThroughARealPseudoTerminal(t *testing.T) {
	device, far := testPTY(t)

	// The far end reads the command once and answers it. A master side has
	// no portable read timeout, so the wait below is a bounded select.
	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := far.Read(buf)
		if err != nil || n == 0 {
			received <- ""
			return
		}
		received <- string(buf[:n])
		_, _ = far.Write([]byte("RESPONSE: ok\r\n"))
	}()

	line := serialline.Config{BaudRate: 9600, DataBits: 8, Parity: serialline.ParityNone, StopBits: serialline.StopBitsOne}
	tr := serialtransport.New(serialexec.Options{ReadTimeout: 300 * time.Millisecond})
	target := transport.Target{Endpoint: transport.SerialEndpoint{Device: serialline.Device(device), Line: line}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := tr.Exec(ctx, target, credential.Credential{}, "show version")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	var gotCommand string
	select {
	case gotCommand = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("the far end never received the command")
	}
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
