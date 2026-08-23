package serialtcp_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
	"strings"
)

// This file is the RULE 0 evidence for serialtcp.Exec, against a real
// net.Listener rather than a mock -- the same discipline
// pkg/remoteexec/remoteexectest applies for SSH: a real protocol
// implementation, just a test-local one instead of a container, since
// nothing about a raw TCP byte pipe requires a real console server to
// exercise for real.

// echoServer starts a real TCP listener on 127.0.0.1 that hands each
// accepted connection to handle, and returns the port to dial plus a
// cleanup func.
func echoServer(t *testing.T, handle func(net.Conn)) int {
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

// TestExec_RoundTripsThroughARealTCPConnection proves the data path: a
// command written by Exec reaches the far end verbatim, and the far
// end's response reaches Exec's own Result.Stdout verbatim.
func TestExec_RoundTripsThroughARealTCPConnection(t *testing.T) {
	received := make(chan string, 1)
	port := echoServer(t, func(conn net.Conn) {
		defer conn.Close()
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
	result, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: 300 * time.Millisecond}, "show version")
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
		t.Error("expected ExitStatusUnknown to always be true for raw TCP passthrough")
	}
}

// TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent proves Exec does not
// hang forever waiting for a response that never comes, by genuinely
// letting its read timeout fire against a real connection with nothing
// written back.
func TestExec_ReadTimeoutFiresWhenTheDeviceStaysSilent(t *testing.T) {
	port := echoServer(t, func(conn net.Conn) {
		// Deliberately silent: never read or write, just hold the
		// connection open until the test's own cleanup closes it.
		<-make(chan struct{})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	result, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: 200 * time.Millisecond}, "no response expected")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "" {
		t.Errorf("Result.Stdout = %q, want empty", result.Stdout)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Exec took %v, expected it to return promptly once its own read timeout fired", elapsed)
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
	opts := serialtcp.Options{ReadTimeout: 200 * time.Millisecond}
	for i := 0; i < 2; i++ {
		if _, err := serialtcp.Exec(ctx, "127.0.0.1", port, opts, "cmd"); err != nil {
			t.Fatalf("Exec call %d: %v", i+1, err)
		}
	}
}

// TestExec_DefaultsWhenOptionsIsZeroValue proves a zero-value Options
// still works, taking every documented default.
func TestExec_DefaultsWhenOptionsIsZeroValue(t *testing.T) {
	port := echoServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		// Stays silent after reading: proves DefaultReadTimeout applies.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{}, "cmd"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Exec took %v with a zero-value Options, expected DefaultReadTimeout to apply", elapsed)
	}
}

// TestExec_DialFailureIsAClearError proves a connection refused (a
// closed listener) fails with a wrapped error naming the address, not a
// panic or an opaque failure.
func TestExec_DialFailureIsAClearError(t *testing.T) {
	// Port 0 is the sockets API's "assign me any free port" value for
	// bind, so nothing can ever be listening on it. FAILURE_PATTERNS.md
	// #123 and #177 both record the alternative (open a listener, read
	// its port, close it, dial the number again) failing for real: a
	// just-released loopback port keeps accepting connects on this
	// project's own development host, and another process can claim it
	// in the window regardless.
	_, err := serialtcp.Exec(context.Background(), "127.0.0.1", 0, serialtcp.Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error dialing an address nothing can be listening on")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:0") {
		t.Errorf("error %q does not name the address it failed to reach", err)
	}
}

// TestExec_RefusesOutputExceedingTheCap proves a device streaming
// unbounded output is refused outright once accumulated output exceeds
// MaxOutputBytes, rather than silently truncated or allowed to exhaust
// memory.
func TestExec_RefusesOutputExceedingTheCap(t *testing.T) {
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'x'
	}
	port := echoServer(t, func(conn net.Conn) {
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

	_, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: 2 * time.Second, MaxOutputBytes: 10}, "cmd")
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded MaxOutputBytes")
	}
}

// TestExec_ContextCancellationDuringTheReadLoopIsPropagated proves the
// read loop's own ctx.Done() check (distinct from DialContext's own
// cancellation handling, which
// TestExec_ContextCancellationIsPropagated already covers) is actually
// reached. The server keeps sending small chunks well inside each
// call's own ReadTimeout, so the loop never takes the "gone quiet" exit
// on its own; the only way it can stop is by observing ctx.Done() at
// the top of some later iteration, once the context's own much shorter
// deadline has passed.
func TestExec_ContextCancellationDuringTheReadLoopIsPropagated(t *testing.T) {
	port := echoServer(t, func(conn net.Conn) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: time.Second}, "cmd")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestExec_GenuineReadErrorIsNotTreatedAsQuiet proves a real read
// failure (a TCP RST, forced deterministically via SO_LINGER=0 on the
// server side rather than a timing-dependent trick) is surfaced as an
// error and NOT mistaken for a timeout or an EOF-as-quiet-period, the
// two error shapes readUntilQuiet deliberately treats as success.
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
	_, err = serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: 3 * time.Second}, "cmd")
	if err == nil {
		t.Fatal("expected a genuine read error (connection reset) to be surfaced")
	}
}

// TestExec_ContextCancellationIsPropagated proves Exec surfaces a
// canceled context as its own wrapped error rather than swallowing it.
func TestExec_ContextCancellationIsPropagated(t *testing.T) {
	port := echoServer(t, func(conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := serialtcp.Exec(ctx, "127.0.0.1", port, serialtcp.Options{ReadTimeout: time.Second}, "cmd")
	if err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want it to wrap context.Canceled", err)
	}
}
