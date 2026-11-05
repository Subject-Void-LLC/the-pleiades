//go:build !windows

// Tests for ConnectVia, a connection whose TCP leg a route's dialer opens,
// and for the two ways run ends that are neither a status nor the caller's
// cancellation: the context ending while the session is opened, and the
// connection dropping mid-command.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestConnectVia_RunsThroughTheDialerItIsGiven: the dialer reaches the
// server whatever address it is asked for, so a target address nothing
// listens on is reached through it, and the dialer is asked for that address.
func TestConnectVia_RunsThroughTheDialerItIsGiven(t *testing.T) {
	srv := startPoolServer(t)
	var asked []string
	via := NetDial(func(ctx context.Context, network, address string) (net.Conn, error) {
		asked = append(asked, network+" "+address)
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Addr())
	})
	r := New(Options{InsecureSkipHostKeyVerify: true})
	conn, err := r.ConnectVia(context.Background(), via, Target{Host: "inner.example", Port: 2222}, PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("ConnectVia: %v", err)
	}
	res, err := conn.Run(context.Background(), "echo via")
	if err != nil || strings.TrimSpace(res.Stdout) != "via" {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	// A detached command's redial goes through the same dialer.
	again, err := conn.redial(context.Background())
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	_ = again.Close()
	_ = conn.Close()
	if len(asked) != 2 || asked[0] != "tcp inner.example:2222" {
		t.Fatalf("the dialer was asked for %q, want inner.example:2222 twice", asked)
	}
}

// TestConnectVia_WithNoDialerIsADirectConnection: a nil dialer is Connect.
func TestConnectVia_WithNoDialerIsADirectConnection(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).ConnectVia(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("ConnectVia(nil): %v", err)
	}
	_ = conn.Close()
}

// TestConnectVia_RefusesWhatTheRouteCannotReachOrVerify: a dialer that fails
// fails the connection naming the route, and a target whose host key
// known_hosts does not name is refused through the route as directly.
func TestConnectVia_RefusesWhatTheRouteCannotReachOrVerify(t *testing.T) {
	srv := startPoolServer(t)
	auth := PasswordAuth(srv.Username, srv.Password)
	broken := NetDial(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("bastion refused") })
	if _, err := New(Options{InsecureSkipHostKeyVerify: true}).ConnectVia(context.Background(), broken, target(srv), auth); err == nil || !strings.Contains(err.Error(), "through its route") {
		t.Fatalf("ConnectVia through a failing dialer = %v; want an error naming the route", err)
	}

	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	via := NetDial(func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Addr())
	})
	_, err := New(Options{KnownHostsPath: empty}).ConnectVia(context.Background(), via, Target{Host: "inner.example", Port: 2222}, auth)
	if err == nil || !strings.Contains(err.Error(), "key is unknown") {
		t.Fatalf("ConnectVia to a target known_hosts does not name = %v; want a host key refusal", err)
	}
}

// TestRun_AContextEndingWhileTheSessionOpensIsACancellation: a server slow to
// confirm the session's channel answers after the context ended, but before
// the abandon grace closes the connection. The session it opened is closed,
// nothing is sent, and the error is the context's.
func TestRun_AContextEndingWhileTheSessionOpensIsACancellation(t *testing.T) {
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(generateTestHostKey(t))
	execs := make(chan string, 1)
	serve := func(conn net.Conn) {
		sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer func() { _ = sConn.Close() }()
		go ssh.DiscardRequests(reqs)
		for newChannel := range chans {
			// Confirmed late: after the client's context ends.
			time.Sleep(500 * time.Millisecond)
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer func() { _ = channel.Close() }()
				for req := range requests {
					if req.Type == "exec" {
						execs <- string(req.Payload)
					}
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}()
		}
	}
	dial := func(_ context.Context, addr string, clientConfig *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go serve(serverConn)
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
	conn, err := newTestRunner(dial, Options{}).Connect(context.Background(), nil, testTarget, testAuth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := conn.Run(ctx, "touch /tmp/never"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v; want context.DeadlineExceeded", err)
	}
	select {
	case got := <-execs:
		t.Fatalf("the command was sent after its context ended: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRun_AConnectionDroppedMidCommandIsAnErrorNotAStatus: the server closing
// the connection while a command runs is an error naming the run, never an
// exit code, and the caller's context did not end.
func TestRun_AConnectionDroppedMidCommandIsAnErrorNotAStatus(t *testing.T) {
	srv := startPoolServer(t)
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).Connect(context.Background(), nil, target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		time.Sleep(300 * time.Millisecond)
		srv.DropConnections()
	}()
	// Short: the server's Close, at the test's end, waits for its own
	// command to finish.
	res, err := conn.Run(context.Background(), "sleep 2")
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run on a dropped connection = %+v, %v; want a transport error", res, err)
	}
}

// routeThrough is the dialer a route gives a method (internal/engine's
// routeDial): DialThroughHops through bastion, on r, for whatever address it
// is asked for.
func routeThrough(r *Runner, bastion Target, auth Auth) NetDial {
	hops := []Hop{{Target: bastion, Auth: auth, InsecureSkipHostKeyVerify: true}}
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, err
		}
		return r.DialThroughHops(ctx, hops, Target{Host: host, Port: port})
	}
}

// TestConnectVia_ARoutedTargetsCircuitRecovers: once a routed target's
// circuit has opened and its cooldown passed, the next connection through
// the route closes it. The route's dialer guards the target's address on the
// same Runner, and a second guard on the same dial took the half-open probe
// the dialer then found in flight, so the circuit reopened on every attempt.
func TestConnectVia_ARoutedTargetsCircuitRecovers(t *testing.T) {
	bastion := startPoolServer(t)
	inner := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true, BreakerThreshold: 2, BreakerCooldown: 200 * time.Millisecond})
	auth := PasswordAuth(bastion.Username, bastion.Password)
	via := routeThrough(r, target(bastion), auth)

	// The target was unreachable long enough to open its circuit.
	for range 2 {
		r.breaker.RecordFailure(inner.Addr())
	}
	time.Sleep(300 * time.Millisecond)
	conn, err := r.ConnectVia(context.Background(), via, target(inner), PasswordAuth(inner.Username, inner.Password))
	if err != nil {
		t.Fatalf("ConnectVia after the cooldown: %v; the circuit never recovers", err)
	}
	_ = conn.Close()
	if !r.breaker.Permitted(inner.Addr()) {
		t.Fatal("the circuit is still open after a connection succeeded")
	}
}

// TestRun_ACancelledCommandThroughAStalledBastionIsBounded: a cancelled
// command on a routed connection whose bastion stopped answering returns
// within the abandon grace. Closing the target's client waits on the route's
// tunnel, which a silent bastion never ends; the route is cut from its first
// hop instead.
func TestRun_ACancelledCommandThroughAStalledBastionIsBounded(t *testing.T) {
	bastion := startPoolServer(t)
	inner := startPoolServer(t)
	port, freeze := stallingProxy(t, bastion.Addr())
	r := New(Options{InsecureSkipHostKeyVerify: true})
	via := routeThrough(r, Target{Host: "127.0.0.1", Port: port}, PasswordAuth(bastion.Username, bastion.Password))
	conn, err := r.ConnectVia(context.Background(), via, target(inner), PasswordAuth(inner.Username, inner.Password))
	if err != nil {
		t.Fatalf("ConnectVia: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(ctx, "sleep 3")
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	freeze()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want context.Canceled", err)
		}
	case <-time.After(abandonGrace + 15*time.Second):
		t.Fatal("Run never returned: the route's tunnel was waited on")
	}
}

// TestConnectVia_RefusesBeforeDialingWhatCannotBeChecked: no usable
// credential, or a known_hosts that cannot be read, refuses before the
// route's dialer is called.
func TestConnectVia_RefusesBeforeDialingWhatCannotBeChecked(t *testing.T) {
	called := false
	via := NetDial(func(context.Context, string, string) (net.Conn, error) {
		called = true
		return nil, errors.New("dialed")
	})
	if _, err := New(Options{InsecureSkipHostKeyVerify: true}).ConnectVia(context.Background(), via, Target{Host: "h", Port: 22}, Auth{}); err == nil || !strings.Contains(err.Error(), "no usable authentication") {
		t.Fatalf("ConnectVia with no credential = %v; want a refusal", err)
	}
	missing := filepath.Join(t.TempDir(), "absent", "known_hosts")
	if _, err := New(Options{KnownHostsPath: missing}).ConnectVia(context.Background(), via, Target{Host: "h", Port: 22}, PasswordAuth("u", "p")); err == nil || !strings.Contains(err.Error(), "host key verification unavailable") {
		t.Fatalf("ConnectVia with an unreadable known_hosts = %v; want a refusal", err)
	}
	if called {
		t.Fatal("the route's dialer was called for a connection that could not be checked")
	}
}

// TestRun_ACancelledCommandThroughAPlainRouteIsBounded: a route whose
// dialer hands back a plain connection (no tunnel of this package's) is cut
// by closing that connection, so a cancelled command on a server that stopped
// answering still returns within the abandon grace.
func TestRun_ACancelledCommandThroughAPlainRouteIsBounded(t *testing.T) {
	inner := startPoolServer(t)
	port, freeze := stallingProxy(t, inner.Addr())
	via := NetDial(func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	})
	conn, err := New(Options{InsecureSkipHostKeyVerify: true}).ConnectVia(context.Background(), via, target(inner), PasswordAuth(inner.Username, inner.Password))
	if err != nil {
		t.Fatalf("ConnectVia: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(ctx, "sleep 3")
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	freeze()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want context.Canceled", err)
		}
	case <-time.After(abandonGrace + 15*time.Second):
		t.Fatal("Run never returned")
	}
}
