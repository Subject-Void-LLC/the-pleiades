package remoteexec

import (
	"context"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// startMaliciousRedirectingBastion starts a real, otherwise-normal fake
// SSH server whose direct-tcpip handling ignores whatever destination a
// client actually asks for and silently forwards every such channel to
// redirectTo instead - standing in for a compromised bastion trying to
// have the client's own next hop land on an attacker-controlled
// endpoint. Session ("exec") channels are refused outright: this
// fixture's only job is to misbehave at the forwarding layer, the exact
// attack surface under test.
func startMaliciousRedirectingBastion(t *testing.T, redirectTo string) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

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
			go func() {
				sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChannel := range chans {
					if newChannel.ChannelType() != "direct-tcpip" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "this fixture only misbehaves at direct-tcpip")
						continue
					}
					// The payload (the client's REQUESTED destination) is
					// deliberately never even parsed: this bastion is
					// malicious BY CONSTRUCTION, redirecting every channel
					// to redirectTo regardless of what was actually asked
					// for, not merely failing to honor a parsed request.
					redirected, dialErr := net.Dial("tcp", redirectTo)
					if dialErr != nil {
						_ = newChannel.Reject(ssh.ConnectionFailed, dialErr.Error())
						continue
					}
					channel, requests, err := newChannel.Accept()
					if err != nil {
						_ = redirected.Close()
						continue
					}
					go ssh.DiscardRequests(requests)
					go forwardDirectTCPIP(channel, redirected)
				}
			}()
		}
	}()

	return listener.Addr().String(), hostSigner.PublicKey()
}

// TestConnect_HostileBastionRedirectIsRefusedByTheNextHopsOwnKeyCheck is
// Phase 73's own adversarial proof of the hostile-bastion redirect Phase
// 72 explicitly deferred: a compromised bastion reconfigured so its own
// direct-tcpip channel silently lands on an attacker-controlled endpoint
// instead of the real target the client asked for. The next hop's own
// SSH handshake (Connect treats the final leg as a real, independent SSH
// connection, exactly like ssh_exec's own production path) presents the
// ATTACKER's key where the REAL target's is recorded in known_hosts, so
// verification fails closed - naming the target's own address, the hop
// the client believed it was reaching, not the bastion's.
//
// This test is deliberately paired with
// TestConnect_HostileBastionRedirect_SameChainSucceedsAgainstAWellBehavedBastion
// below, run against the identical known_hosts and the identical target
// address: the pairing is what makes this falsifiable in both
// directions, proving the failure here is really about the redirect and
// not a broken known_hosts entry or a coincidental dial failure.
func TestConnect_HostileBastionRedirectIsRefusedByTheNextHopsOwnKeyCheck(t *testing.T) {
	realTargetAddr, realTargetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "REAL-TARGET-REACHED:" + cmd, "", 0
	})
	attackerAddr, _ := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "ATTACKER-IMPERSONATION-SUCCEEDED", "", 0
	})
	maliciousBastionAddr, maliciousBastionKey := startMaliciousRedirectingBastion(t, attackerAddr)

	// known_hosts trusts the bastion itself (a real device, legitimately
	// reached) and the REAL target's key under the REAL target's own
	// address - never the attacker's key under any address, since the
	// operator has no reason to know the attacker exists.
	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		maliciousBastionAddr: maliciousBastionKey,
		realTargetAddr:       realTargetKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})
	bastionHost, bastionPort := splitHostPortT(t, maliciousBastionAddr)
	targetHost, targetPort := splitHostPortT(t, realTargetAddr)

	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	target := Target{Host: targetHost, Port: targetPort}

	_, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err == nil {
		t.Fatal("expected the redirected connection to fail closed on the next hop's own key mismatch")
	}
	if !strings.Contains(err.Error(), realTargetAddr) {
		t.Errorf("err = %v, want it to name the REAL target's own address %q (the hop the client believed it was reaching), not the bastion's or the attacker's", err, realTargetAddr)
	}
	t.Logf("hostile bastion redirect refused as expected: %v", err)
}

// TestConnect_HostileBastionRedirect_SameChainSucceedsAgainstAWellBehavedBastion
// is the falsifiable-both-directions half of the pair above: the
// IDENTICAL known_hosts, the IDENTICAL real target address, through a
// WELL-BEHAVED bastion (startFakeSSHListener, which forwards to whatever
// is actually requested) instead of the malicious one. This must
// succeed, proving the prior test's failure was genuinely about the
// redirect and not some other broken precondition shared by both.
func TestConnect_HostileBastionRedirect_SameChainSucceedsAgainstAWellBehavedBastion(t *testing.T) {
	realTargetAddr, realTargetKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "REAL-TARGET-REACHED:" + cmd, "", 0
	})
	wellBehavedBastionAddr, wellBehavedBastionKey := startFakeSSHListener(t, func(cmd string) (string, string, int) {
		return "bastion should never run a command directly", "", 1
	})

	knownHostsPath := writeMultiKnownHosts(t, map[string]ssh.PublicKey{
		wellBehavedBastionAddr: wellBehavedBastionKey,
		realTargetAddr:         realTargetKey,
	})

	r := New(Options{KnownHostsPath: knownHostsPath, MaxRetries: 1})
	bastionHost, bastionPort := splitHostPortT(t, wellBehavedBastionAddr)
	targetHost, targetPort := splitHostPortT(t, realTargetAddr)

	hops := []Hop{{Target: Target{Host: bastionHost, Port: bastionPort}, Auth: testAuth}}
	target := Target{Host: targetHost, Port: targetPort}

	result, err := r.Run(context.Background(), hops, target, testAuth, "echo hi")
	if err != nil {
		t.Fatalf("expected the SAME chain shape to succeed through a well-behaved bastion, got: %v", err)
	}
	if !strings.Contains(result.Stdout, "REAL-TARGET-REACHED:echo hi") {
		t.Errorf("stdout = %q, want the real target's own handler output", result.Stdout)
	}
}
