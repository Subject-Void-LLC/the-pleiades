package netcli_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"golang.org/x/crypto/ssh"
)

// This file's own fake SSH+PTY server is a deliberate, small duplicate
// of pkg/remoteexec/shell_test.go's newFakePTYSSHServer, not an import
// of it: that helper is unexported, and pkg/remoteexec/remoteexectest
// (the one fixture package that IS shared across package boundaries)
// only exists because a second real consumer needed its exec-based,
// real-/bin/sh server -- see that package's own doc comment. Nothing
// outside pkg/remoteexec needs a PTY-based fake server yet, so this
// stays local until a second one does, the same precedent that fixture
// package itself sets.
//
// It also can't reuse pkg/remoteexec's own dialFunc-injection trick
// (newTestRunner in that package's tests): Runner.dial is unexported and
// remoteexec.New always wires it to the real dialer, so a Collection
// method's own test (and this package's) has no way to intercept a
// dial from outside pkg/remoteexec. This server therefore listens on a
// REAL loopback TCP port and is reached through the real, public
// Runner.Connect, exactly as a real device would be -- a slightly
// heavier fixture than an injected dialFunc, but not a mock: the SSH
// handshake, the pty-req and the shell request are all genuine.

// generateTestHostKey returns a real ed25519 ssh.Signer, the same shape
// a real sshd's host key takes.
func generateTestHostKey(t testing.TB) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to build ssh.Signer: %v", err)
	}
	return signer
}

// newFakeIOSServer starts an in-process, real-loopback-TCP SSH server
// whose session channel answers "pty-req" and "shell", then hands the
// resulting channel to sessionFunc once per connection, and returns the
// remoteexec.Target to dial it with. It accepts any username/password.
func newFakeIOSServer(t *testing.T, sessionFunc func(ssh.Channel)) remoteexec.Target {
	t.Helper()

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(generateTestHostKey(t))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed by t.Cleanup
			}
			go serveOneFakeIOSConnection(conn, config, sessionFunc)
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return remoteexec.Target{Host: "127.0.0.1", Port: addr.Port}
}

func serveOneFakeIOSConnection(conn net.Conn, config *ssh.ServerConfig, sessionFunc func(ssh.Channel)) {
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go handleFakeIOSSession(channel, requests, sessionFunc)
	}
}

func handleFakeIOSSession(channel ssh.Channel, requests <-chan *ssh.Request, sessionFunc func(ssh.Channel)) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "pty-req":
			if req.WantReply {
				req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				req.Reply(true, nil)
			}
			sessionFunc(channel)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// dialFakeIOSServer opens a real Conn against target's fake server,
// through the same public Connect path a Collection method uses.
func dialFakeIOSServer(t *testing.T, target remoteexec.Target) *remoteexec.Conn {
	t.Helper()
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(context.Background(), nil, target, remoteexec.PasswordAuth("u", "p"))
	if err != nil {
		t.Fatalf("dialing the fake IOS server at %s: %v", target.Addr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
