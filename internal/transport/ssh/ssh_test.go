package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// This file tests what this package is, which since pkg/remoteexec took
// over the mechanism is an Adapter and nothing else: it translates a
// transport.Target into a remoteexec.Target, a credential.Credential
// into a remoteexec.Auth, and a remoteexec.Result back into a
// transport.Result, and it must not lose or reshape anything on the way.
//
// Retry, backoff, the circuit breaker, host key verification and the
// dial itself are pkg/remoteexec's tests, not copies of them here.
// ssh_container_test.go is what proves the whole stack still reaches a
// real, independent sshd after that move.
//
// These run against a real loopback SSH server rather than an injected
// dial function, because an Adapter with an injected dial would be
// testing pkg/remoteexec through a wrapper. A loopback socket is local
// and near-instant, so this stays a fast unit test.

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

// writeKnownHosts writes a single known_hosts line for hostPort and key
// into a fresh temp file, returning the file's path.
func writeKnownHosts(t testing.TB, hostPort string, key ssh.PublicKey) string {
	t.Helper()
	line := knownhosts.Line([]string{hostPort}, key)
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts file: %v", err)
	}
	return path
}

// loopbackServer is a real SSH server on a loopback socket, accepting
// any credential and answering one exec request per session with
// handler's result.
type loopbackServer struct {
	target  transport.Target
	hostKey ssh.PublicKey
}

// startLoopbackServer starts a loopbackServer that runs until the test
// ends. It accepts any username and password because which credential
// authenticated is not what this file tests; whether the Adapter
// produced one at all is.
func startLoopbackServer(t *testing.T, handler func(command string) (stdout, stderr string, exitCode int)) loopbackServer {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// The listener was closed at cleanup; this goroutine's job
				// is done.
				return
			}
			go serveLoopbackConnection(conn, config, handler)
		}
	}()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", listener.Addr())
	}
	return loopbackServer{
		target:  transport.Target{Host: "127.0.0.1", Port: tcpAddr.Port},
		hostKey: hostSigner.PublicKey(),
	}
}

// addr is the "host:port" a known_hosts line is written against.
func (s loopbackServer) addr() string {
	return net.JoinHostPort(s.target.Host, strconv.Itoa(s.target.Port))
}

// serveLoopbackConnection completes one server-side handshake and serves
// its session channels.
func serveLoopbackConnection(conn net.Conn, config *ssh.ServerConfig, handler func(string) (string, string, int)) {
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		// A test may have deliberately triggered a handshake failure;
		// there is no reporting channel from this goroutine, so it simply
		// stops serving.
		_ = conn.Close()
		return
	}
	defer func() { _ = sConn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go serveLoopbackSession(channel, requests, handler)
	}
}

// serveLoopbackSession answers exactly one exec request with handler's
// stdout, stderr and exit code.
func serveLoopbackSession(channel ssh.Channel, requests <-chan *ssh.Request, handler func(string) (string, string, int)) {
	defer func() { _ = channel.Close() }()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)

		stdout, stderr, exitCode := handler(payload.Command)
		_, _ = channel.Write([]byte(stdout))
		_, _ = channel.Stderr().Write([]byte(stderr))
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ Status uint32 }{uint32(exitCode)})) // #nosec G115 -- a test's own small, fixed exit codes
		return
	}
}

// TestExec_TranslatesResultFaithfully proves the three fields of a
// remote result reach transport.Result unchanged and unmerged, and that
// a non-zero exit status arrives as an ExitCode with a nil error rather
// than as a Go error.
//
// That last part is transport.Transport's load-bearing contract: a
// caller must be able to tell "the command ran and failed" apart from
// "the command's outcome is unknown," and folding one into the other
// would make every failed command look like a broken connection.
func TestExec_TranslatesResultFaithfully(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
	}{
		{name: "success", stdout: "out-line", stderr: "", exitCode: 0},
		{name: "output on both streams", stdout: "out-line", stderr: "err-line", exitCode: 0},
		{name: "non-zero exit status", stdout: "", stderr: "boom", exitCode: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := startLoopbackServer(t, func(string) (string, string, int) {
				return tc.stdout, tc.stderr, tc.exitCode
			})
			tr := New(Options{KnownHostsPath: writeKnownHosts(t, server.addr(), server.hostKey)})

			result, err := tr.Exec(context.Background(), server.target, testCred(), "anything")
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if result.Stdout != tc.stdout {
				t.Errorf("Stdout = %q, want %q", result.Stdout, tc.stdout)
			}
			if result.Stderr != tc.stderr {
				t.Errorf("Stderr = %q, want %q", result.Stderr, tc.stderr)
			}
			if result.ExitCode != tc.exitCode {
				t.Errorf("ExitCode = %d, want %d", result.ExitCode, tc.exitCode)
			}
		})
	}
}

// TestExec_CommandRunsVerbatim proves the exact command string survives
// the Adapter untouched: no local shell wrapping, and no concatenation
// with the target address.
func TestExec_CommandRunsVerbatim(t *testing.T) {
	const want = `echo "$(whoami)"; rm -rf /tmp/should-not-be-touched`

	received := make(chan string, 1)
	server := startLoopbackServer(t, func(cmd string) (string, string, int) {
		received <- cmd
		return "", "", 0
	})
	tr := New(Options{KnownHostsPath: writeKnownHosts(t, server.addr(), server.hostKey)})

	if _, err := tr.Exec(context.Background(), server.target, testCred(), want); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got := <-received; got != want {
		t.Errorf("the remote side received %q, want %q", got, want)
	}
}

// TestExec_TranslatesEveryCredentialShape proves each field of a
// credential.Credential reaches an authentication method that a real
// server accepts, and that a credential carrying nothing usable is
// refused outright.
//
// Refusal is the important case. A credential store that found nothing
// returns a zero Credential, and an Adapter that turned that into a
// connection attempt would be trying to log in with no authentication at
// all against a device it was told it had no key for.
func TestExec_TranslatesEveryCredentialShape(t *testing.T) {
	plainKey := marshalTestPrivateKey(t, "")
	encryptedKey := marshalTestPrivateKey(t, "correct-horse")

	tests := []struct {
		name    string
		cred    credential.Credential
		wantErr string
	}{
		{name: "password", cred: credential.Credential{Username: "u", Password: "p"}},
		{name: "private key", cred: credential.Credential{Username: "u", PrivateKeyPEM: plainKey}},
		{
			name: "passphrase-encrypted private key",
			cred: credential.Credential{Username: "u", PrivateKeyPEM: encryptedKey, Passphrase: "correct-horse"},
		},
		{
			name:    "no usable material",
			cred:    credential.Credential{Username: "u"},
			wantErr: "no usable authentication method",
		},
		{
			name:    "unparsable private key",
			cred:    credential.Credential{Username: "u", PrivateKeyPEM: []byte("garbage")},
			wantErr: "parse private key",
		},
		{
			name:    "wrong passphrase",
			cred:    credential.Credential{Username: "u", PrivateKeyPEM: encryptedKey, Passphrase: "wrong"},
			wantErr: "parse private key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := startLoopbackServer(t, func(string) (string, string, int) {
				return "authenticated", "", 0
			})
			tr := New(Options{KnownHostsPath: writeKnownHosts(t, server.addr(), server.hostKey)})

			result, err := tr.Exec(context.Background(), server.target, tc.cred, "whoami")
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q, got a successful call", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				// Every refusal happens before any network I/O, so a
				// failure here must never carry a dial or handshake in it.
				if strings.Contains(err.Error(), "dial") {
					t.Errorf("error = %v, want the refusal to happen before any dial", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if result.Stdout != "authenticated" {
				t.Errorf("Stdout = %q, want the server to have accepted this credential", result.Stdout)
			}
		})
	}
}

// TestExec_HostKeyVerificationStillApplies proves the Adapter did not
// lose the fail-closed host key check when the mechanism moved to
// pkg/remoteexec: a known_hosts file naming somebody else's key for this
// address is exactly what an interposed server looks like, and it must
// be refused.
func TestExec_HostKeyVerificationStillApplies(t *testing.T) {
	server := startLoopbackServer(t, func(string) (string, string, int) {
		return "reached", "", 0
	})

	t.Run("the real host key is accepted", func(t *testing.T) {
		tr := New(Options{KnownHostsPath: writeKnownHosts(t, server.addr(), server.hostKey)})
		if _, err := tr.Exec(context.Background(), server.target, testCred(), "echo hi"); err != nil {
			t.Fatalf("Exec with a matching known_hosts entry: %v", err)
		}
	})

	t.Run("a different host key is refused", func(t *testing.T) {
		forged := generateTestHostKey(t)
		tr := New(Options{
			KnownHostsPath: writeKnownHosts(t, server.addr(), forged.PublicKey()),
			MaxRetries:     1, // a forged key is not a transient condition retrying would fix
		})
		if _, err := tr.Exec(context.Background(), server.target, testCred(), "echo hi"); err == nil {
			t.Fatal("a mismatched host key was accepted, so the connection is not MITM-resistant")
		}
	})

	t.Run("a missing known_hosts file fails closed", func(t *testing.T) {
		tr := New(Options{
			KnownHostsPath: filepath.Join(t.TempDir(), "does-not-exist"),
			MaxRetries:     1,
		})
		_, err := tr.Exec(context.Background(), server.target, testCred(), "echo hi")
		if err == nil {
			t.Fatal("a missing known_hosts file was treated as trust on first use")
		}
		if strings.Contains(err.Error(), "dial") {
			t.Errorf("error = %v, want host key verification to fail before any dial", err)
		}
	})
}

// TestExec_ContextCancellationReachesTheCall proves a caller's canceled
// context aborts an Exec promptly rather than waiting out the dial
// budget, which is what lets an interruptible job actually stop.
func TestExec_ContextCancellationReachesTheCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// TEST-NET-1 (RFC 5737) is reserved for documentation and not routed,
	// so a dial here would otherwise sit until the timeout.
	target := transport.Target{Host: "192.0.2.1", Port: 22}
	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 30 * time.Second})

	done := make(chan error, 1)
	go func() {
		_, err := tr.Exec(ctx, target, testCred(), "echo hi")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a failure on an already-canceled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exec did not return promptly on a canceled context")
	}
}

// testCred is the password credential the loopback server accepts.
func testCred() credential.Credential {
	return credential.Credential{Username: "u", Password: "p"}
}

// marshalTestPrivateKey generates a fresh ed25519 key and returns it PEM
// encoded, encrypted with passphrase when passphrase is not empty.
func marshalTestPrivateKey(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	var block *pem.Block
	var err2 error
	if passphrase == "" {
		block, err2 = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err2 = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err2 != nil {
		t.Fatalf("failed to marshal private key: %v", err2)
	}
	return pem.EncodeToMemory(block)
}
