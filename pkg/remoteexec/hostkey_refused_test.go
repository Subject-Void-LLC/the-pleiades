// A host key this side refuses, against a real SSH server on loopback:
// refused once per call, and never counted against the shared circuit.
package remoteexec

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// startHandshakeCountingSSHListener starts a real SSH server on loopback
// that accepts rightPassword, and counts every connection it accepts: a
// refused host key ends the handshake before any password is sent, so the
// connection is what a retry would show up as.
func startHandshakeCountingSSHListener(t *testing.T) (target Target, hostKey ssh.PublicKey, accepted *int32) {
	t.Helper()
	accepted = new(int32)
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == rightPassword {
				return nil, nil
			}
			return nil, errors.New("password refused")
		},
	}
	signer := generateTestHostKey(t)
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(accepted, 1)
			go serveOneFakeConnection(conn, config, func(string) (string, string, int) { return "", "", 0 })
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return Target{Host: addr.IP.String(), Port: addr.Port}, signer.PublicKey(), accepted
}

// writeKnownHostsFor writes a known_hosts file naming target with key, or an
// empty one when key is nil.
func writeKnownHostsFor(t *testing.T, path string, target Target, key ssh.PublicKey) {
	t.Helper()
	var body []byte
	if key != nil {
		body = []byte(knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))}, key) + "\n")
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestConnect_ARefusedHostKeyIsNotRetried proves an unknown host key and
// a changed one are each refused after one handshake: the key will not be
// different on a second attempt, and a retry only delays the refusal.
func TestConnect_ARefusedHostKeyIsNotRetried(t *testing.T) {
	target, _, accepted := startHandshakeCountingSSHListener(t)
	for name, key := range map[string]ssh.PublicKey{"unknown": nil, "changed": generateTestHostKey(t).PublicKey()} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			writeKnownHostsFor(t, path, target, key)
			before := atomic.LoadInt32(accepted)
			_, err := New(Options{KnownHostsPath: path}).Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) {
				t.Fatalf("error = %v, want a host key refusal", err)
			}
			if got := atomic.LoadInt32(accepted) - before; got != 1 {
				t.Errorf("one call made %d handshakes with a refused host key, want 1", got)
			}
		})
	}
}

// TestConnect_ARefusedHostKeyDoesNotOpenTheCircuit proves refusals of a
// host key never cost another caller its turn: after more refusals than
// the breaker's threshold, the same Runner connects as soon as known_hosts
// names the key.
func TestConnect_ARefusedHostKeyDoesNotOpenTheCircuit(t *testing.T) {
	target, key, _ := startHandshakeCountingSSHListener(t)
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHostsFor(t, path, target, nil)
	r := New(Options{KnownHostsPath: path})

	for i := 0; i < 2*breaker.DefaultThreshold; i++ {
		_, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
		if errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("refusal %d: the circuit opened: %v", i, err)
		}
	}

	writeKnownHostsFor(t, path, target, key)
	conn, err := r.Connect(context.Background(), nil, target, PasswordAuth("u", rightPassword))
	if err != nil {
		t.Fatalf("connecting once known_hosts names the key: %v", err)
	}
	_ = conn.Close()
}
