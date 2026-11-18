// Package remoteexec holds the tests that Connect holds each hop of a bastion
// chain to the legacy SSH flags its own record gave it. The route built from
// device records is tested in internal/engine.
package remoteexec

import (
	"context"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// startLegacyHopServer starts a loopback SSH server offering only ciphers and
// key exchanges (nil meaning the library's own default for that kind), on the
// same fake handler every hop test uses, so it can serve as a bastion (direct-tcpip)
// or as a target (a command). It returns its address and host key.
func startLegacyHopServer(t *testing.T, ciphers, kex []string) (string, ssh.PublicKey) {
	t.Helper()
	signer := generateTestHostKey(t)
	config := &ssh.ServerConfig{
		Config: ssh.Config{Ciphers: ciphers, KeyExchanges: kex},
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
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
			go serveOneFakeConnection(conn, config, func(cmd string) (string, string, int) {
				return "reached:" + cmd, "", 0
			})
		}
	}()
	return listener.Addr().String(), signer.PublicKey()
}

// TestConnect_HopLegacy_EachHopIsHeldToItsOwnFlags proves a bastion's legacy
// flags and the target's are each used for their own leg and for no other:
// a bastion that allows CBC ciphers reaches a target that refuses them (the
// bastion warned, the target named), and a bastion that refuses SHA-1 key
// exchange is refused even when the target would accept it. Each refusal is
// paired with the same chain where the flag sits on the leg that needs it, so
// the refusal is shown to come from the flag and not from the fixture.
func TestConnect_HopLegacy_EachHopIsHeldToItsOwnFlags(t *testing.T) {
	cbc := []string{"aes128-cbc"}
	dh14 := []string{ssh.InsecureKeyExchangeDH14SHA1}

	t.Run("bastion allows ciphers, target refuses them", func(t *testing.T) {
		bastionAddr, bastionKey := startLegacyHopServer(t, cbc, nil)
		targetAddr, targetKey := startLegacyHopServer(t, cbc, nil)
		r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey, targetAddr: targetKey}), MaxRetries: 1})
		bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
		targetHost, targetPort := splitHostPortT(t, targetAddr)
		hop := func(legacy Legacy) []Hop {
			return []Hop{{Target: Target{Host: bastionHost, Port: bastionPort, Legacy: legacy}, Auth: testAuth}}
		}

		// The bastion's flag carries it through; the target, allowing nothing,
		// refuses the CBC offer and the refusal names its own property.
		_, err := r.Connect(context.Background(), hop(Legacy{Ciphers: true}), Target{Host: targetHost, Port: targetPort}, testAuth)
		if err == nil || !strings.Contains(err.Error(), targetAddr) || !strings.Contains(err.Error(), LegacyCiphersProperty) {
			t.Fatalf("a target allowing no ciphers was not refused naming itself and %s: %v", LegacyCiphersProperty, err)
		}

		// The same chain with the target's flag set reaches it, and the
		// bastion's weakening is warned about on the connection.
		conn, err := r.Connect(context.Background(), hop(Legacy{Ciphers: true}), Target{Host: targetHost, Port: targetPort, Legacy: Legacy{Ciphers: true}}, testAuth)
		if err != nil {
			t.Fatalf("the chain with both flags set failed: %v", err)
		}
		defer conn.Close()
		if w := strings.Join(conn.LegacyWarnings("target"), "\n"); !strings.Contains(w, "bastion "+bastionAddr) {
			t.Errorf("the bastion's CBC cipher was not warned about: %q", w)
		}

		// A bastion whose flag is dropped is refused, naming itself: the
		// swap or drop a regression would make cannot pass silently.
		_, err = r.Connect(context.Background(), hop(Legacy{}), Target{Host: targetHost, Port: targetPort, Legacy: Legacy{Ciphers: true}}, testAuth)
		if err == nil || !strings.Contains(err.Error(), bastionAddr) {
			t.Fatalf("a bastion with its flag dropped was not refused naming itself: %v", err)
		}
	})

	t.Run("bastion refuses SHA-1 key exchange, target allows it", func(t *testing.T) {
		bastionAddr, bastionKey := startLegacyHopServer(t, nil, dh14)
		targetAddr, targetKey := startLegacyHopServer(t, nil, dh14)
		r := New(Options{KnownHostsPath: writeMultiKnownHosts(t, map[string]ssh.PublicKey{bastionAddr: bastionKey, targetAddr: targetKey}), MaxRetries: 1})
		bastionHost, bastionPort := splitHostPortT(t, bastionAddr)
		targetHost, targetPort := splitHostPortT(t, targetAddr)
		target := Target{Host: targetHost, Port: targetPort, Legacy: Legacy{KeyExchange: true}}

		// The target alone would take SHA-1 key exchange, so the refusal below
		// is the bastion's own, not a fault of the target's flag.
		if conn, err := r.Connect(context.Background(), nil, target, testAuth); err != nil {
			t.Fatalf("the target alone, with its flag, should connect: %v", err)
		} else {
			conn.Close()
		}

		// The bastion allows nothing, so its weak key exchange offer is
		// refused even though the target permits it, naming the bastion and
		// the bastion's property.
		hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
		_, err := r.Connect(context.Background(), hops, target, testAuth)
		if err == nil || !strings.Contains(err.Error(), bastionAddr) || !strings.Contains(err.Error(), LegacyKeyExchangeProperty) {
			t.Fatalf("a bastion allowing no key exchange was not refused naming itself and %s: %v", LegacyKeyExchangeProperty, err)
		}

		// With the bastion's flag set the same chain connects.
		hops[0].Target.Legacy = Legacy{KeyExchange: true}
		conn, err := r.Connect(context.Background(), hops, target, testAuth)
		if err != nil {
			t.Fatalf("the chain with the bastion's flag set failed: %v", err)
		}
		conn.Close()
	})
}
