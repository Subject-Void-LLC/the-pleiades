package remoteexec

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// writeMultiKnownHosts writes one known_hosts line per entry into a fresh
// temp file, returning the file's path. Every server startFakeSSHListener
// starts in this file already answers both "session"
// (handleFakeSession) and "direct-tcpip" (forwardDirectTCPIP) channel
// requests, so the exact same fake server works as a hop-chain's bastion
// (forwarding) and as its final target (running a command) with no
// separate implementation of either role.
func writeMultiKnownHosts(t testing.TB, entries map[string]ssh.PublicKey) string {
	t.Helper()
	var b strings.Builder
	for hostPort, key := range entries {
		b.WriteString(knownhosts.Line([]string{hostPort}, key))
		b.WriteString("\n")
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts file: %v", err)
	}
	return path
}

// splitHostPortT splits "host:port" into its parts, failing the test on
// any error; every address under test here comes from a real
// net.Listener, so a parse failure indicates a broken test fixture.
func splitHostPortT(t testing.TB, hostPort string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", hostPort, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

// TestConnect_HopChain_TunnelsThroughOneBastion proves the real mechanism
// end to end: a genuine TCP dial and SSH handshake to the bastion, a
// genuine direct-tcpip channel opened through it, and a SECOND, genuinely
// independent SSH handshake over that channel to the target, whose own
// handler (not the bastion's) answers the command. Per-hop host key
// verification is exercised for real here too: both the bastion's and the
// target's keys are written to known_hosts under their own addresses, and
// InsecureSkipHostKeyVerify is never set.
func TestConnect_HopChain_TunnelsThroughOneBastion(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion should never run a command directly", "", 1
	})
	targetAddr, targetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "target-reached:" + cmd, "", 0
	})

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		bastionAddr: bastionKey,
		targetAddr:  targetKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)

	hops := []Hop{
		{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth},
	}
	target := Target{Host: targetHost, Port: targetPort}

	result, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err != nil {
		t.Fatalf("hop-chain Run failed: %v", err)
	}
	if !strings.Contains(result.Stdout, "target-reached:echo hi") {
		t.Fatalf("stdout = %q, want it to show the TARGET's handler ran, not the bastion's", result.Stdout)
	}
}

// TestConnect_HopChain_TunnelsThroughTwoBastions proves N hops, not one:
// client -> bastion A -> bastion B -> target, two independent direct-tcpip
// tunnels nested inside each other, two independent SSH handshakes past
// the first.
func TestConnect_HopChain_TunnelsThroughTwoBastions(t *testing.T) {
	bastionAAddr, bastionAKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion A should never run a command directly", "", 1
	})
	bastionBAddr, bastionBKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion B should never run a command directly", "", 1
	})
	targetAddr, targetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "target-reached:" + cmd, "", 0
	})

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		bastionAAddr: bastionAKey,
		bastionBAddr: bastionBKey,
		targetAddr:   targetKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	aHost, aPort := splitHostPortT(t, bastionAAddr)
	bHost, bPort := splitHostPortT(t, bastionBAddr)
	tHost, tPort := splitHostPortT(t, targetAddr)

	hops := []Hop{
		{Target: Target{Host: aHost, Port: aPort}, Auth: testAuth},
		{Target: Target{Host: bHost, Port: bPort}, Auth: testAuth},
	}
	target := Target{Host: tHost, Port: tPort}

	result, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err != nil {
		t.Fatalf("two-hop chain Run failed: %v", err)
	}
	if !strings.Contains(result.Stdout, "target-reached:echo hi") {
		t.Fatalf("stdout = %q, want the target's own handler output", result.Stdout)
	}
}

// TestConnect_HopChain_UnverifiedTargetKeyFailsClosed is the per-hop
// key-confusion adversarial case: the bastion's key is known, the
// target's is deliberately absent from known_hosts. The tunneled
// handshake must fail closed, and the failure must be attributable to the
// TARGET's own key check specifically (its address appears in the error),
// not to a generic timeout or dial failure that would pass a naive
// assertion while proving nothing.
func TestConnect_HopChain_UnverifiedTargetKeyFailsClosed(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "", "", 0
	})
	targetAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "should never be reached", "", 0
	})

	// Only the bastion's key is known; the target's is deliberately
	// omitted.
	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		bastionAddr: bastionKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)

	hops := []Hop{
		{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth},
	}
	target := Target{Host: targetHost, Port: targetPort}

	_, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err == nil {
		t.Fatal("expected the tunneled handshake to fail closed against an unknown target key")
	}
	if !strings.Contains(err.Error(), targetAddr) {
		t.Errorf("err = %v, want it to name the target's own address %q (the hop that actually failed), not a generic dial failure", err, targetAddr)
	}
}

// TestConnect_HopChain_UnverifiedTargetKeySucceedsOnceKeyIsAdded is the
// other direction of the adversarial pair above: the identical chain,
// against the identical servers, succeeds once the target's real key is
// added, proving the prior test's failure was really about verification
// and not some other broken precondition (a falsifiable negative control).
func TestConnect_HopChain_UnverifiedTargetKeySucceedsOnceKeyIsAdded(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "", "", 0
	})
	targetAddr, targetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "target-reached", "", 0
	})

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		bastionAddr: bastionKey,
		targetAddr:  targetKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)

	hops := []Hop{
		{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth},
	}
	target := Target{Host: targetHost, Port: targetPort}

	result, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err != nil {
		t.Fatalf("expected the chain to succeed once the target's real key is present, got: %v", err)
	}
	if !strings.Contains(result.Stdout, "target-reached") {
		t.Fatalf("stdout = %q, want the target's own handler output", result.Stdout)
	}
}

// TestConnect_HopChain_UnreachableTargetThroughBastionFailsWithChannelError
// proves the OTHER dialThroughHop failure branch: the bastion's own
// handshake succeeds, but nothing is listening at the address the caller
// asked the bastion to forward to, so the bastion's real direct-tcpip
// forwarding (forwardDirectTCPIP's own net.Dial) fails and rejects the
// channel, which must surface client-side as a real, named error rather
// than hanging or panicking.
func TestConnect_HopChain_UnreachableTargetThroughBastionFailsWithChannelError(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "", "", 0
	})

	// Port 0: syntactically valid, and nothing can ever be listening on
	// it, because 0 is the sockets API's "assign me any free port" value
	// for bind. This used to open a listener and close it again, which
	// FAILURE_PATTERNS.md #123, #177 and #181 all record failing for
	// real on this project's own development host, where a just-released
	// loopback port keeps accepting connects.
	const unreachableAddr = "127.0.0.1:0"

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, InsecureSkipHostKeyVerify: false, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, unreachableAddr)

	hops := []Hop{
		{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth},
	}
	// The target's own key is never checked: the channel fails before any
	// SSH handshake with it is attempted, so no known_hosts entry for it
	// is needed for this test to isolate the channel-open failure alone.
	target := Target{Host: targetHost, Port: targetPort}
	targetAuth := testAuth

	_, err := r.Run(context.Background(), hops, target, targetAuth, "echo hi")
	if err == nil {
		t.Fatal("expected a channel-open failure against an address nothing is listening on")
	}
}

// TestConnect_HopChain_UnusableHopAuthRefusedBeforeDialing proves a hop
// with no usable authentication method is refused before any network I/O,
// naming that hop's own address rather than the target's.
func TestConnect_HopChain_UnusableHopAuthRefusedBeforeDialing(t *testing.T) {
	bastionAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	targetAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })

	r := New(Options{InsecureSkipHostKeyVerify: true, MaxRetries: 1})

	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)

	hops := []Hop{
		{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: Auth{}},
	}
	target := Target{Host: targetHost, Port: targetPort}

	_, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err == nil {
		t.Fatal("expected a hop with no usable Auth to be refused")
	}
	if !strings.Contains(err.Error(), "no usable authentication method") || !strings.Contains(err.Error(), bastionAddr) {
		t.Errorf("err = %v, want it to name the hop's own address %q, not the target's", err, bastionAddr)
	}
}

// TestDialThroughHop_TunneledConnRemoteAddrIsZero pins, by direct
// assertion rather than by reading golang.org/x/crypto's own source, the
// fact dialThroughHop's own doc comment relies on: a tunneled connection's
// RemoteAddr() is always the zero value, which is exactly why addr must be
// passed to ssh.NewClientConn explicitly for host key verification to
// check the real hop rather than degrading to "0.0.0.0:0".
func TestDialThroughHop_TunneledConnRemoteAddrIsZero(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "", "", 0 })
	targetAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) { return "reached", "", 0 })

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})
	hostKeyCB, err := hostKeyCallback(r.opts)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}

	bastionClient, err := r.dial(context.Background(), bastionAddr, testAuth.clientConfig(hostKeyCB, 2*time.Second))
	if err != nil {
		t.Fatalf("dial bastion: %v", err)
	}
	defer bastionClient.Close()

	rawConn, err := bastionClient.DialContext(context.Background(), "tcp", targetAddr)
	if err != nil {
		t.Fatalf("open direct-tcpip channel: %v", err)
	}
	defer rawConn.Close()

	if got := rawConn.RemoteAddr().String(); got != "0.0.0.0:0" {
		t.Fatalf("tunneled conn RemoteAddr() = %q, want the documented zero value 0.0.0.0:0", got)
	}
}
