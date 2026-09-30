// A host that never answers is reported as a failure before anything ran.
package winrmexec

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestExecute_ADeadlineBeforeAnyShellIsNotStarted proves a host that never
// answers is reported as a failure before anything ran.
//
// The listener accepts the connection and then says nothing, which is
// what a firewall that drops packets looks like once the TCP handshake is
// past, and what the lab host does to a closed port. Execute gives up at
// its bound with no shell open, so no command was ever sent: the error
// must be a *NotStartedError wrapping context.DeadlineExceeded, which is
// what lets a retry and a circuit breaker see it, and must not claim the
// command may still be running.
func TestExecute_ADeadlineBeforeAnyShellIsNotStarted(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		// Closing the held connections releases the goroutine Execute
		// leaves parked in the library's request.
		for _, c := range held {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	_, err = Execute(context.Background(), Target{Host: "127.0.0.1", Port: port}, Auth{Username: "u", Password: "p"},
		Command{Shell: ShellNone, Script: "prog"}, Options{Timeout: 300 * time.Millisecond})

	var notStarted *NotStartedError
	if !errors.As(err, &notStarted) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a *NotStartedError wrapping context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "never sent") || strings.Contains(err.Error(), "may still be running") {
		t.Errorf("err = %v, want it to say the command was never sent", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1") || !strings.Contains(err.Error(), "300ms") {
		t.Errorf("err = %v, want it to name the host and the bound", err)
	}
}
