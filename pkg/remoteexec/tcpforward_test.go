// Tests for Conn.DialTCP against remoteexectest's real in-process SSH
// server, which genuinely forwards a direct-tcpip channel: an endpoint the
// far host reaches, one it does not, and an address that is not one.
package remoteexec

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
)

// TestDialTCP_ReachesAnEndpointAndNamesARefusal opens a channel to a live
// listener and reads its echo, then to a closed port, whose refusal names
// both causes sshd's one answer may mean, and the login stays reusable.
func TestDialTCP_ReachesAnEndpointAndNamesARefusal(t *testing.T) {
	srv := startPoolServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
	}()
	closedAddr := refusing.Addr(t)

	r := New(Options{InsecureSkipHostKeyVerify: true})
	p := NewPool(0)
	t.Cleanup(func() { _ = p.Close() })
	conn, err := p.Connect(context.Background(), r, "box", target(srv), PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := conn.DialTCP(context.Background(), ln.Addr().String())
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	_, _ = ch.Write([]byte("ping"))
	if got, err := io.ReadAll(ch); err != nil || string(got) != "ping" {
		t.Fatalf("read %q, %v; want the echo", got, err)
	}
	_ = ch.Close()

	if _, err := conn.DialTCP(context.Background(), closedAddr); err == nil || !strings.Contains(err.Error(), "refused or is unreachable from the device") {
		t.Fatalf("a closed port = %v, want both causes named", err)
	}
	for _, bad := range []string{"db", ":5432", "db:0", "db:99999", "a b:1"} {
		if _, err := conn.DialTCP(context.Background(), bad); err == nil {
			t.Errorf("DialTCP(%q) was attempted", bad)
		}
	}
	_ = conn.Close()
	task(t, p, r, "box", srv)
	if got := srv.Logins(); got != 1 {
		t.Fatalf("%d logins, want 1: a refused channel is the server answering, not the login breaking", got)
	}
}
