// Tests that the handshake with a target reached through a bastion is
// bounded when the target accepts the tunneled connection and never
// speaks.
package remoteexec

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// silentListener accepts connections and never writes to them, as a
// tarpit, a half-open middlebox or a port proxy with nothing behind it
// does.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, conn)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range held {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// TestConnect_HopChain_ASilentTargetIsBounded: a target that accepts the
// connection the bastion forwards and never sends an SSH version is given
// up on when the caller's context ends, rather than holding the run.
func TestConnect_HopChain_ASilentTargetIsBounded(t *testing.T) {
	bastionAddr, bastionKey := startFakeSSHListener(t, func(string) (string, string, int) { return "", "", 1 })
	targetAddr := silentListener(t)
	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey})
	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})
	bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
	targetHost, targetPort := splitHostPortT(t, targetAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := r.Run(ctx, []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}},
		Target{Host: targetHost, Port: targetPort}, testAuth, "true")
	if err == nil {
		t.Fatal("a silent target produced a result")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("gave up after %v, want about the context's two seconds", elapsed)
	}
}
