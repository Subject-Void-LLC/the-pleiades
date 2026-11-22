// Tests that TwoServers.Close cannot be held open by a connection that
// arrives while it runs.
package remoteexectest

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestTwoServers_ACloseIsNotHeldOpenByALateConnection: a connection track
// sees after Close is closed at once, and Close returns while clients keep
// dialing the listener.
func TestTwoServers_ACloseIsNotHeldOpenByALateConnection(t *testing.T) {
	behind, err := Start(Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer behind.Close()
	ts, err := StartTwoServers(behind)
	if err != nil {
		t.Fatalf("StartTwoServers: %v", err)
	}

	// Clients dial for as long as Close runs, so some arrive during it.
	stop := make(chan struct{})
	var dialing sync.WaitGroup
	dialing.Add(1)
	go func() {
		defer dialing.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if c, err := net.DialTimeout("tcp", ts.Addr(), time.Second); err == nil {
				_ = c.Close()
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() { ts.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return while clients were dialing")
	}
	close(stop)
	dialing.Wait()

	// A connection tracked after Close is closed at once.
	late, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	ts.track(late)
	// An open pipe end would block the write until its deadline, so only a
	// closed one answers io.ErrClosedPipe.
	_ = late.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := late.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("writing to a connection tracked after Close = %v, want it closed", err)
	}
}
