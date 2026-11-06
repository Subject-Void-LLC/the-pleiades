// Tests for the lost published port: reading a socket of this user's on a
// host port from /proc/net/tcp, and a readiness wait that fails at once on
// one rather than after its bound.
package testsupport

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
)

// TestSocketsOwnedOn reads /proc/net/tcp and tcp6 lines as the kernel
// writes them: the local port in hex after the address, the state, and the
// owner's uid in the eighth field.
func TestSocketsOwnedOn(t *testing.T) {
	const text = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:D6D6 0100007F:9C40 01 00000000:00000000 00:00000000 00000000  1000        0 111 1
   1: 0100007F:D6D6 0100007F:9C41 06 00000000:00000000 03:00000000 00000000     0        0 0 3
   2: 00000000:D6D6 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 222 1
   3: 0100007F:1F90 0100007F:D6D6 01 00000000:00000000 00:00000000 00000000  1000        0 333 1
   4: 00000000000000000000000001000000:D6D6 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 444 1
   5: 0100007F:D6D6 0100007F:9C42 01 00000000:00000000 00:00000000 00000000  1001        0 555 1
`
	// 0xD6D6 is 54998; line 3 has it as the remote port only.
	if got, want := socketsOwnedOn(text, 54998, 1000), []string{"ESTABLISHED", "LISTEN"}; !reflect.DeepEqual(got, want) {
		t.Errorf("uid 1000: %v, want %v (its client socket and its tcp6 listener, not the remote-port match)", got, want)
	}
	// A TIME_WAIT is left out for every owner (it does not block an
	// SO_REUSEADDR bind), so even read for uid 0 only the root listener and
	// nothing else shows; ownSocketsOn never reads for root at all.
	if got := socketsOwnedOn(text, 54998, 0); len(got) != 1 || got[0] != "LISTEN" {
		t.Errorf("uid 0: %v, want only the root listener, never the TIME_WAIT", got)
	}
	if got := socketsOwnedOn(text, 40000, 1000); got != nil {
		t.Errorf("a port only remote (0x9C40): %v, want none", got)
	}
	if got := socketsOwnedOn(text, 8080, 1000); len(got) != 1 {
		t.Errorf("line 3's own local port 8080: %v, want its one socket", got)
	}
	if got := socketsOwnedOn("garbage\n\n", 54998, 1000); got != nil {
		t.Errorf("garbage: %v", got)
	}
	if got := socketsOwnedOn("   0: 0100007F:D6D6 0100007F:9C40 0C 0 0 0 1000 0 1 1", 54998, 1000); len(got) != 1 || got[0] != "state 0C" {
		t.Errorf("an unknown state: %v", got)
	}
}

// TestGreeting_FailsAtOnceOnAPortASocketOfOursHolds is the collision as the
// kernel shows it: a real socket of this process holds local port P, P
// refuses (no listener), and the greeting, connect-only and NATS waits fail
// with ErrPublishedPortLost and the fix rather than after their bound. A
// container that stopped is still reported as stopped, and the control, a
// refused port /proc shows no holder for, waits out its bound.
func TestGreeting_FailsAtOnceOnAPortASocketOfOursHolds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/net/tcp, which only Linux has")
	}
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		for {
			c, err := server.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	holder, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	held := holder.LocalAddr().(*net.TCPAddr).Port

	if os.Getuid() == 0 {
		t.Skip("a root run reads no holder (hostport.go), so there is no fast failure to see")
	}
	// The bound is minutes, so only the fast path can return the sentinel:
	// the bound's own failure wraps the refusal, never ErrPublishedPortLost.
	heldAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(held))
	for _, g := range []*greeting{
		{port: "22/tcp", prefix: "SSH-", protocol: "SSH", timeout: 5 * time.Minute},
		{port: "5432/tcp", protocol: "Postgres", timeout: 5 * time.Minute, connectOnly: true},
	} {
		err := g.WaitUntilReady(context.Background(), fakeTarget{addr: heldAddr, running: true})
		if !errors.Is(err, ErrPublishedPortLost) || !strings.Contains(err.Error(), "ip_local_port_range") || !strings.Contains(err.Error(), "ESTABLISHED") {
			t.Fatalf("%s: err = %v, want ErrPublishedPortLost naming the holder and the fix", g.protocol, err)
		}
	}
	if err := waitForNATSGreeting(heldAddr, 5*time.Minute, time.Second); !errors.Is(err, ErrPublishedPortLost) {
		t.Errorf("NATS on a held port: %v, want ErrPublishedPortLost", err)
	}

	// A container that stopped is reported as that, even on a held port.
	g := &greeting{port: "22/tcp", prefix: "SSH-", protocol: "SSH", timeout: 5 * time.Minute}
	if err := g.WaitUntilReady(context.Background(), fakeTarget{addr: heldAddr, running: false, exit: 2}); err == nil || !strings.Contains(err.Error(), "stopped (exit 2)") {
		t.Errorf("a stopped container on a held port: %v, want it reported as stopped", err)
	}

	// The control: a bound, unlistened port, which refuses and which no
	// other socket can take, waits out its bound and says what it saw.
	control := refusing.Port(t)
	g = &greeting{port: "22/tcp", prefix: "SSH-", protocol: "SSH", timeout: time.Second}
	err = g.WaitUntilReady(context.Background(), fakeTarget{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(control)), running: true})
	if err == nil || errors.Is(err, ErrPublishedPortLost) || !strings.Contains(err.Error(), "lists no listening or connected socket of this user on host port "+strconv.Itoa(control)) {
		t.Errorf("a refused port with no holder /proc shows: %v, want the bound's failure saying what /proc showed", err)
	}
	if !strings.Contains(hostSideEvidence(held), "Docker Desktop's forwarder cannot bind it") {
		t.Errorf("the evidence for a held port: %q", hostSideEvidence(held))
	}
}

// TestLostHostPort_OnlyOnLoopback leaves a remote engine's port alone: a
// local socket with the same number there is a coincidence.
func TestLostHostPort_OnlyOnLoopback(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "127.0.0.2": true, "10.0.0.5": false, "docker.example": false, "": false} {
		if got := loopbackHost(host); got != want {
			t.Errorf("loopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
	if err := lostHostPort("port 22/tcp", "10.0.0.5", 1); err != nil {
		t.Errorf("a remote host: %v, want nil", err)
	}
	if err := lostAddr("x", "no port here"); err != nil {
		t.Errorf("an address that does not split: %v", err)
	}
	if err := lostAddr("x", "127.0.0.1:http"); err != nil {
		t.Errorf("a port that is not a number: %v", err)
	}
}

// TestForPublishedPort accepts a connection without a first line, and
// keeps the agreed bound so a wait.ForAll does not narrow it.
func TestForPublishedPort(t *testing.T) {
	quiet, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quiet.Close() })
	go func() {
		for {
			c, err := quiet.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	s := ForPublishedPort("5432/tcp", "Postgres")
	if err := s.WaitUntilReady(context.Background(), fakeTarget{addr: quiet.Addr().String(), running: true}); err != nil {
		t.Fatalf("a server that says nothing first: %v", err)
	}
	if g := s.(*greeting); *g.Timeout() != ContainerStartupTimeout {
		t.Errorf("bound %v, want ContainerStartupTimeout", *g.Timeout())
	}
}
