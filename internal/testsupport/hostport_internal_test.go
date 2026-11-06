// Tests for the lost published port: reading a socket of this user's on a
// host port from /proc/net/tcp, and a readiness wait that fails at once on
// one rather than after its bound.
package testsupport

import (
	"context"
	"errors"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
	// Root: the root listener (a forwarder that did bind) and the TIME_WAIT
	// (owned by no user) are not collisions.
	if got := socketsOwnedOn(text, 54998, 0); len(got) != 1 || got[0] != "TIME_WAIT" {
		t.Errorf("uid 0: %v, want only the TIME_WAIT, never a root listener", got)
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
// refuses (no listener), and the wait fails with ErrPublishedPortLost and
// the fix at once, not after its bound. The control is a free port that
// also refuses: the wait waits out its bound and says no socket holds it.
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

	for _, g := range []*greeting{
		{port: "22/tcp", prefix: "SSH-", protocol: "SSH", timeout: 30 * time.Second},
		{port: "5432/tcp", protocol: "Postgres", timeout: 30 * time.Second, connectOnly: true},
	} {
		start := time.Now()
		err := g.WaitUntilReady(context.Background(), fakeTarget{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(held)), running: true})
		if !errors.Is(err, ErrPublishedPortLost) || !strings.Contains(err.Error(), "ip_local_port_range") || !strings.Contains(err.Error(), "ESTABLISHED") {
			t.Fatalf("%s: err = %v, want ErrPublishedPortLost naming the holder and the fix", g.protocol, err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%s: a lost port took %v to be reported, want at once", g.protocol, took)
		}
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()
	g := &greeting{port: "22/tcp", prefix: "SSH-", protocol: "SSH", timeout: time.Second}
	err = g.WaitUntilReady(context.Background(), fakeTarget{addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort)), running: true})
	if err == nil || errors.Is(err, ErrPublishedPortLost) || !strings.Contains(err.Error(), "no socket of this user holds host port "+strconv.Itoa(freePort)) {
		t.Errorf("a refused port nothing holds: %v, want the bound's failure saying no socket holds it", err)
	}
	if !strings.Contains(hostSideEvidence(held), "Docker Desktop's forwarder cannot bind it") {
		t.Errorf("the evidence for a held port: %q", hostSideEvidence(held))
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
