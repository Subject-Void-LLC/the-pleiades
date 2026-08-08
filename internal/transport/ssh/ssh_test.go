package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// credentialFixture builds a credential.Credential carrying keyBytes as
// PrivateKeyPEM and passphrase as Passphrase, with no password set, so
// buildAuthMethod is exercised on its key-authentication branch (or the
// no-usable-auth branch, when keyBytes is empty).
func credentialFixture(keyBytes []byte, passphrase string) credential.Credential {
	return credential.Credential{Username: "u", PrivateKeyPEM: keyBytes, Passphrase: passphrase}
}

// localPipe returns two ends of a real, loopback TCP connection. It is
// deliberately NOT net.Pipe(): net.Pipe is fully synchronous and
// unbuffered, and the SSH handshake writes from both sides without
// waiting for a matching read first, which deadlocks net.Pipe outright
// (this is exactly why golang.org/x/crypto/ssh's own test suite defines
// this same real-loopback-socket helper instead of using net.Pipe; see
// its handshake_test.go's netPipe). A loopback TCP connection is still
// entirely local and near-instant, so this stays a fast unit test, not
// a network test.
func localPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a loopback listener: %v", err)
	}
	defer listener.Close()

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		conn, err := listener.Accept()
		acceptCh <- acceptResult{conn, err}
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial the loopback listener: %v", err)
	}

	result := <-acceptCh
	if result.err != nil {
		t.Fatalf("failed to accept the loopback connection: %v", result.err)
	}
	return clientConn, result.conn
}

// newFakeSSHServer starts an in-process, loopback-TCP-backed SSH server
// for fast, deterministic unit tests that need a genuine SSH handshake
// and a genuine session, without the container test's real, independent
// sshd (ssh_container_test.go covers that side of RULE 0). It accepts
// any username/password: auth acceptance/rejection against a real,
// independent server implementation is covered by the container tests,
// not duplicated here. It runs handler once per "exec" request on the
// resulting session, replying with handler's stdout, stderr, and exit
// code. It returns a dialFunc that establishes exactly one such
// connection per call, and the server's host public key.
func newFakeSSHServer(t *testing.T, handler func(command string) (stdout, stderr string, exitCode int)) (dialFunc, ssh.PublicKey) {
	t.Helper()
	hostSigner := generateTestHostKey(t)

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

	dial := func(ctx context.Context, addr string, clientConfig *ssh.ClientConfig) (*ssh.Client, error) {
		clientConn, serverConn := localPipe(t)
		go serveOneFakeConnection(serverConn, config, handler)
		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, clientConfig)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}
	return dial, hostSigner.PublicKey()
}

// serveOneFakeConnection completes the server side of one SSH handshake
// over conn and dispatches every resulting session channel to
// handleFakeSession.
func serveOneFakeConnection(conn net.Conn, config *ssh.ServerConfig, handler func(string) (string, string, int)) {
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		// The client side may have deliberately triggered a handshake
		// failure (a fuzz/negative-path test, for example); there is no
		// test-visible reporting mechanism from this goroutine, so this
		// simply stops serving.
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
		go handleFakeSession(channel, requests, handler)
	}
}

// handleFakeSession answers exactly one "exec" request on channel with
// handler's result, then closes the channel. Any other request type is
// declined.
func handleFakeSession(channel ssh.Channel, requests <-chan *ssh.Request, handler func(string) (string, string, int)) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}

		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)

		stdout, stderr, exitCode := handler(payload.Command)
		channel.Write([]byte(stdout))
		channel.Stderr().Write([]byte(stderr))
		channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ Status uint32 }{uint32(exitCode)}))
		return
	}
}

// newTestTransport builds an sshTransport wired to dial, with host key
// verification bypassed (the fake server's host key has nothing to do
// with the behavior under test here; known_hosts_test.go and the
// container tests already cover host key verification itself).
func newTestTransport(dial dialFunc, opts Options) *sshTransport {
	opts.InsecureSkipHostKeyVerify = true
	opts = applyDefaults(opts)
	return &sshTransport{
		opts:    opts,
		breaker: newCircuitBreaker(opts.BreakerThreshold, opts.BreakerCooldown),
		dial:    dial,
	}
}

var testTarget = transport.Target{Host: "127.0.0.1", Port: 2222}
var testCred = credential.Credential{Username: "u", Password: "p"}

// TestExec_Success proves a successful Exec call returns the remote
// command's stdout and a zero exit code with no error.
func TestExec_Success(t *testing.T) {
	var execCount int32
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		atomic.AddInt32(&execCount, 1)
		return "hello\n", "", 0
	})
	tr := newTestTransport(dial, Options{})

	result, err := tr.Exec(context.Background(), testTarget, testCred, "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain %q, got %q", "hello", result.Stdout)
	}
	if got := atomic.LoadInt32(&execCount); got != 1 {
		t.Errorf("expected exactly 1 exec on the server side, got %d", got)
	}
}

// TestExec_NonZeroExitCode proves a command that exits non-zero on the
// remote side is reported via Result.ExitCode with a nil error, per
// transport.Transport's documented contract: a non-zero exit code is
// not itself a Go error.
func TestExec_NonZeroExitCode(t *testing.T) {
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "", "boom\n", 7
	})
	tr := newTestTransport(dial, Options{})

	result, err := tr.Exec(context.Background(), testTarget, testCred, "exit 7")
	if err != nil {
		t.Fatalf("expected no error for a non-zero remote exit code, got: %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("expected exit code 7, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "boom") {
		t.Errorf("expected stderr to contain %q, got %q", "boom", result.Stderr)
	}
}

// TestExec_SeparatesStdoutAndStderr proves stdout and stderr are
// captured into separate Result fields, never combined.
func TestExec_SeparatesStdoutAndStderr(t *testing.T) {
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "out-line", "err-line", 0
	})
	tr := newTestTransport(dial, Options{})

	result, err := tr.Exec(context.Background(), testTarget, testCred, "noop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stdout != "out-line" {
		t.Errorf("expected stdout %q, got %q", "out-line", result.Stdout)
	}
	if result.Stderr != "err-line" {
		t.Errorf("expected stderr %q, got %q", "err-line", result.Stderr)
	}
}

// TestExec_CommandRunsVerbatim proves the exact command string reaches
// the remote side unmodified: no local shell wrapping, and no string
// concatenation with the target host or anything else.
func TestExec_CommandRunsVerbatim(t *testing.T) {
	const want = `echo "$(whoami)"; rm -rf /tmp/should-not-be-touched`

	var got string
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		got = cmd
		return "", "", 0
	})
	tr := newTestTransport(dial, Options{})

	if _, err := tr.Exec(context.Background(), testTarget, testCred, want); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected the remote side to receive command verbatim:\nwant: %q\ngot:  %q", want, got)
	}
}

// TestExec_NoAuthMethod proves a credential with neither Password nor
// PrivateKeyPEM set is rejected explicitly, before any dial is
// attempted.
func TestExec_NoAuthMethod(t *testing.T) {
	var dialCalls int32
	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("dial should never be reached")
	}
	tr := newTestTransport(dial, Options{})

	_, err := tr.Exec(context.Background(), testTarget, credential.Credential{Username: "u"}, "echo hi")
	if err == nil {
		t.Fatal("expected an error for a credential with no usable authentication method")
	}
	if !strings.Contains(err.Error(), "no usable authentication method") {
		t.Errorf("expected a clear no-usable-authentication error, got: %v", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial attempt for an unusable credential, got %d", got)
	}
}

// TestExec_BadPrivateKeyPEM proves a credential with unparsable
// PrivateKeyPEM bytes fails with a wrapped parse error, before any dial
// is attempted, rather than silently falling through to no
// authentication.
func TestExec_BadPrivateKeyPEM(t *testing.T) {
	var dialCalls int32
	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("dial should never be reached")
	}
	tr := newTestTransport(dial, Options{})

	cred := credentialFixture([]byte("not a valid private key"), "")
	_, err := tr.Exec(context.Background(), testTarget, cred, "echo hi")
	if err == nil {
		t.Fatal("expected an error for an unparsable private key")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial attempt for an unparsable private key, got %d", got)
	}
}

// TestExec_RetryOnlyWrapsDialPhase is the direct proof this package's
// central safety rule holds: a dial that fails twice and succeeds on the
// third attempt must result in the remote command running EXACTLY ONCE,
// never once per dial attempt. The first two dial attempts fail before
// ever reaching the fake server (no session is created for them at
// all); only the third, successful dial reaches handleFakeSession.
func TestExec_RetryOnlyWrapsDialPhase(t *testing.T) {
	var execCount int32
	realDialFn, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		atomic.AddInt32(&execCount, 1)
		return "ok", "", 0
	})

	var dialAttempts int32
	flakyDial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		attempt := atomic.AddInt32(&dialAttempts, 1)
		if attempt <= 2 {
			return nil, fmt.Errorf("simulated dial failure %d", attempt)
		}
		return realDialFn(ctx, addr, config)
	}

	tr := newTestTransport(flakyDial, Options{MaxRetries: 3})

	result, err := tr.Exec(context.Background(), testTarget, testCred, "echo hi")
	if err != nil {
		t.Fatalf("expected the third dial attempt to succeed, got error: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := atomic.LoadInt32(&dialAttempts); got != 3 {
		t.Fatalf("expected exactly 3 dial attempts, got %d", got)
	}
	if got := atomic.LoadInt32(&execCount); got != 1 {
		t.Fatalf("expected the command to run exactly ONCE despite 2 dial retries, got %d", got)
	}
}

// TestExec_DialRetryExhaustedThenBreakerOpens proves that once
// MaxRetries dial attempts have all failed, Exec reports a clean error,
// and (when MaxRetries reaches BreakerThreshold) the circuit is now
// open: a following Exec call against the same target fails fast, well
// under the time a real dial-timeout*retries budget would take, and
// attempts no further dial at all.
func TestExec_DialRetryExhaustedThenBreakerOpens(t *testing.T) {
	var dialCalls int32
	failingDial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("simulated unreachable target")
	}
	tr := newTestTransport(failingDial, Options{MaxRetries: 3, BreakerThreshold: 3, BreakerCooldown: time.Hour})

	_, err := tr.Exec(context.Background(), testTarget, testCred, "echo hi")
	if err == nil {
		t.Fatal("expected an error once retries are exhausted")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 3 {
		t.Fatalf("expected exactly 3 dial attempts (MaxRetries), got %d", got)
	}

	start := time.Now()
	_, err = tr.Exec(context.Background(), testTarget, testCred, "echo hi")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the now-open breaker to reject this call")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("expected a clear circuit-open error, got: %v", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 3 {
		t.Errorf("expected NO additional dial attempts once the breaker is open, total stayed at %d", got)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("expected the breaker to fail fast (no dial, no backoff wait), took %v", elapsed)
	}
}

// TestExec_ContextCanceledDuringBackoff proves that a caller's context
// cancellation aborts an in-flight retry loop promptly, rather than
// waiting out the full MaxRetries*backoff budget.
func TestExec_ContextCanceledDuringBackoff(t *testing.T) {
	failingDial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return nil, errors.New("simulated unreachable target")
	}
	// A high threshold/cooldown keeps the breaker out of the way, so
	// only ctx cancellation can explain a fast return here.
	tr := newTestTransport(failingDial, Options{MaxRetries: 5, BreakerThreshold: 1000, BreakerCooldown: time.Hour})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := tr.Exec(ctx, testTarget, testCred, "echo hi")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the context is canceled mid-retry")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected context cancellation to abort retries promptly, took %v", elapsed)
	}
}

// TestExec_SessionFailureIsNeverRetried proves that once a dial has
// succeeded and a session has been established, a failure while running
// the command (as opposed to a non-zero exit code) is reported as a
// genuine error and never triggers a second dial attempt: the dial
// phase is over by then, and Exec's retry loop has already returned.
func TestExec_SessionFailureIsNeverRetried(t *testing.T) {
	hostSigner := generateTestHostKey(t)
	srvConfig := &ssh.ServerConfig{NoClientAuth: true}
	srvConfig.AddHostKey(hostSigner)

	var dialAttempts int32
	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialAttempts, 1)
		clientConn, serverConn := localPipe(t)
		go func() {
			sConn, chans, reqs, err := ssh.NewServerConn(serverConn, srvConfig)
			if err != nil {
				return
			}
			defer sConn.Close()
			go ssh.DiscardRequests(reqs)
			for newChannel := range chans {
				// Reject every session channel outright, forcing
				// session.Run to fail with a connection-level error
				// rather than reporting a translated exit code.
				newChannel.Reject(ssh.Prohibited, "session channels are refused by this test server")
			}
		}()

		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, config)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}

	tr := newTestTransport(dial, Options{MaxRetries: 3})

	_, err := tr.Exec(context.Background(), testTarget, testCred, "echo hi")
	if err == nil {
		t.Fatal("expected an error when the session cannot be established")
	}
	if got := atomic.LoadInt32(&dialAttempts); got != 1 {
		t.Fatalf("expected exactly 1 dial attempt: a post-dial session failure must never trigger a retried dial, got %d", got)
	}
}

// TestBuildAuthMethod is a table-driven test of buildAuthMethod's four
// branches: password, unencrypted key, passphrase-encrypted key, and
// neither set.
func TestBuildAuthMethod(t *testing.T) {
	plainKeyPEM := marshalTestPrivateKey(t, "")
	encryptedKeyPEM := marshalTestPrivateKey(t, "correct-horse")

	tests := []struct {
		name    string
		cred    credential.Credential
		wantErr bool
	}{
		{
			name: "password",
			cred: credential.Credential{Username: "u", Password: "p"},
		},
		{
			name: "unencrypted private key",
			cred: credential.Credential{Username: "u", PrivateKeyPEM: plainKeyPEM},
		},
		{
			name: "passphrase-encrypted private key with correct passphrase",
			cred: credential.Credential{Username: "u", PrivateKeyPEM: encryptedKeyPEM, Passphrase: "correct-horse"},
		},
		{
			name:    "passphrase-encrypted private key with wrong passphrase",
			cred:    credential.Credential{Username: "u", PrivateKeyPEM: encryptedKeyPEM, Passphrase: "wrong"},
			wantErr: true,
		},
		{
			name:    "unparsable private key bytes",
			cred:    credential.Credential{Username: "u", PrivateKeyPEM: []byte("garbage")},
			wantErr: true,
		},
		{
			name:    "neither password nor key set",
			cred:    credential.Credential{Username: "u"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			method, err := buildAuthMethod(tc.cred)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if method != nil {
					t.Error("expected a nil AuthMethod alongside a non-nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if method == nil {
				t.Fatal("expected a non-nil AuthMethod")
			}
		})
	}
}

// marshalTestPrivateKey generates a fresh ed25519 key and returns it PEM
// encoded, encrypted with passphrase if non-empty.
func marshalTestPrivateKey(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return pem.EncodeToMemory(block)
}
