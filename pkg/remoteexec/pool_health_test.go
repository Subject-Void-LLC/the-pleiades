// Tests for the checks a pooled connection passes before it is lent
// again: known_hosts unchanged, and the device still answering.
package remoteexec

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"
)

// TestPool_KnownHostsEditForcesAFreshCheck proves a pooled login is not
// trusted past a change to the file it was verified against: any edit
// dials again, and a host key an operator removed is refused at once
// rather than honored through the earlier login.
func TestPool_KnownHostsEditForcesAFreshCheck(t *testing.T) {
	srv := startPoolServer(t)
	kh := writeKnownHosts(t, srv.Addr(), srv.HostKey)
	r := New(Options{KnownHostsPath: kh})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	task(t, p, r, "web1", srv)
	task(t, p, r, "web1", srv)
	if got := srv.Logins(); got != 1 {
		t.Fatalf("unchanged known_hosts: %d logins, want 1", got)
	}

	f, err := os.OpenFile(kh, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(f, "# reviewed\n")
	_ = f.Close()
	task(t, p, r, "web1", srv)
	if got := srv.Logins(); got != 2 {
		t.Fatalf("after an edit: %d logins, want 2", got)
	}

	other := generateTestHostKey(t).PublicKey()
	if err := os.WriteFile(kh, []byte(knownhosts.Line([]string{srv.Addr()}, other)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Connect(context.Background(), r, "web1", target(srv), PasswordAuth(srv.Username, srv.Password)); err == nil {
		t.Fatal("Connect succeeded after the device's host key was replaced in known_hosts")
	}
	waitFor(t, "the earlier login to close", func() bool { return srv.Live() == 0 })
}

// TestPool_DeadConnectionIsReplacedBeforeAnyCommand proves a connection
// the device dropped while it sat in the pool is found dead by the
// keepalive and replaced, so the task's command runs rather than failing
// on a connection that was already gone.
func TestPool_DeadConnectionIsReplacedBeforeAnyCommand(t *testing.T) {
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })

	task(t, p, r, "web1", srv)
	srv.DropConnections()
	waitFor(t, "the drop", func() bool { return srv.Live() == 0 })
	task(t, p, r, "web1", srv)
	task(t, p, r, "web1", srv)
	if got := srv.Logins(); got != 2 {
		t.Fatalf("%d logins, want 2: one replacement, then reuse again", got)
	}
}

// TestPool_SilentDeviceIsNotPooledAgain uses a real TCP relay that stops
// carrying an established connection's bytes, as a stateful firewall
// that forgot the flow does, so the keepalive gets no reply. The task
// still runs, on a fresh login, and the device is not pooled again in
// this run: paying the keepalive timeout before every task would cost
// more than the login it saves.
func TestPool_SilentDeviceIsNotPooledAgain(t *testing.T) {
	srv := startPoolServer(t)
	relay := startStallRelay(t, srv.Addr())
	host, port := relay.hostPort(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	p.keepaliveTimeout = 200 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	auth := PasswordAuth(srv.Username, srv.Password)

	run := func() {
		t.Helper()
		conn, err := p.Connect(context.Background(), r, "web1", Target{Host: host, Port: port}, auth)
		if err != nil {
			t.Fatalf("Connect error = %v", err)
		}
		if _, err := conn.Run(context.Background(), "true"); err != nil {
			t.Fatalf("Run error = %v", err)
		}
		_ = conn.Close()
	}
	run()
	relay.stallExisting()
	run()
	run()
	if got := srv.Logins(); got != 3 {
		t.Fatalf("%d logins, want 3: the stalled one replaced, then no pooling", got)
	}
}

// stallRelay is a real TCP relay whose established connections can be
// made to stop carrying bytes while new ones still pass.
type stallRelay struct {
	ln    net.Listener
	mu    sync.Mutex
	flags []*atomic.Bool
	conns []net.Conn
}

// startStallRelay relays every connection on a loopback port to addr.
func startStallRelay(t *testing.T, addr string) *stallRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &stallRelay{ln: ln}
	go func() {
		for {
			down, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", addr)
			if err != nil {
				_ = down.Close()
				continue
			}
			stalled := new(atomic.Bool)
			s.mu.Lock()
			s.flags = append(s.flags, stalled)
			s.conns = append(s.conns, down, up)
			s.mu.Unlock()
			go pumpUnlessStalled(up, down, stalled)
			go pumpUnlessStalled(down, up, stalled)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	return s
}

// hostPort is the relay's address.
func (s *stallRelay) hostPort(t *testing.T) (string, int) {
	t.Helper()
	a := s.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// stallExisting stops every connection made so far from carrying bytes.
func (s *stallRelay) stallExisting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.flags {
		f.Store(true)
	}
}

// pumpUnlessStalled copies src to dst, dropping bytes once stalled is
// set, and closes both when src ends.
func pumpUnlessStalled(dst, src net.Conn, stalled *atomic.Bool) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !stalled.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				err = werr
			}
		}
		if err != nil {
			_ = dst.Close()
			_ = src.Close()
			return
		}
	}
}

// TestPool_UnresolvableTrustIsNotPooled covers a known_hosts that cannot
// be read or found: the Pool does not pool, and the dial it falls back to
// reports the problem itself.
func TestPool_UnresolvableTrustIsNotPooled(t *testing.T) {
	srv := startPoolServer(t)
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	auth := PasswordAuth(srv.Username, srv.Password)

	missing := New(Options{KnownHostsPath: filepath.Join(t.TempDir(), "absent")})
	if _, err := p.Connect(context.Background(), missing, "web1", target(srv), auth); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Connect with a missing known_hosts returned %v, want the dial's own refusal", err)
	}

	t.Setenv("HOME", "")
	t.Setenv(KnownHostsEnv, "")
	if _, err := p.Connect(context.Background(), New(Options{}), "web1", target(srv), auth); err == nil {
		t.Error("Connect with no known_hosts source at all succeeded")
	}
	if len(p.entries) != 0 {
		t.Errorf("%d connections pooled with no verifiable trust", len(p.entries))
	}
}

// TestPool_ExpireLeavesALentConnection covers an idle timer that fires
// after its connection was lent again: the borrower keeps it.
func TestPool_ExpireLeavesALentConnection(t *testing.T) {
	srv := startPoolServer(t)
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	conn, err := p.Connect(context.Background(), New(Options{InsecureSkipHostKeyVerify: true}), "web1", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	e := p.entries["web1"]
	p.expire("web1", e)
	if p.entries["web1"] != e {
		t.Fatal("expire removed a connection that was lent")
	}
	if _, err := conn.Run(context.Background(), "true"); err != nil {
		t.Fatalf("the borrower's connection stopped working: %v", err)
	}
	_ = conn.Close()
}
