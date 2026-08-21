package remoteexec

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startFakeSSHListener starts a real loopback TCP listener speaking SSH
// (via serveOneFakeConnection, runner_test.go) and serves connections until
// the test ends. Unlike newFakeSSHServer (which hands out a dialFunc
// backed by a single loopback pair per call), this exposes a real
// "host:port" address so realDial itself, the default dialFunc New
// wires up, can be exercised directly: a genuine net.Dialer.DialContext
// TCP dial followed by a genuine SSH handshake, with no Docker
// dependency (this is loopback-only, not the real, independent sshd
// internal/transport/ssh dials in a container).
func startFakeSSHListener(t testing.TB, handler func(command string) (stdout, stderr string, exitCode int)) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// The listener was closed by t.Cleanup above; this
				// goroutine's job is done.
				return
			}
			go serveOneFakeConnection(conn, config, handler)
		}
	}()

	return listener.Addr().String(), hostSigner.PublicKey()
}

// TestRealDial_ConnectsOverLoopbackTCP exercises the real, default
// dialFunc end to end: an actual TCP dial via net.Dialer.DialContext
// followed by an actual SSH handshake, entirely over loopback (no
// Docker required). This is what New wires Runner.dial to; every other
// test in this package substitutes a fake dial specifically to avoid
// exercising this function, so it needs its own direct coverage.
func TestRealDial_ConnectsOverLoopbackTCP(t *testing.T) {
	addr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "real-dial-ok", "", 0
	})

	config := &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	client, err := realDial(context.Background(), addr, config)
	if err != nil {
		t.Fatalf("realDial failed: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("failed to open session: %v", err)
	}
	defer session.Close()

	var out bytes.Buffer
	session.Stdout = &out
	if err := session.Run("echo hi"); err != nil {
		t.Fatalf("session.Run failed: %v", err)
	}
	if !strings.Contains(out.String(), "real-dial-ok") {
		t.Errorf("expected stdout to contain %q, got %q", "real-dial-ok", out.String())
	}
}

// TestRealDial_TCPDialFailure proves realDial reports a clean, wrapped
// error when the TCP dial itself fails (nothing listening on the target
// port), rather than hanging or panicking.
func TestRealDial_TCPDialFailure(t *testing.T) {
	config := &ssh.ClientConfig{Timeout: 500 * time.Millisecond}
	// Port 1 is a well-known reserved port loopback almost never has a
	// listener on; connecting (as opposed to binding) needs no
	// privilege, and a refused connection fails near-instantly.
	_, err := realDial(context.Background(), "127.0.0.1:1", config)
	if err == nil {
		t.Fatal("expected an error dialing a port with nothing listening")
	}
}

// TestRealDial_ContextCanceledAbortsHandshake proves that canceling ctx
// while a TCP connection is established but the SSH handshake has not
// yet completed aborts realDial promptly, via the ctx-watching goroutine
// that closes the underlying conn out from under an in-flight handshake.
func TestRealDial_ContextCanceledAbortsHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		close(accepted)
		// Deliberately never speak SSH on this connection: the client
		// side's handshake blocks waiting for a server version string
		// that never arrives, until ctx cancellation closes its end.
		<-context.Background().Done()
		conn.Close()
	}()

	config := &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second, // large on purpose: ctx, not this fallback, must be what aborts the call
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = realDial(ctx, listener.Addr().String(), config)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when ctx is canceled mid-handshake")
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected ctx cancellation to abort the handshake promptly, took %v", elapsed)
	}

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("server side never accepted the connection; test setup is broken")
	}
}

// TestRealDial_ConfigTimeoutBoundsHandshakeWithNoCtxDeadline proves
// config.Timeout genuinely bounds the WHOLE dial (TCP connect plus SSH
// handshake), not just the TCP connect step, even when ctx itself
// carries no deadline at all (context.Background()). Without this, a
// caller that never sets its own context deadline would have no bound
// on a handshake against a target that accepts the TCP connection but
// never speaks SSH, exactly the failure mode observed against a stopped
// container mid-development of this package.
func TestRealDial_ConfigTimeoutBoundsHandshakeWithNoCtxDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Accept the TCP connection but deliberately never speak SSH on
		// it, holding it open until the test itself tears the listener
		// down.
		buf := make([]byte, 1)
		conn.Read(buf) //nolint:errcheck // intentionally block until the peer closes
	}()

	config := &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         150 * time.Millisecond,
	}

	start := time.Now()
	// context.Background() has no deadline of its own: only
	// config.Timeout can bound this call.
	_, err = realDial(context.Background(), listener.Addr().String(), config)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error once config.Timeout elapses with no SSH handshake ever completing")
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected config.Timeout to bound the handshake even with no ctx deadline, took %v", elapsed)
	}
}
