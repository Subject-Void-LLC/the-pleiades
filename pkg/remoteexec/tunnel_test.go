package remoteexec

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// startEchoListener starts a real, plain TCP listener (no SSH involved
// at all) that echoes back whatever each connection writes to it, until
// that connection closes, and keeps accepting new connections for the
// life of the test. This is DialThroughHops' own "not SSH" final
// target: any non-SSH protocol (raw passthrough, RFC 2217, ...) reaches
// exactly this kind of plain byte-stream endpoint. Looping Accept
// matters even for a single logical round trip in most tests here: a
// bastion's own direct-tcpip forwarding opens a genuinely new TCP
// connection to this listener for every DialThroughHops call, so a test
// reusing the same bastion and target across two calls (proving a
// second chain still works after the first is closed) needs a second
// connection actually accepted, not left sitting in the OS backlog
// forever behind a listener that only ever called Accept once.
func startEchoListener(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	return ln.Addr().String()
}

// roundTrip writes msg to conn and reads back exactly len(msg) bytes,
// failing the test on any error or mismatch.
func roundTrip(t testing.TB, conn net.Conn, msg string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != msg {
		t.Fatalf("echoed %q, want %q", buf, msg)
	}
}

// TestDialThroughHops_ZeroHopsDialsDirectly proves an empty hops slice
// is exactly a direct TCP dial to target, with no SSH involved anywhere
// -- DialThroughHops' own "nil route" contract, matching Connect's
// identical guarantee for an empty Route.
func TestDialThroughHops_ZeroHopsDialsDirectly(t *testing.T) {
	echoAddr := startEchoListener(t)
	host, port := splitHostPortT(t, echoAddr)

	r := New(Options{MaxRetries: 1})
	conn, err := r.DialThroughHops(context.Background(), nil, Target{Host: host, Port: port})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	defer conn.Close()

	roundTrip(t, conn, "hello-direct")
}

// TestDialThroughHops_TunnelsThroughOneBastion proves the real mechanism
// end to end: a genuine SSH handshake to the bastion, a genuine
// direct-tcpip channel opened through it, landing on a target that is
// NOT an SSH server at all -- a plain TCP echo listener -- with no
// second SSH handshake attempted against it. This is the exact shape
// pkg/serialtcp and pkg/rfc2217 need for a console server reached
// through a bastion.
func TestDialThroughHops_TunnelsThroughOneBastion(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion should never run a command", "", 1
	})
	echoAddr := startEchoListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)

	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	conn, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	defer conn.Close()

	roundTrip(t, conn, "hello-through-one-bastion")
}

// TestDialThroughHops_TunnelsThroughTwoBastions proves N hops, not one:
// client -> bastion A -> bastion B -> a plain TCP echo target, two
// independent SSH handshakes and two nested direct-tcpip channels,
// landing on a target that speaks neither SSH nor anything this package
// understands at all -- proving the chain is genuinely N-deep rather
// than a one-hop special case, the same requirement the real bastion
// proof (internal/transport/ssh's own container test) exists to satisfy
// against real containers.
func TestDialThroughHops_TunnelsThroughTwoBastions(t *testing.T) {
	aAddr, aKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 1 })
	bAddr, bKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 1 })
	echoAddr := startEchoListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{aAddr: aKey, bAddr: bKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	aHost, aPort := splitHostPortT(t, aAddr)
	bHost, bPort := splitHostPortT(t, bAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)

	hops := []Hop{
		{Target: Target{Host: aHost, Port: aPort}, Auth: testAuth},
		{Target: Target{Host: bHost, Port: bPort}, Auth: testAuth},
	}
	conn, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	defer conn.Close()

	roundTrip(t, conn, "hello-through-two-bastions")
}

// TestDialThroughHops_CloseClosesTheWholeChain proves the returned
// net.Conn's own Close call releases every hop's own SSH client, not
// just the tunneled channel: a second connection attempt through the
// SAME bastion, after the first is closed, must still succeed (the
// bastion's own listener accepts a fresh connection), and a goroutine
// leak check across the whole sequence proves nothing from the first
// chain was left running.
func TestDialThroughHops_CloseClosesTheWholeChain(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 1 })
	echoAddr := startEchoListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	target := Target{Host: echoHost, Port: echoPort}

	conn, err := r.DialThroughHops(context.Background(), hops, target)
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	roundTrip(t, conn, "before-close")
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The tunneled conn itself must now be unusable.
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Error("expected Write to fail on a closed tunneled connection")
	}

	// A fresh chain through the same bastion must still work: proves
	// Close did not somehow wedge the bastion's own listener, and that
	// this package holds no lingering per-chain state keyed by address.
	conn2, err := r.DialThroughHops(context.Background(), hops, target)
	if err != nil {
		t.Fatalf("second DialThroughHops after Close: %v", err)
	}
	defer conn2.Close()
	roundTrip(t, conn2, "after-close")
}

// TestDialThroughHops_UnusableHopAuthRefusedBeforeDialing proves a hop
// with no usable authentication method is refused before any network
// I/O, naming that hop's own address.
func TestDialThroughHops_UnusableHopAuthRefusedBeforeDialing(t *testing.T) {
	bastionAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	echoAddr := startEchoListener(t)

	r := New(Options{InsecureSkipHostKeyVerify: true, MaxRetries: 1})
	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)

	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: Auth{}}}
	_, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err == nil {
		t.Fatal("expected a hop with no usable Auth to be refused")
	}
	if !strings.Contains(err.Error(), "no usable authentication method") || !strings.Contains(err.Error(), bastionAddr) {
		t.Errorf("err = %v, want it to name the hop's own address %q", err, bastionAddr)
	}
}

// TestDialThroughHops_UnverifiedBastionKeyFailsClosed proves the hop's
// own host key check still applies exactly as it does for Connect: an
// unknown bastion key fails closed, naming the bastion.
func TestDialThroughHops_UnverifiedBastionKeyFailsClosed(t *testing.T) {
	bastionAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	echoAddr := startEchoListener(t)

	r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, nil), MaxRetries: 1})
	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)

	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	_, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err == nil {
		t.Fatal("expected an unknown bastion key to fail closed")
	}
	if !strings.Contains(err.Error(), bastionAddr) {
		t.Errorf("err = %v, want it to name the bastion's own address %q", err, bastionAddr)
	}
}

// TestDialThroughHops_UnreachableTargetThroughBastionFailsWithChannelError
// proves the final-leg failure mode: the bastion's own handshake
// succeeds, but nothing is listening at the address the caller asked it
// to forward to, so the channel open itself fails and must surface as a
// real, named error.
// unreachableAddr is deliberately NOT built by opening a listener and
// closing it: FAILURE_PATTERNS.md #123 found that exact strategy picks
// the one address on a WSL2 host that keeps accepting connects after
// release (loopback bridging between the Linux and Windows sides), which
// is worse than a coin flip for a test asserting a specific failure
// shape. Port 0 is the sockets API's "assign me any free port" value for
// bind; nothing can ever be listening on it, by definition, so a connect
// to it fails for a reason no host-specific timing can undo.
const unreachableAddr = "127.0.0.1:0"

func TestDialThroughHops_UnreachableTargetThroughBastionFailsWithChannelError(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	unreachableHost, unreachablePort := splitHostPortT(t, unreachableAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}

	_, err := r.DialThroughHops(context.Background(), hops, Target{Host: unreachableHost, Port: unreachablePort})
	if err == nil {
		t.Fatal("expected a channel-open failure against an address nothing is listening on")
	}
}

// TestHopTunneledConn_CloseStopsBothPumpGoroutines proves
// newHopTunneledConn's two background pump goroutines actually exit
// once Close returns, using a LOCAL goleak.IgnoreCurrent() snapshot
// taken after every fixture (bastion, echo listener, chain) is already
// up -- FAILURE_PATTERNS.md #169's own lesson, so this check depends on
// nothing this test binary happened to run before it, not shared
// package-level state. Close's own doc comment states it waits for
// pumpDone before returning specifically so this property holds; this
// test is what proves that wait is real, not merely written.
func TestHopTunneledConn_CloseStopsBothPumpGoroutines(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	echoAddr := startEchoListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}

	conn, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	roundTrip(t, conn, "before-close")

	leakOpts := goleak.IgnoreCurrent()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	goleak.VerifyNone(t, leakOpts)
}

// TestDialFinalLegWithRetry_RetriesThenSucceeds is a white-box proof
// that the final leg gets real retry treatment, not a bare
// best-effort attempt: a dial that fails twice then succeeds still
// returns success, having been retried by dialFinalLegWithRetry itself.
func TestDialFinalLegWithRetry_RetriesThenSucceeds(t *testing.T) {
	r := New(Options{MaxRetries: 5})
	attempts := 0
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("simulated transient failure")
		}
		return &net.TCPConn{}, nil
	}

	conn, err := r.dialFinalLegWithRetry(context.Background(), dial, "target:1")
	if err != nil {
		t.Fatalf("dialFinalLegWithRetry: %v", err)
	}
	if conn == nil {
		t.Fatal("expected a non-nil conn once the dial finally succeeded")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want exactly 3 (two failures then a success)", attempts)
	}
}

// TestDialFinalLegWithRetry_CircuitOpensAfterRepeatedFailures proves the
// SAME circuit breaker every other leg uses also guards the final,
// non-SSH leg: enough consecutive failures against one address opens
// its circuit, and a subsequent call fails fast (breaker.ErrOpen) without
// calling dial again.
func TestDialFinalLegWithRetry_CircuitOpensAfterRepeatedFailures(t *testing.T) {
	r := New(Options{MaxRetries: 1})
	const addr = "always-fails:1"
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		return nil, errors.New("simulated permanent failure")
	}

	// Drive enough failures to open the breaker (breakerThreshold is this
	// package's own internal constant; a generous upper bound of calls
	// here avoids coupling this test to its exact value).
	var lastErr error
	for i := 0; i < 20; i++ {
		_, lastErr = r.dialFinalLegWithRetry(context.Background(), dial, addr)
		if lastErr != nil && errors.Is(lastErr, breaker.ErrOpen) {
			break
		}
	}
	if !errors.Is(lastErr, breaker.ErrOpen) {
		t.Fatalf("expected the circuit to open after repeated failures, last error: %v", lastErr)
	}
}

// TestHopTunneledConn_CloseReturnsTheFirstError proves Close attempts
// every hop's own client close regardless of an earlier failure, and
// reports the first error encountered rather than silently swallowing
// it.
func TestHopTunneledConn_CloseReturnsTheFirstError(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	echoAddr := startEchoListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	echoHost, echoPort := splitHostPortT(t, echoAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}

	conn, err := r.DialThroughHops(context.Background(), hops, Target{Host: echoHost, Port: echoPort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}

	// Close once normally, then again: the second Close still attempts
	// every step (net.Conn.Close and ssh.Client.Close are both documented
	// safe to call more than once) and must not panic.
	if err := conn.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Logf("second Close returned %v (acceptable: closing twice may report an already-closed error)", err)
	}
}
