// Tests that waiting for a broker's greeting is bounded, retries past a
// port that is not answering yet, and accepts only a NATS greeting.
package testsupport

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// greetingServer listens on a loopback port; the first drop connections
// are closed at once, and every later one is sent greeting, or nothing
// when greeting is empty.
func greetingServer(t *testing.T, drop int, greeting string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	go func() {
		for n := 0; ; n++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if n < drop {
				_ = conn.Close()
				continue
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
			if greeting != "" {
				_, _ = conn.Write([]byte(greeting))
			}
		}
	}()
	return ln.Addr().String()
}

// TestWaitForNATSGreeting covers each answer a published port can give.
func TestWaitForNATSGreeting(t *testing.T) {
	const info = "INFO {\"server_id\":\"x\"}\r\n"
	if err := waitForNATSGreeting(greetingServer(t, 0, info), time.Second, 200*time.Millisecond); err != nil {
		t.Errorf("a broker's greeting: %v", err)
	}
	if err := waitForNATSGreeting(greetingServer(t, 3, info), 5*time.Second, 200*time.Millisecond); err != nil {
		t.Errorf("a port that closed its first connections, then answered: %v", err)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := closed.Addr().String()
	_ = closed.Close()

	for name, tc := range map[string]struct{ addr, want string }{
		"a silent server":        {greetingServer(t, 0, ""), "within 1s"},
		"a refusing port":        {refused, "within 1s"},
		"another protocol":       {greetingServer(t, 0, "SSH-2.0-OpenSSH_9.6\r\n"), "not a NATS greeting"},
		"a port that only drops": {greetingServer(t, 1<<30, info), "within 1s"},
	} {
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			err := waitForNATSGreeting(tc.addr, time.Second, 200*time.Millisecond)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Errorf("gave up after %v, want about a second", elapsed)
			}
		})
	}
}
