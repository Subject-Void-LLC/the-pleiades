// Tests that the client's socket for an IPv4 server is an IPv4 one, so no
// other socket can share its port and take the server's replies (macOS), and
// the resolution refusals newClient adds.
package tftpxfer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tftpxfer"
)

// errorPacket is a TFTP ERROR packet (RFC 1350): opcode 5, error code 0
// (not defined), the message, and its terminating zero.
func errorPacket(message string) []byte {
	return append(append([]byte{0, 5, 0, 0}, message...), 0)
}

// silentServer listens on 127.0.0.1 and answers nothing until told. It
// returns its port; a function that waits for the first request and returns
// the address it came from; and one that answers that address with a TFTP
// error, which ends the client's transfer at once, so a test need not wait
// out the client's timeout.
func silentServer(t *testing.T) (port int, request func() *net.UDPAddr, refuse func(to *net.UDPAddr, message string)) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	request = func() *net.UDPAddr {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, from, err := conn.ReadFromUDP(make([]byte, 1<<16))
		if err != nil {
			t.Fatalf("no request reached the server: %v", err)
		}
		return from
	}
	refuse = func(to *net.UDPAddr, message string) {
		if _, err := conn.WriteToUDP(errorPacket(message), to); err != nil {
			t.Fatalf("answering the client: %v", err)
		}
	}
	return conn.LocalAddr().(*net.UDPAddr).Port, request, refuse
}

// wsaeaddrinuse is Windows' WSAEADDRINUSE. Go's syscall.EADDRINUSE on
// Windows is a constant of its own that no socket call returns, so
// errors.Is never matches the error Windows does return.
const wsaeaddrinuse = 10048

// addrInUse reports whether err says the address is already in use, on
// every operating system.
func addrInUse(err error) bool {
	var errno syscall.Errno
	return errors.Is(err, syscall.EADDRINUSE) || (errors.As(err, &errno) && errno == wsaeaddrinuse)
}

// TestAnIPv4ClientSocketLeavesItsPortFreeOnIPv6 catches the client using a
// dual-stack wildcard socket for an IPv4 server. While a Get and then a Put
// wait for a reply, the test binds the IPv6 loopback on the very port the
// request came from. A dual-stack [::] socket holds that port for IPv6 too,
// so the bind fails on every kernel; an IPv4 socket does not. A dual-stack
// socket is the one macOS lets share its port with an IPv4 socket bound to
// 127.0.0.1, which then receives every reply (localaddr.go).
//
// The client is provably still holding its socket when the bind is tried:
// its timeout is far longer than the test takes, and the server's error
// reply, sent only after the bind, must reach it and end the call, which a
// client that had closed its socket would never see. A server written as
// an IPv4-mapped IPv6 literal takes the IPv4 path too.
func TestAnIPv4ClientSocketLeavesItsPortFreeOnIPv6(t *testing.T) {
	probe, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	need.Require(t, "ipv6-loopback", err == nil, fmt.Sprintf("the test binds [::1] beside the client's socket (%v)", err))
	_ = probe.Close()

	opts := tftpxfer.Options{Timeout: 30 * time.Second, Retries: 1}
	calls := map[string]func(host string, port int) error{
		"Get": func(host string, port int) error {
			_, err := tftpxfer.Get(context.Background(), host, port, opts, "fw.bin", &bytes.Buffer{})
			return err
		},
		"Put": func(host string, port int) error {
			_, err := tftpxfer.Put(context.Background(), host, port, opts, "fw.bin", strings.NewReader("x"))
			return err
		},
	}
	for name, call := range calls {
		for _, host := range []string{"127.0.0.1", "::ffff:127.0.0.1"} {
			t.Run(name+" "+host, func(t *testing.T) {
				port, request, refuse := silentServer(t)
				done := make(chan error, 1)
				go func() { done <- call(host, port) }()
				client := request()
				v6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: client.Port})
				if v6 != nil {
					_ = v6.Close()
				}
				switch {
				case addrInUse(err):
					t.Errorf("the client's socket on port %d also holds it for IPv6: it is a dual-stack wildcard socket, "+
						"which macOS lets share its port with an IPv4 socket that then takes the server's replies", client.Port)
				case err != nil:
					t.Errorf("binding [::1]:%d: %v", client.Port, err)
				}
				const message = "the test ends this transfer"
				refuse(client, message)
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), message) {
						t.Errorf("the call ended with %v, want the server's error: the client was not on its socket when the bind was tried", err)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("the server's error never reached the client, so its socket was not the one the request came from")
				}
			})
		}
	}
}

// TestTransfersSurviveIPv4SocketsHoldingManyPorts is the symptom itself: with
// IPv4 loopback sockets holding a large share of the ephemeral ports, every
// transfer must still finish. On macOS a dual-stack client socket shares a
// port with one of them about once in eight transfers here, and then times
// out; on Linux the kernel never lets it, so this passes there either way.
// A shared port loses every reply whatever the timeout, so the timeout is
// long enough for a slow runner under -race and still costs a real failure
// only a few seconds.
func TestTransfersSurviveIPv4SocketsHoldingManyPorts(t *testing.T) {
	const held = 2000
	for i := 0; i < held; i++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
			need.Require(t, "open-files", false, fmt.Sprintf(
				"the test holds %d sockets open, and the open-file limit stopped it at %d (%v); raise it with ulimit -n", held, i, err))
		}
		if err != nil {
			t.Fatalf("holding port %d of %d: %v", i, held, err)
		}
		t.Cleanup(func() { _ = c.Close() })
	}
	port, files := testServer(t)
	opts := tftpxfer.Options{Timeout: 3 * time.Second, Retries: 2}
	failed := 0
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("file-%d.bin", i)
		files.Store(name, []byte(name))
		var got bytes.Buffer
		if _, err := tftpxfer.Get(context.Background(), "127.0.0.1", port, opts, name, &got); err != nil {
			failed++
			t.Logf("Get %s: %v", name, err)
		} else if got.String() != name {
			t.Errorf("Get %s returned %q", name, got.String())
		}
	}
	if failed > 0 {
		t.Errorf("%d of 40 transfers failed while IPv4 sockets held other ports", failed)
	}
}

// TestAnIPv6ServerRoundTrips proves an IPv6 server still works through the
// library's own wildcard socket, which this package keeps for IPv6.
func TestAnIPv6ServerRoundTrips(t *testing.T) {
	probe, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	need.Require(t, "ipv6-loopback", err == nil, fmt.Sprintf("the test serves on [::1] (%v)", err))
	_ = probe.Close()

	port, files := testServerOn(t, "[::1]:0")
	ctx := context.Background()
	if _, err := tftpxfer.Put(ctx, "::1", port, tftpxfer.Options{}, "v6.bin", strings.NewReader("over ipv6")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored, ok := files.Load("v6.bin"); !ok || string(stored.([]byte)) != "over ipv6" {
		t.Fatalf("server stored %v, want %q", stored, "over ipv6")
	}
	var got bytes.Buffer
	if _, err := tftpxfer.Get(ctx, "::1", port, tftpxfer.Options{}, "v6.bin", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.String() != "over ipv6" {
		t.Errorf("Get returned %q, want %q", got.String(), "over ipv6")
	}
}

// TestAHostThatDoesNotResolveIsRefused covers newClient's own resolution: a
// name with no address comes back as an error naming it, before any socket.
func TestAHostThatDoesNotResolveIsRefused(t *testing.T) {
	_, err := tftpxfer.Get(context.Background(), "no-such-host.invalid", 69, tftpxfer.Options{}, "fw.bin", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "resolving no-such-host.invalid:69") {
		t.Errorf("error = %v, want a resolution failure naming the host", err)
	}
}
