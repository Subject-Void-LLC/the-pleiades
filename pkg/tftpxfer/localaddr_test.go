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

// requestSource listens on 127.0.0.1 without ever answering and returns its
// port, plus a function that waits for the first request and returns the
// client port it came from.
func requestSource(t *testing.T) (port int, source func() int) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("net.ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().(*net.UDPAddr).Port, func() int {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, from, err := conn.ReadFromUDP(make([]byte, 1<<16))
		if err != nil {
			t.Fatalf("no request reached the server: %v", err)
		}
		return from.Port
	}
}

// TestAnIPv4ClientSocketLeavesItsPortFreeOnIPv6 catches the client using a
// dual-stack wildcard socket for an IPv4 server. While a Get and then a Put
// wait for a reply, the test binds the IPv6 loopback on the very port the
// request came from. A dual-stack [::] socket holds that port for IPv6 too,
// so the bind fails on every kernel; an IPv4 socket does not. A dual-stack
// socket is the one macOS lets share its port with an IPv4 socket bound to
// 127.0.0.1, which then receives every reply (localaddr.go).
func TestAnIPv4ClientSocketLeavesItsPortFreeOnIPv6(t *testing.T) {
	probe, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	need.Require(t, "ipv6-loopback", err == nil, fmt.Sprintf("the test binds [::1] beside the client's socket (%v)", err))
	_ = probe.Close()

	opts := tftpxfer.Options{Timeout: 300 * time.Millisecond, Retries: 1}
	calls := map[string]func(port int) error{
		"Get": func(port int) error {
			_, err := tftpxfer.Get(context.Background(), "127.0.0.1", port, opts, "fw.bin", &bytes.Buffer{})
			return err
		},
		"Put": func(port int) error {
			_, err := tftpxfer.Put(context.Background(), "127.0.0.1", port, opts, "fw.bin", strings.NewReader("x"))
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			port, source := requestSource(t)
			done := make(chan error, 1)
			go func() { done <- call(port) }()
			clientPort := source()
			// The client is still waiting for its reply, so its socket is open.
			v6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: clientPort})
			if v6 != nil {
				_ = v6.Close()
			}
			if errors.Is(err, syscall.EADDRINUSE) {
				t.Errorf("the client's socket on port %d also holds it for IPv6: it is a dual-stack wildcard socket, "+
					"which macOS lets share its port with an IPv4 socket that then takes the server's replies", clientPort)
			} else if err != nil {
				t.Fatalf("binding [::1]:%d: %v", clientPort, err)
			}
			// The server never answers, so the call ends in its timeout.
			if err := <-done; err == nil {
				t.Error("the call succeeded against a server that never answers")
			}
		})
	}
}

// TestTransfersSurviveIPv4SocketsHoldingManyPorts is the symptom itself: with
// IPv4 loopback sockets holding a large share of the ephemeral ports, every
// transfer must still finish. On macOS a dual-stack client socket shares a
// port with one of them about once in eight transfers here, and then times
// out; on Linux the kernel never lets it, so this passes there either way.
func TestTransfersSurviveIPv4SocketsHoldingManyPorts(t *testing.T) {
	for i := 0; i < 2000; i++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatalf("holding port %d of 2000: %v", i, err)
		}
		t.Cleanup(func() { _ = c.Close() })
	}
	port, files := testServer(t)
	opts := tftpxfer.Options{Timeout: 500 * time.Millisecond, Retries: 2}
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
