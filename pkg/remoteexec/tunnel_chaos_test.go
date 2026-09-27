package remoteexec

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
)

// startStreamingListener starts a real TCP listener that, once it
// accepts a connection, writes a small chunk every 20ms forever (until
// the connection closes), standing in for a console server whose
// session has no natural end - exactly the shape that makes "was the
// hop severed mid-stream" a meaningful question, unlike a server that
// answers once and goes quiet on its own.
func startStreamingListener(t *testing.T) string {
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
		defer conn.Close()
		for {
			if _, err := conn.Write([]byte("console-output-chunk\n")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	return ln.Addr().String()
}

// TestDialThroughHops_SeveredBastionLegIsIndistinguishableFromAGracefulQuit
// is this phase's own Chaos Testing proof for a non-SSH final leg, and
// it proves the OPPOSITE of what this phase's own plan first assumed
// this test would show. The plan expected severing the bastion leg to
// surface as "a genuine error, never silent truncation" - a reasonable
// guess, but wrong, caught here by actually running it rather than
// trusting the plan's own prediction (this module's own RULE 0
// discipline, applied to a planning document instead of code for once).
//
// What actually happens: killing the bastion's own *ssh.Client while
// its direct-tcpip channel is mid-read does NOT surface as a distinct
// transport-level error. golang.org/x/crypto/ssh's own Channel.Read
// returns plain io.EOF both when a channel closes cleanly AND when the
// underlying multiplexed connection dies out from under it - there is
// no separate "the connection under this channel just died" error this
// package could catch even if it wanted to. Combined with
// pkg/serialtcp's own deliberate, already-tested EOF-as-quiet design
// (FAILURE_PATTERNS.md #172: a raw byte pipe has no session semantics to
// say WHY the far end went quiet), the honest, consistent behavior is
// that a severed bastion leg and a graceful target-side close are
// genuinely indistinguishable here - which is exactly what this test
// and its own paired control
// (TestDialThroughHops_GracefulTargetCloseIsStillTreatedAsQuiet, run
// against the identical topology) both prove, deliberately, rather than
// silently.
func TestDialThroughHops_SeveredBastionLegIsIndistinguishableFromAGracefulQuit(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	consoleAddr := startStreamingListener(t)

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	consoleHost, consolePort := splitHostPortT(t, consoleAddr)
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}

	ctx := context.Background()
	conn, err := r.DialThroughHops(ctx, hops, Target{Host: consoleHost, Port: consolePort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}
	tunneled, ok := conn.(*hopTunneledConn)
	if !ok {
		t.Fatalf("conn is %T, want *hopTunneledConn", conn)
	}
	if len(tunneled.chain) != 1 {
		t.Fatalf("chain has %d clients, want exactly 1 (the bastion)", len(tunneled.chain))
	}

	// Local snapshot taken after every fixture is already up
	// (FAILURE_PATTERNS.md #169's own lesson), not a shared baseline.
	// Registered before conn.Close() below, so it runs AFTER it (defers
	// are LIFO): closing chain[0] directly only ever unblocks ONE of
	// hopTunneledConn's two pump goroutines (the one reading FROM the
	// tunneled channel) - the other (reading from the caller-facing
	// pipe half, waiting for a Write that will never come once this test
	// stops driving one) only stops once conn.Close() itself runs, so
	// the leak check must wait for that too.
	leakOpts := goleak.IgnoreCurrent()
	defer func() { goleak.VerifyNone(t, leakOpts) }()
	defer conn.Close()

	// Sever ONLY the bastion leg, mid-stream, from a background goroutine
	// so it lands while ExecOverConn's own read loop is genuinely
	// in-flight rather than racing a call that already finished.
	severed := make(chan struct{})
	go func() {
		defer close(severed)
		time.Sleep(150 * time.Millisecond)
		_ = tunneled.chain[0].Close()
	}()

	start := time.Now()
	result, execErr := serialtcp.ExecOverConn(ctx, conn, serialtcp.Options{ReadTimeout: 2 * time.Second, MaxOutputBytes: 1 << 20}, "cmd")
	elapsed := time.Since(start)
	<-severed

	// The verified real behavior: no error, a clean Result carrying
	// whatever chunks arrived before the severance landed. See this
	// test's own doc comment for why, and for the paired control that
	// proves this is not this package accidentally swallowing every
	// close as quiet.
	if execErr != nil {
		t.Fatalf("expected the severed bastion leg to be treated as quiet (io.EOF), like a graceful close, got an error instead: %v", execErr)
	}
	if !strings.Contains(result.Stdout, "console-output-chunk") {
		t.Errorf("Stdout = %q, want at least one chunk that arrived before the severance", result.Stdout)
	}
	if !result.ExitStatusUnknown {
		t.Error("expected ExitStatusUnknown to always be true for raw TCP passthrough")
	}
	if elapsed > 5*time.Second {
		t.Errorf("severed read took %v to return, want it bounded well under ReadTimeout", elapsed)
	}
	t.Logf("severed bastion leg produced a clean (truncated) result after %v, exactly like a graceful close: %q", elapsed, result.Stdout)
}

// TestDialThroughHops_GracefulTargetCloseIsStillTreatedAsQuiet is the
// deliberate control this chaos test needs to be meaningful: the SAME
// topology, but the CONSOLE SERVER itself closing its own connection
// cleanly (not the bastion leg dying) must still be treated as
// pkg/serialtcp's own documented "quiet" success, exactly as it would be
// with no bastion in the path at all. Without this control, a reader
// could not tell whether the error above came from something specific
// to a severed BASTION leg or from this package accidentally treating
// every connection close as an error, which would silently break the
// EOF-as-quiet contract pkg/serialtcp's own real tests already depend
// on.
func TestDialThroughHops_GracefulTargetCloseIsStillTreatedAsQuiet(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })

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
		_, _ = conn.Write([]byte("one chunk then goodbye\n"))
		_ = conn.Close() // graceful, deliberate: the target itself ends the session
	}()

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	consoleHost, consolePort := splitHostPortT(t, ln.Addr().String())
	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}

	ctx := context.Background()
	conn, err := r.DialThroughHops(ctx, hops, Target{Host: consoleHost, Port: consolePort})
	if err != nil {
		t.Fatalf("DialThroughHops: %v", err)
	}

	// Registered before conn.Close() below, so it runs AFTER it (defers
	// are LIFO): the pump goroutines Close() stops must have actually
	// stopped before this checks for leaks, not while they are still
	// shutting down.
	leakOpts := goleak.IgnoreCurrent()
	defer func() { goleak.VerifyNone(t, leakOpts) }()
	defer conn.Close()

	result, execErr := serialtcp.ExecOverConn(ctx, conn, serialtcp.Options{ReadTimeout: 2 * time.Second}, "cmd")
	if execErr != nil {
		t.Fatalf("expected the target's own graceful close to be treated as quiet, got: %v", execErr)
	}
	if result.Stdout != "one chunk then goodbye\n" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "one chunk then goodbye\n")
	}
}
