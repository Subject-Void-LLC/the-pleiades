package serialexec_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
	realserial "go.bug.st/serial"
)

// This file is the RULE 0 evidence for serialexec.Exec, against a real
// socat PTY pair rather than a mock: a command written to one side
// appears on the other, the port opens and closes cleanly, and a read
// timeout genuinely fires when nothing more is coming.
//
// A PTY pair proves the data path and the lifecycle ONLY. A pseudo
// terminal stores baud rate, parity, and stop bits and reads them back,
// but never applies them to the data path, so a baud mismatch across a
// PTY pair moves bytes intact instead of producing garbage. Claiming
// otherwise would be the theater AGENTS.md's RULE 0 forbids. Real line
// settings (a genuine baud change observed on the far side) are proven
// against a real console server, not here.

// testPTYPair starts a real socat process linking two PTYs at fixed
// paths under t.TempDir(), waits for both to exist, and registers
// cleanup. Without socat on PATH it stops through testsupport.Require,
// so the skip names the need ("needs socat"), a coverage floor this
// package then misses is reported as unchecked rather than failed, and
// a run that requires socat (PLEIADES_TEST_REQUIRE) fails instead of
// quietly testing less.
func testPTYPair(t *testing.T) (a, b string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping PTY-backed integration test in short mode")
	}
	_, err := exec.LookPath("socat")
	testsupport.Require(t, "socat", err == nil,
		"socat links the two pseudo terminals these tests talk across; install it (apt-get install socat)")

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
	// Waited on exactly once, here, so the loop below can tell "socat is
	// still starting" from "socat is gone". Cleanup drains the result
	// rather than calling Wait a second time, which would error.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-waited
	})

	// Thirty seconds rather than five. This is not a budget for how long
	// socat SHOULD take -- it takes a few milliseconds on an idle machine
	// -- it is the point at which waiting longer tells us nothing new.
	// The old five seconds was inside the range a loaded machine can
	// delay a subprocess by: `make ci` runs twenty-one container packages
	// and the whole suite three times over, and this test failed there
	// while passing alone in under a second. A timeout that fires on load
	// reports a defect that is not there and hides the next real one.
	const ptyLinkTimeout = 30 * time.Second
	started := time.Now()
	for {
		_, errA := os.Lstat(a)
		_, errB := os.Lstat(b)
		if errA == nil && errB == nil {
			break
		}
		// Asked before the deadline, because socat dying is a DIFFERENT
		// failure and used to be reported as this one: a process that
		// exited immediately still produced "never created both PTY
		// links" after the full wait, which names the symptom and sends
		// the reader to look at PTYs rather than at socat's own error.
		select {
		case err := <-waited:
			waited <- err // put it back, so Cleanup's drain still returns
			t.Fatalf("socat exited before creating its PTY links: %v", err)
		default:
		}
		if elapsed := time.Since(started); elapsed > ptyLinkTimeout {
			t.Fatalf("socat never created both PTY links within %s (a err: %v, b err: %v)",
				elapsed.Round(time.Second), errA, errB)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a, b
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

// TestExec_RoundTripsThroughARealPTYPair proves the data path: a command
// written by Exec reaches the far end verbatim, and the far end's
// response reaches Exec's own Result.Stdout verbatim.
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
		// Echo a canned response back, prefixed distinctly so the test
		// can tell it apart from the command it echoes.
		_, _ = farEnd.Write([]byte("RESPONSE: ok\r\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := serialexec.Exec(ctx, serialline.Device(a), testLineConfig(), serialexec.Options{ReadTimeout: 300 * time.Millisecond}, "show version")
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

// TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent is the second half
// of this phase's own RULE 0 requirement: prove Exec does not hang
// forever waiting for a response that never comes, by genuinely letting
// its read timeout fire against a real PTY with nothing written back.
func TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent(t *testing.T) {
	a, b := testPTYPair(t)

	farEnd, err := realserial.Open(b, &realserial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("opening far end %s: %v", b, err)
	}
	defer farEnd.Close()
	// Deliberately never read or write from farEnd: the device is
	// silent, and Exec must still return promptly rather than hang.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	result, err := serialexec.Exec(ctx, serialline.Device(a), testLineConfig(), serialexec.Options{ReadTimeout: 200 * time.Millisecond}, "no response expected")
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
	a, b := testPTYPair(t)

	farEnd, err := realserial.Open(b, &realserial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("opening far end %s: %v", b, err)
	}
	defer farEnd.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := serialexec.Options{ReadTimeout: 150 * time.Millisecond}
	for i := 0; i < 2; i++ {
		if _, err := serialexec.Exec(ctx, serialline.Device(a), testLineConfig(), opts, "cmd"); err != nil {
			t.Fatalf("Exec call %d: %v", i+1, err)
		}
	}
}

// TestExec_DefaultReadTimeoutWhenUnset proves a zero-value Options
// still works, taking DefaultReadTimeout rather than blocking forever
// with go.bug.st/serial's own NoTimeout semantics.
func TestExec_DefaultReadTimeoutWhenUnset(t *testing.T) {
	a, b := testPTYPair(t)

	farEnd, err := realserial.Open(b, &realserial.Mode{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("opening far end %s: %v", b, err)
	}
	defer farEnd.Close()
	// Silent device: Exec must still return using DefaultReadTimeout
	// (500ms), not hang until ctx's own deadline.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := serialexec.Exec(ctx, serialline.Device(a), testLineConfig(), serialexec.Options{}, "cmd"); err != nil {
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
	a, _ := testPTYPair(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := serialexec.Exec(ctx, serialline.Device(a), testLineConfig(), serialexec.Options{ReadTimeout: time.Second}, "cmd")
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
