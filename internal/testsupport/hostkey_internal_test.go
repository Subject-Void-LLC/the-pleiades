// Tests that capturing a host key is bounded against a server that
// accepts a connection and never speaks, and retries past one.
package testsupport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSSHServer listens on a loopback port. The first silent connections
// it accepts are held open and never answered, as a container port proxy
// does before the server behind it listens; every later one completes an
// SSH handshake for user and password with hostKey.
func fakeSSHServer(t *testing.T, silent int, user, password string) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == user && string(p) == password {
			return nil, nil
		}
		return nil, ssh.ErrNoAuth
	}}
	config.AddHostKey(signer)

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
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
			if n < silent {
				continue
			}
			go func() {
				if sc, chans, reqs, err := ssh.NewServerConn(conn, config); err == nil {
					go ssh.DiscardRequests(reqs)
					for ch := range chans {
						_ = ch.Reject(ssh.Prohibited, "no channels here")
					}
					_ = sc.Close()
				}
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

// TestCaptureHostKey_ASilentServerIsBounded: a server that accepts and
// never answers is given up on at the overall bound, not waited on
// forever.
func TestCaptureHostKey_ASilentServerIsBounded(t *testing.T) {
	addr, _ := fakeSSHServer(t, 1<<30, "u", "p")
	started := time.Now()
	_, err := captureHostKey(addr, "u", "p", time.Second, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "within 1s") {
		t.Fatalf("err = %v, want the overall bound named", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("gave up after %v, want about a second", elapsed)
	}
}

// TestCaptureHostKey_RetriesPastASilentConnection: the first connection is
// held silent, and the next attempt reaches the server and returns the
// key it presented.
func TestCaptureHostKey_RetriesPastASilentConnection(t *testing.T) {
	addr, want := fakeSSHServer(t, 1, "u", "p")
	got, err := captureHostKey(addr, "u", "p", 10*time.Second, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Marshal(), want.Marshal()) {
		t.Error("captured a key other than the one the server presented")
	}
}

// TestCaptureHostKey_ARefusedPasswordIsNotAKey: a server that refuses the
// credential never yields a key, since the capture also proves the
// credential the test will use.
func TestCaptureHostKey_ARefusedPasswordIsNotAKey(t *testing.T) {
	addr, _ := fakeSSHServer(t, 0, "u", "p")
	if _, err := captureHostKey(addr, "u", "wrong", time.Second, 300*time.Millisecond); err == nil {
		t.Fatal("a refused password produced a host key")
	}
}
