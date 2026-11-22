// Package remoteexectest: one address with two SSH servers behind it, for the
// tests of what a client records when more than one server answers for one
// device (a load balancer, a failover pair, a machine in the middle).
//
// The first connection reaches a server holding an ed25519 host key of its
// own. It completes the key exchange, so the client checks and accepts its
// key, and then drops the connection when the client sends its password, as a
// server that dies during a login does. A dial that retries a dropped
// connection then reaches the real server behind the same address, and its
// key is checked too. Nothing in it is a mock: both are real SSH servers and
// the client's handshake with each is the library's own.
package remoteexectest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	cryptossh "golang.org/x/crypto/ssh"
)

// TwoServers is a listener standing for one device's address, with a second
// server in front of the real one for the first connection. Close it when done.
type TwoServers struct {
	// Host and Port are where it listens, always on loopback.
	Host string
	Port int
	// FirstKey is the host key of the server the first connection reaches.
	FirstKey cryptossh.PublicKey

	listener net.Listener
	serving  sync.WaitGroup
	closed   sync.Once
	accepted atomic.Int64

	mu   sync.Mutex
	live map[net.Conn]struct{}
}

// StartTwoServers starts the listener, with behind answering every connection
// but the first.
func StartTwoServers(behind *Server) (*TwoServers, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: generating the first server's host key: %w", err)
	}
	signer, err := cryptossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: building the first server's host key signer: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("remoteexectest: listening: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("remoteexectest: listener address %T is not TCP", listener.Addr())
	}
	ts := &TwoServers{Host: "127.0.0.1", Port: tcpAddr.Port, FirstKey: signer.PublicKey(), listener: listener, live: map[net.Conn]struct{}{}}
	ts.serving.Add(1)
	go func() {
		defer ts.serving.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			first := ts.accepted.Add(1) == 1
			ts.track(conn)
			ts.serving.Add(1)
			go func() {
				defer ts.serving.Done()
				defer ts.forget(conn)
				if first {
					dropAtLogin(conn, signer)
					return
				}
				forwardTo(conn, behind.Addr(), ts)
			}()
		}
	}()
	return ts, nil
}

// Addr returns the "host:port" a known_hosts entry and a dialer both need.
func (ts *TwoServers) Addr() string {
	return net.JoinHostPort(ts.Host, fmt.Sprintf("%d", ts.Port))
}

// Close stops the listener, ends every connection it is carrying, and waits
// for them. It is safe to call more than once.
func (ts *TwoServers) Close() {
	ts.closed.Do(func() {
		_ = ts.listener.Close()
		ts.mu.Lock()
		for c := range ts.live {
			_ = c.Close()
		}
		ts.mu.Unlock()
		ts.serving.Wait()
	})
}

// track registers c as live, so Close can end it.
func (ts *TwoServers) track(c net.Conn) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.live[c] = struct{}{}
}

// forget closes c and stops tracking it.
func (ts *TwoServers) forget(c net.Conn) {
	_ = c.Close()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.live, c)
}

// dropAtLogin completes the key exchange on conn with signer's key, then
// closes the connection when the client sends a password.
func dropAtLogin(conn net.Conn, signer cryptossh.Signer) {
	config := &cryptossh.ServerConfig{
		PasswordCallback: func(cryptossh.ConnMetadata, []byte) (*cryptossh.Permissions, error) {
			_ = conn.Close()
			return nil, errors.New("dropped during the login")
		},
	}
	config.AddHostKey(signer)
	// The handshake fails by design once the connection is closed.
	_, _, _, _ = cryptossh.NewServerConn(conn, config)
}

// forwardTo copies bytes both ways between conn and a new connection to addr
// until either side ends.
func forwardTo(conn net.Conn, addr string, ts *TwoServers) {
	upstream, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	ts.track(upstream)
	defer ts.forget(upstream)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	<-done
	// Either side ending ends both, so the other copy returns too.
	_ = conn.Close()
	_ = upstream.Close()
	<-done
}
