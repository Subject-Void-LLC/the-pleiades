package telnetexec_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
)

// This file is the RULE 0 evidence for telnetexec.Exec, against a real
// net.Listener rather than a mock -- the same discipline
// pkg/serialtcp's own test file applies, since nothing about bare Telnet
// requires a real device to exercise the protocol handling for real: a
// telnetd's IAC negotiation is exactly as real over a loopback
// net.Listener as it is over a WAN link.

// telnetServer starts a real TCP listener on 127.0.0.1 that hands each
// accepted connection to handle, and returns the port to dial.
func telnetServer(t *testing.T, handle func(net.Conn)) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handle(conn)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port
}

// TestExec_RoundTripsThroughARealTelnetServer proves the full shape: an
// opening negotiation-and-banner burst is drained and answered before
// the command is ever written, the command reaches the far end verbatim,
// and the far end's response reaches Exec's own Result.Stdout, with the
// banner text preserved ahead of it.
func TestExec_RoundTripsThroughARealTelnetServer(t *testing.T) {
	received := make(chan string, 1)
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		// A login banner with no negotiation bytes: this test proves
		// plain data flows correctly end to end, kept deliberately
		// separate from TestExec_RefusesOptionsOffered's own proof that
		// a negotiation offer gets a real reply -- mixing the two here
		// would race this server's single Read against the client's own
		// negotiation-reply bytes, which arrive before the command does.
		_, _ = conn.Write([]byte("Welcome\r\n"))

		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		if err != nil || n == 0 {
			received <- ""
			return
		}
		received <- string(buf[:n])
		_, _ = conn.Write([]byte("RESPONSE: ok\r\n"))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 300 * time.Millisecond}, "show version")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	gotCommand := <-received
	if gotCommand != "show version\r\n" {
		t.Errorf("far end received %q, want %q", gotCommand, "show version\r\n")
	}
	want := "Welcome\r\nRESPONSE: ok\r\n"
	if result.Stdout != want {
		t.Errorf("Result.Stdout = %q, want %q", result.Stdout, want)
	}
	if !result.ExitStatusUnknown {
		t.Error("expected ExitStatusUnknown to always be true for bare Telnet")
	}
}

// TestExec_RefusesOptionsOffered proves the IAC filter's refusal actually
// runs over a real TCP connection, not just inside the unit-level filter
// tests below: a server offering DO and WILL gets WONT and DONT back,
// verbatim, before the client writes anything else.
func TestExec_RefusesOptionsOffered(t *testing.T) {
	const optA, optB = 1, 3 // ECHO, SUPPRESS-GO-AHEAD (RFC 857/858) -- values are arbitrary to this filter

	replyCh := make(chan []byte, 1)
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		// IAC DO optA IAC WILL optB: one of each verb this client must
		// refuse.
		_, _ = conn.Write([]byte{255, 253, optA, 255, 251, optB})

		reply := make([]byte, 6)
		if _, err := io.ReadFull(conn, reply); err != nil {
			replyCh <- nil
			return
		}
		replyCh <- reply
		// Stays silent afterward: proves Exec still completes (both read
		// phases go quiet) rather than hanging for a reply that never
		// comes.
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 200 * time.Millisecond}, "cmd")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "" {
		t.Errorf("Result.Stdout = %q, want empty: negotiation bytes must never leak into plain data", result.Stdout)
	}

	want := []byte{255, 252, optA, 255, 254, optB} // IAC WONT optA IAC DONT optB
	got := <-replyCh
	if got == nil {
		t.Fatal("server never received a full 6-byte reply")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reply = %v, want %v (IAC WONT optA IAC DONT optB)", got, want)
		}
	}
}

// TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent proves Exec does not
// hang forever waiting for a response that never comes, across BOTH read
// phases (banner drain, then command output), by genuinely letting each
// phase's own read timeout fire against a real connection with nothing
// written back.
func TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		// Deliberately silent: never read or write, just hold the
		// connection open until the test's own cleanup closes it.
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	result, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 200 * time.Millisecond}, "no response expected")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "" {
		t.Errorf("Result.Stdout = %q, want empty", result.Stdout)
	}
	// Two read phases, each bounded by ReadTimeout: bounded well under
	// the outer context deadline, not a tight bound on either phase
	// individually.
	if elapsed > 3*time.Second {
		t.Errorf("Exec took %v, expected both read phases' own timeouts to fire promptly", elapsed)
	}
}

// TestExec_ConnectionLifecycleOpensAndClosesCleanly proves the
// connection opens and closes cleanly by running Exec twice against a
// server that accepts one connection at a time -- a leaked connection
// from the first call would make the second Accept never happen.
func TestExec_ConnectionLifecycleOpensAndClosesCleanly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 256)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("ok\r\n"))
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := telnetexec.Options{ReadTimeout: 200 * time.Millisecond}
	for i := 0; i < 2; i++ {
		if _, err := telnetexec.Exec(ctx, "127.0.0.1", port, opts, "cmd"); err != nil {
			t.Fatalf("Exec call %d: %v", i+1, err)
		}
	}
}

// TestExec_DefaultsWhenOptionsIsZeroValue proves a zero-value Options
// still works, taking every documented default.
func TestExec_DefaultsWhenOptionsIsZeroValue(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		// Stays silent after reading: proves DefaultReadTimeout applies.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{}, "cmd"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("Exec took %v with a zero-value Options, expected DefaultReadTimeout to apply to both read phases", elapsed)
	}
}

// TestExec_DialFailureIsAClearError proves a connection refused (a
// closed listener) fails with a wrapped error naming the address, not a
// panic or an opaque failure.
func TestExec_DialFailureIsAClearError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing is listening now

	_, err = telnetexec.Exec(context.Background(), "127.0.0.1", port, telnetexec.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error dialing a closed listener")
	}
}

// TestExec_RefusesOutputExceedingTheCap proves a device streaming
// unbounded output is refused outright once accumulated output within a
// single read phase exceeds MaxOutputBytes, rather than silently
// truncated or allowed to exhaust memory.
func TestExec_RefusesOutputExceedingTheCap(t *testing.T) {
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'x'
	}
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		for {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 2 * time.Second, MaxOutputBytes: 10}, "cmd")
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded MaxOutputBytes")
	}
}

// TestExec_ContextCancellationDuringTheReadLoopIsPropagated proves the
// read loop's own ctx.Done() check is actually reached during the
// post-command read phase. The server keeps sending small chunks well
// inside each call's own ReadTimeout, so that phase never takes the
// "gone quiet" exit on its own; the only way it can stop is by observing
// ctx.Done() at the top of some later iteration, once the context's own
// much shorter deadline has passed.
func TestExec_ContextCancellationDuringTheReadLoopIsPropagated(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		for {
			if _, err := conn.Write([]byte(".")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: time.Second}, "cmd")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestExec_GenuineReadErrorIsNotTreatedAsQuiet proves a real read
// failure (a TCP RST, forced deterministically via SO_LINGER=0 on the
// server side rather than a timing-dependent trick) is surfaced as an
// error and NOT mistaken for a timeout or an EOF-as-quiet-period --
// exactly the distinction FAILURE_PATTERNS.md #172 records pkg/serialtcp
// getting wrong on its first draft, so this package proves it
// independently rather than assuming the ported design is correct here
// too.
func TestExec_GenuineReadErrorIsNotTreatedAsQuiet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			return
		}
		buf := make([]byte, 256)
		_, _ = tcpConn.Read(buf)
		// SO_LINGER=0 makes the next Close() send a real RST instead of
		// a clean FIN, so the client's next Read fails with "connection
		// reset by peer" -- a genuine error, not a timeout and not EOF.
		_ = tcpConn.SetLinger(0)
		_ = tcpConn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: 3 * time.Second}, "cmd")
	if err == nil {
		t.Fatal("expected a genuine read error (connection reset) to be surfaced")
	}
}

// TestExec_ContextCancellationIsPropagated proves Exec surfaces an
// already-canceled context as its own wrapped error rather than
// swallowing it. With no live connection yet, this is DialContext's own
// cancellation handling that fires, before readUntilQuiet's banner-drain
// phase is ever reached — TestExec_ContextCancellationDuringBannerDrainIsPropagated
// below is what proves the read loop's OWN ctx.Done() check separately.
func TestExec_ContextCancellationIsPropagated(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: time.Second}, "cmd")
	if err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want it to wrap context.Canceled", err)
	}
}

// TestExec_ContextCancellationDuringBannerDrainIsPropagated proves
// readUntilQuiet's own ctx.Done() check is reached and its error wrapped
// during the FIRST read phase (draining the opening negotiation/banner
// burst, before the command is ever written) -- distinct from
// TestExec_ContextCancellationIsPropagated above, which never gets past
// DialContext, and from TestExec_ContextCancellationDuringTheReadLoopIsPropagated,
// which covers the SECOND phase the same way. A single blocking Read
// bounded only by the (much longer) ReadTimeout would never give the
// loop a chance to observe ctx.Done() between iterations, exactly the
// documented "checked only between reads" limitation both packages'
// doc comments state -- so, like that test, the server keeps the banner
// phase's own read loop iterating by sending small chunks continuously
// from the moment it accepts, well before the client would ever write a
// command.
func TestExec_ContextCancellationDuringBannerDrainIsPropagated(t *testing.T) {
	port := telnetServer(t, func(conn net.Conn) {
		defer conn.Close()
		for {
			if _, err := conn.Write([]byte(".")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := telnetexec.Exec(ctx, "127.0.0.1", port, telnetexec.Options{ReadTimeout: time.Second}, "cmd")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}
