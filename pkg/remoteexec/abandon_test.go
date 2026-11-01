//go:build !windows

// Tests for a command abandoned at its context's end (conn.go's
// waitOrAbandon): the shape Debian's sshd produces, where a cancelled
// command's session is never closed because its output never ends. The
// in-process server answers a client's close by itself, as x/crypto/ssh
// does, so a proxy in front of it stops passing the server's side on: from
// the client's end that is a server which never answers.
package remoteexec

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stallingProxy forwards one TCP connection to addr and returns its port and
// a freeze function. After freeze, nothing more from the server reaches the
// client (it is read and dropped), while the client's bytes still reach the
// server.
func stallingProxy(t *testing.T, addr string) (int, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var frozen atomic.Bool
	var wg sync.WaitGroup
	var mu sync.Mutex
	var open []net.Conn
	// track remembers a connection so the cleanup closes it.
	track := func(c net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		open = append(open, c)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, c := range open {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := listener.Accept()
		if err != nil {
			return
		}
		track(client)
		server, err := net.Dial("tcp", addr)
		if err != nil {
			_ = client.Close()
			return
		}
		track(server)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(server, client)
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := server.Read(buf)
			// Once frozen, the server's bytes are dropped, its answer to the
			// client's close among them.
			if n > 0 && !frozen.Load() {
				if _, werr := client.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, func() { frozen.Store(true) }
}

// TestRun_ACommandWhoseServerNeverAnswersIsAbandoned: a cancelled command on
// a server that never answers returns its context's error within the
// abandon grace, and its output, which the stream copiers may still be
// writing, is never read (under -race, reading it was a data race).
func TestRun_ACommandWhoseServerNeverAnswersIsAbandoned(t *testing.T) {
	srv := startPoolServer(t)
	port, freeze := stallingProxy(t, srv.Addr())
	r := New(Options{InsecureSkipHostKeyVerify: true})
	conn, err := r.Connect(context.Background(), nil, Target{Host: "127.0.0.1", Port: port}, PasswordAuth(srv.Username, srv.Password))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(ctx, "echo started; sleep 5")
		done <- err
	}()
	// Long enough for "started" to reach the client's output buffer, which is
	// the write a read of the buffers would race.
	time.Sleep(500 * time.Millisecond)
	freeze()
	began := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want context.Canceled", err)
		}
		if took := time.Since(began); took > abandonGrace+3*time.Second {
			t.Fatalf("Run took %s after its cancel; want about the abandon grace (%s)", took, abandonGrace)
		}
	case <-time.After(abandonGrace + 10*time.Second):
		t.Fatal("Run never returned: the abandoned command was waited on")
	}
}
