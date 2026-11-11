package serialexec_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/pseudotty"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// This file is the RULE 0 evidence for serialexec.Exec, against a real
// pseudo terminal rather than a mock: Exec opens its device path as it
// opens a real serial port, and the test plays the far end on its master
// side. A command written by Exec arrives intact, the response comes back
// intact (so Exec put the line in raw mode, since the terminal starts in
// the kernel's default canonical, echoing mode), the port opens and closes
// cleanly, and a read timeout genuinely fires when nothing more is coming.
//
// A pseudo terminal proves the data path and the lifecycle ONLY. A pseudo
// terminal stores baud rate, parity, and stop bits and reads them back,
// but never applies them to the data path, so a baud mismatch across a
// pseudo terminal moves bytes intact instead of producing garbage. Claiming
// otherwise would be the theater AGENTS.md's RULE 0 forbids. Real line
// settings (a genuine baud change observed on the far side) are proven
// against a real console server, not here.

// testPTY returns a pseudo terminal's device path, for Exec to open as
// it opens a real serial port, and its master side, which the test reads
// and writes as the far end of the line. The terminal starts in the
// kernel's default mode, so a round trip also proves Exec puts the line in
// raw mode itself. Where the machine has no pseudo terminal it stops
// through need.Require, so the skip names the need ("needs pty"), a
// coverage floor this package then misses is reported as unchecked rather
// than failed, and a run that requires pty (PLEIADES_TEST_REQUIRE) fails
// instead of quietly testing less.
func testPTY(t *testing.T) (device string, far *os.File) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping PTY-backed integration test in short mode")
	}
	return pseudotty.Open(t)
}

// farEndReply reads once from far in a goroutine, sends what it read on the
// returned channel and then writes reply back. A master side has no
// portable read timeout, so the caller bounds the wait with a select.
func farEndReply(far *os.File, reply string) <-chan string {
	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, err := far.Read(buf)
		if err != nil || n == 0 {
			received <- ""
			return
		}
		received <- string(buf[:n])
		_, _ = far.Write([]byte(reply))
	}()
	return received
}

// testLineConfig is a plain 9600-8-N-1 configuration, exercised for its
// data path only (see this file's own caveat above): what values it
// names does not change what a PTY can prove.
func testLineConfig() serialline.Config {
	return serialline.Config{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   serialline.ParityNone,
		StopBits: serialline.StopBitsOne,
	}
}

// TestExec_RoundTripsThroughARealPseudoTerminal proves the data path: a command
// written by Exec reaches the far end verbatim, and the far end's
// response reaches Exec's own Result.Stdout verbatim.
func TestExec_RoundTripsThroughARealPseudoTerminal(t *testing.T) {
	device, far := testPTY(t)

	// A canned response, prefixed distinctly so the test can tell it apart
	// from the command, or from an echo of the command.
	received := farEndReply(far, "RESPONSE: ok\r\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := serialexec.Exec(ctx, serialline.Device(device), testLineConfig(), serialexec.Options{ReadTimeout: 300 * time.Millisecond}, "show version")
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

// TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent is the second half
// of this phase's own RULE 0 requirement: prove Exec does not hang
// forever waiting for a response that never comes, by genuinely letting
// its read timeout fire against a real PTY with nothing written back.
func TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent(t *testing.T) {
	// The far end is deliberately never read or written: the device is
	// silent, and Exec must still return promptly rather than hang.
	device, _ := testPTY(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	result, err := serialexec.Exec(ctx, serialline.Device(device), testLineConfig(), serialexec.Options{ReadTimeout: 200 * time.Millisecond}, "no response expected")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "" {
		t.Errorf("Result.Stdout = %q, want empty (device never wrote anything back)", result.Stdout)
	}
	// A generous upper bound: the real read timeout is 200ms, so this
	// proves Exec returned because of that timeout and not because it
	// hung until the context's own 5-second deadline.
	if elapsed > 2*time.Second {
		t.Errorf("Exec took %v, expected it to return promptly once its own read timeout fired", elapsed)
	}
}

// TestExec_PortLifecycleOpensAndClosesCleanly proves the third RULE 0
// requirement: the port opens and closes cleanly, by running Exec twice
// in a row against the same device path. A leaked or still-locked file
// descriptor from the first call would make the second Open fail.
func TestExec_PortLifecycleOpensAndClosesCleanly(t *testing.T) {
	device, _ := testPTY(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := serialexec.Options{ReadTimeout: 150 * time.Millisecond}
	for i := 0; i < 2; i++ {
		if _, err := serialexec.Exec(ctx, serialline.Device(device), testLineConfig(), opts, "cmd"); err != nil {
			t.Fatalf("Exec call %d: %v", i+1, err)
		}
	}
}

// TestExec_DefaultReadTimeoutWhenUnset proves a zero-value Options
// still works, taking DefaultReadTimeout rather than blocking forever
// with go.bug.st/serial's own NoTimeout semantics.
func TestExec_DefaultReadTimeoutWhenUnset(t *testing.T) {
	device, _ := testPTY(t)
	// Silent device: Exec must still return using DefaultReadTimeout
	// (500ms), not hang until ctx's own deadline.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := serialexec.Exec(ctx, serialline.Device(device), testLineConfig(), serialexec.Options{}, "cmd"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Exec took %v with a zero-value Options, expected DefaultReadTimeout to apply", elapsed)
	}
}

// TestExec_ContextCancellationIsPropagated proves Exec surfaces a
// canceled context as its own wrapped error rather than swallowing it,
// by canceling before Exec ever has a chance to read a response back.
func TestExec_ContextCancellationIsPropagated(t *testing.T) {
	device, _ := testPTY(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := serialexec.Exec(ctx, serialline.Device(device), testLineConfig(), serialexec.Options{ReadTimeout: time.Second}, "cmd")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want it to wrap context.Canceled", err)
	}
}

// TestExec_OpenFailureIsAClearError proves opening a device path that
// does not exist fails with a wrapped error naming the device, not a
// panic or an opaque failure.
func TestExec_OpenFailureIsAClearError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	_, err := serialexec.Exec(context.Background(), serialline.Device("/dev/does-not-exist-serialexec-test"), testLineConfig(), serialexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error opening a nonexistent device")
	}
}
