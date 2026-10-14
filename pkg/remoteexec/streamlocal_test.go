// Tests for Conn.DialUnix, the channel to a Unix socket on the far host
// that a container engine's socket carrier rides, against remoteexectest's
// real in-process SSH server: what a clean, a refused and a canceled open
// each leave behind on a pooled login.
package remoteexec

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are Phase 119a's failure-mode walk for DialUnix on a pooled
// connection: which outcomes leave the login fit to lend again and which do
// not, counted by the server's own logins. The refusals OpenSSH itself gives
// are proven against a real sshd in internal/testsupport's
// TestDockerHost_SSHDRefusals.

// echoSocket listens on a Unix socket in a short directory and echoes one
// line back on each connection, then closes it.
func echoSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ul")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = c.Write(buf[:n])
			}()
		}
	}()
	return path
}

// TestDialUnix_ACleanChannelLeavesTheLoginReusable proves a channel that
// carried its bytes and ended cleanly does not cost the next task a login.
func TestDialUnix_ACleanChannelLeavesTheLoginReusable(t *testing.T) {
	srv := startPoolServer(t)
	socket := echoSocket(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	conn, err := p.Connect(context.Background(), r, "box", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := conn.DialUnix(context.Background(), socket)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	if _, err := ch.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(ch)
	if err != nil || string(got) != "ping" {
		t.Fatalf("read %q, %v; want the echo and a clean end", got, err)
	}
	_ = ch.Close()
	_ = conn.Close()

	task(t, p, r, "box", srv)
	if got := srv.Logins(); got != 1 {
		t.Fatalf("%d logins, want 1: a clean channel must not taint the login", got)
	}
}

// TestDialUnix_ARefusedOpenLeavesTheLoginReusable proves sshd refusing the
// channel (here, no such socket) is the server answering, not the login
// breaking, so the next task reuses it.
func TestDialUnix_ARefusedOpenLeavesTheLoginReusable(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	conn, err := p.Connect(context.Background(), r, "box", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.DialUnix(context.Background(), filepath.Join(t.TempDir(), "absent.sock"))
	if err == nil || !strings.Contains(err.Error(), "connect failed") {
		t.Fatalf("DialUnix to an absent socket: %v, want sshd's connect-failed answer", err)
	}
	_ = conn.Close()

	task(t, p, r, "box", srv)
	if got := srv.Logins(); got != 1 {
		t.Fatalf("%d logins, want 1: a refused channel must not taint the login", got)
	}
}

// TestDialUnix_ACanceledOpenTaintsTheLogin proves an open its context cut
// off leaves the login closed rather than lent again, since whether the far
// side opened the channel is unknown.
func TestDialUnix_ACanceledOpenTaintsTheLogin(t *testing.T) {
	srv := startPoolServer(t)
	socket := echoSocket(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	conn, err := p.Connect(context.Background(), r, "box", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := conn.DialUnix(ctx, socket); !errors.Is(err, context.Canceled) {
		t.Fatalf("DialUnix with a canceled context: %v, want context.Canceled", err)
	}
	_ = conn.Close()
	waitFor(t, "the tainted login to close", func() bool { return srv.Live() == 0 })

	task(t, p, r, "box", srv)
	if got := srv.Logins(); got != 2 {
		t.Fatalf("%d logins, want 2: the cut-off open's login replaced", got)
	}
}
