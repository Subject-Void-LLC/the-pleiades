package ssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// This file covers Ping's success path against a real SSH server.
//
// The server is a genuine one, built on golang.org/x/crypto/ssh and listening
// on a loopback socket: a real key exchange, a real authentication round, a
// real session channel and a real exec request. Nothing about the transport
// is mocked, which is the whole point under RULE 0 -- Ping's subject is that
// it can actually reach a device, and a fake that returned canned bytes would
// prove only that the fake works.
//
// It is in-process rather than a container because this package is forbidden
// from importing internal/transport/ssh (see ping.go's own doc comment) and
// because the protocol exchange, not the packaging, is what needs exercising.
// internal/transport/ssh already proves the real sshd case against Docker.

// testServer is a running in-process SSH server.
type testServer struct {
	host    string
	port    int
	hostKey cryptossh.PublicKey
}

// addr is the host:port a client dials, and the key a known_hosts line is
// written against.
func (s testServer) addr() string { return net.JoinHostPort(s.host, fmt.Sprint(s.port)) }

// newTestSSHServer starts an SSH server that accepts the given username and
// password, or any public key, and implements just enough of a shell to
// answer the one command Ping sends.
//
// exitStatus is what every exec request reports, so a test can drive the
// remote-command-failed branch without needing a second server.
func newTestSSHServer(t *testing.T, username, password string, exitStatus uint32) testServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a host key: %v", err)
	}
	signer, err := cryptossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("building a host key signer: %v", err)
	}

	config := &cryptossh.ServerConfig{
		PasswordCallback: func(c cryptossh.ConnMetadata, pass []byte) (*cryptossh.Permissions, error) {
			if c.User() != username || string(pass) != password {
				return nil, fmt.Errorf("denied")
			}
			return &cryptossh.Permissions{}, nil
		},
		// Any key is accepted. Which key authenticated is not this file's
		// subject; that the key path completes a real handshake is.
		PublicKeyCallback: func(cryptossh.ConnMetadata, cryptossh.PublicKey) (*cryptossh.Permissions, error) {
			return &cryptossh.Permissions{}, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go serveLoop(listener, config, exitStatus)

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", listener.Addr())
	}
	return testServer{host: "127.0.0.1", port: tcpAddr.Port, hostKey: signer.PublicKey()}
}

// serveLoop accepts connections until the listener closes. Errors are
// dropped rather than reported: the listener closing at test cleanup is the
// ordinary way this ends, and a t.Error from a background goroutine after the
// test returns would panic.
func serveLoop(listener net.Listener, config *cryptossh.ServerConfig, exitStatus uint32) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handleConn(conn, config, exitStatus)
	}
}

// handleConn completes the handshake and serves session channels.
func handleConn(conn net.Conn, config *cryptossh.ServerConfig, exitStatus uint32) {
	defer func() { _ = conn.Close() }()

	serverConn, chans, reqs, err := cryptossh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer func() { _ = serverConn.Close() }()
	go cryptossh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(cryptossh.UnknownChannelType, "only sessions")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}
		go serveSession(channel, requests, exitStatus)
	}
}

// serveSession answers exec requests with a minimal echo implementation.
//
// Implementing echo rather than returning a canned string is deliberate: it
// makes this test prove that shellQuote produces something a real remote
// shell parses back into the original bytes. A canned reply would pass
// whatever quoting was sent.
func serveSession(channel cryptossh.Channel, requests <-chan *cryptossh.Request, exitStatus uint32) {
	defer func() { _ = channel.Close() }()

	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		if req.WantReply {
			_ = req.Reply(true, nil)
		}

		command := decodeExecPayload(req.Payload)
		if arg, ok := strings.CutPrefix(command, "echo "); ok {
			_, _ = channel.Write([]byte(unquotePOSIX(arg) + "\n"))
		}
		_, _ = channel.SendRequest("exit-status", false,
			cryptossh.Marshal(struct{ Status uint32 }{exitStatus}))
		return
	}
}

// decodeExecPayload reads RFC 4254's exec request payload: a 32-bit
// big-endian length followed by the command string.
func decodeExecPayload(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}

// unquotePOSIX reverses shellQuote: it strips the surrounding single quotes
// and collapses the '\” escape sequence back to a bare quote, which is what
// a POSIX shell does with the same input.
func unquotePOSIX(s string) string {
	if len(s) < 2 || !strings.HasPrefix(s, "'") || !strings.HasSuffix(s, "'") {
		return s
	}
	return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'")
}

// pingContext builds the RunbookContext Ping reads secrets and writes stats
// through.
func pingContext(secrets map[string]string) *stubContext {
	return &stubContext{secrets: secrets, stats: map[string]any{}}
}

// TestPing_SucceedsAgainstARealSSHServer is the happy path: a real
// handshake, a real password authentication, a real session, and the echoed
// value coming back as a stat.
func TestPing_SucceedsAgainstARealSSHServer(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)
	device := &sshStub{host: server.host, port: server.port}
	rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})

	result, err := ssh.Ping(context.Background(), rc, device, map[string]any{
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	// A connectivity check must never report changed: it does not alter
	// device state, and a runbook counting changes would be wrong.
	if result.Changed {
		t.Error("a reachability check reported changed")
	}
	if got := rc.stats["reply"]; got != "pong" {
		t.Errorf("reply stat = %v, want %q", got, "pong")
	}
}

// TestPing_EchoesTheSuppliedData proves params.data reaches the remote
// command, and that shellQuote survives a real shell parse. The payloads are
// the ones that break naive quoting.
func TestPing_EchoesTheSuppliedData(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)

	for _, data := range []string{
		"custom-value",
		"it's quoted",
		"pong; rm -rf /",
		"a b\tc",
		`$(whoami)`,
	} {
		t.Run(data, func(t *testing.T) {
			device := &sshStub{host: server.host, port: server.port}
			rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})

			if _, err := ssh.Ping(context.Background(), rc, device, map[string]any{
				"data":                          data,
				"insecure_skip_host_key_verify": true,
			}); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			if got := rc.stats["reply"]; got != data {
				t.Errorf("reply = %v, want %q: the quoting did not survive a real shell", got, data)
			}
		})
	}
}

// TestPing_AuthenticatesWithAPrivateKey drives buildAuthMethod's key branch
// all the way through a real handshake, which a unit test of the branch alone
// cannot do: it proves the signer it builds is one a server accepts.
func TestPing_AuthenticatesWithAPrivateKey(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)
	device := &sshStub{host: server.host, port: server.port}
	rc := pingContext(map[string]string{
		"username":        "admin",
		"private_key_pem": newTestPrivateKeyPEM(t),
	})

	if _, err := ssh.Ping(context.Background(), rc, device, map[string]any{
		"insecure_skip_host_key_verify": true,
	}); err != nil {
		t.Fatalf("Ping with a private key: %v", err)
	}
}

// TestPing_VerifiesTheHostKeyAgainstKnownHosts is the security assertion.
// Host key verification is what stops a machine in the middle answering for
// the device, so it has to be proven against a real server key rather than
// only in the callback's own unit test.
func TestPing_VerifiesTheHostKeyAgainstKnownHosts(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)

	t.Run("the real host key is accepted", func(t *testing.T) {
		withKnownHosts(t, knownhosts.Line([]string{server.addr()}, server.hostKey))

		device := &sshStub{host: server.host, port: server.port}
		rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})
		if _, err := ssh.Ping(context.Background(), rc, device, map[string]any{}); err != nil {
			t.Fatalf("Ping with a matching known_hosts entry: %v", err)
		}
	})

	t.Run("a different host key is refused", func(t *testing.T) {
		// A known_hosts entry naming the right address but somebody else's
		// key: exactly what an interposed server looks like.
		other := newTestSSHServer(t, "admin", "hunter2", 0)
		withKnownHosts(t, knownhosts.Line([]string{server.addr()}, other.hostKey))

		device := &sshStub{host: server.host, port: server.port}
		rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})
		_, err := ssh.Ping(context.Background(), rc, device, map[string]any{})
		if err == nil {
			t.Fatal("a mismatched host key was accepted, so the connection is not MITM-resistant")
		}
		if !strings.Contains(err.Error(), "handshake") {
			t.Errorf("err = %v, want the failure reported at the handshake", err)
		}
	})
}

// TestPing_HandshakeFailureIsReported covers the branch where the transport
// connects but authentication is refused, which is a different failure from
// a dial error and must not be reported as one.
func TestPing_HandshakeFailureIsReported(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)
	device := &sshStub{host: server.host, port: server.port}
	rc := pingContext(map[string]string{"username": "admin", "password": "wrong"})

	_, err := ssh.Ping(context.Background(), rc, device, map[string]any{
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a bad password was accepted")
	}
	if !strings.Contains(err.Error(), "handshake") {
		t.Errorf("err = %v, want it to name the handshake", err)
	}
}

// TestPing_RemoteCommandFailureIsReported covers the last error branch: the
// session opens and the command runs, but exits non-zero.
func TestPing_RemoteCommandFailureIsReported(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 1)
	device := &sshStub{host: server.host, port: server.port}
	rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})

	_, err := ssh.Ping(context.Background(), rc, device, map[string]any{
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a non-zero remote exit status was reported as success")
	}
	if !strings.Contains(err.Error(), "run command") {
		t.Errorf("err = %v, want it to name the remote command", err)
	}
}

// TestPing_StatFailureIsReported covers the final branch: everything worked
// and recording the answer did not. Reported rather than swallowed, because
// a runbook that registered this method's result would otherwise read an
// absent stat as an absent reply.
func TestPing_StatFailureIsReported(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)
	device := &sshStub{host: server.host, port: server.port}
	rc := pingContext(map[string]string{"username": "admin", "password": "hunter2"})
	rc.statErr = fmt.Errorf("deliberate stat failure")

	_, err := ssh.Ping(context.Background(), rc, device, map[string]any{
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the reply was swallowed")
	}
}

// withKnownHosts points HOME at a temporary directory holding a known_hosts
// file with the given line, so hostKeyCallback reads a real file at a real
// path rather than a fixture handed to it directly.
func withKnownHosts(t *testing.T, line string) {
	t.Helper()
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", sshDir, err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
	t.Setenv("HOME", home)
}

// newTestPrivateKeyPEM returns a fresh OpenSSH-format private key.
func newTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a client key: %v", err)
	}
	block, err := cryptossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshalling a client key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}
