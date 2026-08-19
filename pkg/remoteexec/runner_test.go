package remoteexec

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

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
// and a genuine session, without a container's real, independent sshd
// (internal/transport/ssh's container tests cover that side of RULE 0
// for this package's one internal caller). It accepts any
// username/password: auth acceptance and rejection against a real,
// independent server implementation is covered there, not duplicated
// here. It runs handler once per "exec" request on the resulting
// session, replying with handler's stdout, stderr, and exit code. It
// returns a dialFunc that establishes exactly one such connection per
// call, and the server's host public key.
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
		// failure (a fuzz or negative-path test, for example); there is no
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
//
// It drains the channel's own reader first so a command sent with piped
// standard input completes rather than blocking the client's writer: a
// real remote command that reads stdin is what RunWithStdin exists for,
// and a server that never reads would deadlock that test rather than
// exercising it.
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

// newTestRunner builds a Runner wired to dial, with host key
// verification bypassed: the fake server's host key has nothing to do
// with the behavior under test here, and knownhosts_test.go plus
// internal/transport/ssh's container tests already cover verification
// itself.
func newTestRunner(dial dialFunc, opts Options) *Runner {
	opts.InsecureSkipHostKeyVerify = true
	opts = applyDefaults(opts)
	return &Runner{
		opts:    opts,
		breaker: newCircuitBreaker(opts.BreakerThreshold, opts.BreakerCooldown),
		dial:    dial,
	}
}

var testTarget = Target{Host: "127.0.0.1", Port: 2222}

// testAuth is the password Auth every test below connects with; the fake
// server accepts any credential, so which one is used is not the subject
// of these tests.
var testAuth = PasswordAuth("u", "p")

// TestRun_Success proves a successful Run returns the remote command's
// stdout and a zero exit code with no error.
func TestRun_Success(t *testing.T) {
	var execCount int32
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		atomic.AddInt32(&execCount, 1)
		return "hello\n", "", 0
	})
	r := newTestRunner(dial, Options{})

	result, err := r.Run(context.Background(), testTarget, testAuth, "echo hello")
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

// TestRun_NonZeroExitCode proves a command that exits non-zero on the
// remote side is reported through Result.ExitCode with a nil error: a
// non-zero exit code is information, not a Go error.
func TestRun_NonZeroExitCode(t *testing.T) {
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "", "boom\n", 7
	})
	r := newTestRunner(dial, Options{})

	result, err := r.Run(context.Background(), testTarget, testAuth, "exit 7")
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

// TestRun_SeparatesStdoutAndStderr proves stdout and stderr are captured
// into separate Result fields, never combined.
func TestRun_SeparatesStdoutAndStderr(t *testing.T) {
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		return "out-line", "err-line", 0
	})
	r := newTestRunner(dial, Options{})

	result, err := r.Run(context.Background(), testTarget, testAuth, "noop")
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

// TestRun_CommandRunsVerbatim proves the exact command string reaches
// the remote side unmodified: no local shell wrapping, and no string
// concatenation with the target address or anything else.
func TestRun_CommandRunsVerbatim(t *testing.T) {
	const want = `echo "$(whoami)"; rm -rf /tmp/should-not-be-touched`

	var got string
	dial, _ := newFakeSSHServer(t, func(cmd string) (string, string, int) {
		got = cmd
		return "", "", 0
	})
	r := newTestRunner(dial, Options{})

	if _, err := r.Run(context.Background(), testTarget, testAuth, want); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("expected the remote side to receive command verbatim:\nwant: %q\ngot:  %q", want, got)
	}
}

// TestRun_UnusableAuthRefusedBeforeDialing proves a zero Auth is
// rejected explicitly, before any dial is attempted. Connecting with no
// authentication at all is never the right recovery from a device whose
// credential was not found.
func TestRun_UnusableAuthRefusedBeforeDialing(t *testing.T) {
	var dialCalls int32
	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("dial should never be reached")
	}
	r := newTestRunner(dial, Options{})

	_, err := r.Run(context.Background(), testTarget, Auth{}, "echo hi")
	if err == nil {
		t.Fatal("expected an error for an Auth carrying no authentication method")
	}
	if !strings.Contains(err.Error(), "no usable authentication method") {
		t.Errorf("expected a clear no-usable-authentication error, got: %v", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial attempt for an unusable Auth, got %d", got)
	}
}

// TestRun_RetryOnlyWrapsDialPhase is the direct proof of this package's
// central safety rule: a dial that fails twice and succeeds on the third
// attempt must result in the remote command running EXACTLY ONCE, never
// once per dial attempt. The first two dial attempts fail before ever
// reaching the fake server, so no session is created for them at all;
// only the third, successful dial reaches handleFakeSession.
func TestRun_RetryOnlyWrapsDialPhase(t *testing.T) {
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

	r := newTestRunner(flakyDial, Options{MaxRetries: 3})

	result, err := r.Run(context.Background(), testTarget, testAuth, "echo hi")
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

// TestRun_DialRetryExhaustedThenBreakerOpens proves that once MaxRetries
// dial attempts have all failed, Run reports a clean error, and (when
// MaxRetries reaches BreakerThreshold) the circuit is now open: a
// following call against the same target fails fast, well under the time
// a real dial-timeout-times-retries budget would take, and attempts no
// further dial at all.
func TestRun_DialRetryExhaustedThenBreakerOpens(t *testing.T) {
	var dialCalls int32
	failingDial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("simulated unreachable target")
	}
	r := newTestRunner(failingDial, Options{MaxRetries: 3, BreakerThreshold: 3, BreakerCooldown: time.Hour})

	_, err := r.Run(context.Background(), testTarget, testAuth, "echo hi")
	if err == nil {
		t.Fatal("expected an error once retries are exhausted")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 3 {
		t.Fatalf("expected exactly 3 dial attempts (MaxRetries), got %d", got)
	}

	start := time.Now()
	_, err = r.Run(context.Background(), testTarget, testAuth, "echo hi")
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

// TestRun_ContextCanceledDuringBackoff proves a caller's context
// cancellation aborts an in-flight retry loop promptly, rather than
// waiting out the full MaxRetries times backoff budget.
func TestRun_ContextCanceledDuringBackoff(t *testing.T) {
	failingDial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return nil, errors.New("simulated unreachable target")
	}
	// A high threshold and cooldown keep the breaker out of the way, so
	// only ctx cancellation can explain a fast return here.
	r := newTestRunner(failingDial, Options{MaxRetries: 5, BreakerThreshold: 1000, BreakerCooldown: time.Hour})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.Run(ctx, testTarget, testAuth, "echo hi")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error when the context is canceled mid-retry")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected context cancellation to abort retries promptly, took %v", elapsed)
	}
}

// TestRun_SessionFailureIsNeverRetried proves that once a dial has
// succeeded and a session has been established, a failure while running
// the command (as opposed to a non-zero exit code) is reported as a
// genuine error and never triggers a second dial attempt: the dial phase
// is over by then, and the retry loop has already returned.
func TestRun_SessionFailureIsNeverRetried(t *testing.T) {
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
				// Reject every session channel outright, forcing the
				// session open to fail with a connection-level error rather
				// than reporting a translated exit code.
				newChannel.Reject(ssh.Prohibited, "session channels are refused by this test server")
			}
		}()

		sshConn, chans, reqs, err := ssh.NewClientConn(clientConn, addr, config)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(sshConn, chans, reqs), nil
	}

	r := newTestRunner(dial, Options{MaxRetries: 3})

	_, err := r.Run(context.Background(), testTarget, testAuth, "echo hi")
	if err == nil {
		t.Fatal("expected an error when the session cannot be established")
	}
	if got := atomic.LoadInt32(&dialAttempts); got != 1 {
		t.Fatalf("expected exactly 1 dial attempt: a post-dial session failure must never trigger a retried dial, got %d", got)
	}
}

// TestShared_ReusesOneRunnerPerOptions proves Shared hands equal Options
// the same Runner, which is the whole reason it exists: a Collection
// method has nowhere to keep a Runner between task invocations, so
// without this its circuit breaker would start empty every time and
// could never open.
func TestShared_ReusesOneRunnerPerOptions(t *testing.T) {
	// A KnownHostsPath unique to this test keeps it from sharing a Runner
	// with any other test in this package, since the memo is
	// process-wide by design.
	opts := Options{KnownHostsPath: t.TempDir() + "/known_hosts"}

	first := Shared(opts)
	second := Shared(opts)
	if first != second {
		t.Error("Shared returned two different Runners for equal Options, so breaker state is not shared")
	}

	different := Shared(Options{KnownHostsPath: t.TempDir() + "/other_known_hosts"})
	if different == first {
		t.Error("Shared returned the same Runner for different Options, so one caller's settings would silently apply to another")
	}
}

// TestNew_AppliesDefaults proves New applies every documented default to
// a zero-valued Options, and preserves an explicitly set field rather
// than overwriting it.
func TestNew_AppliesDefaults(t *testing.T) {
	r := New(Options{})
	if r.dial == nil {
		t.Error("expected New to wire a real dial function")
	}
	if r.breaker == nil {
		t.Error("expected New to wire a circuit breaker")
	}
	if r.opts.MaxRetries != defaultMaxRetries {
		t.Errorf("expected default MaxRetries %d, got %d", defaultMaxRetries, r.opts.MaxRetries)
	}
	if r.opts.DialTimeout != defaultDialTimeout {
		t.Errorf("expected default DialTimeout %v, got %v", defaultDialTimeout, r.opts.DialTimeout)
	}
	if r.opts.BreakerThreshold != defaultBreakerThreshold {
		t.Errorf("expected default BreakerThreshold %d, got %d", defaultBreakerThreshold, r.opts.BreakerThreshold)
	}
	if r.opts.BreakerCooldown != defaultBreakerCooldown {
		t.Errorf("expected default BreakerCooldown %v, got %v", defaultBreakerCooldown, r.opts.BreakerCooldown)
	}

	explicit := New(Options{MaxRetries: 9, DialTimeout: 3 * time.Second})
	if explicit.opts.MaxRetries != 9 {
		t.Errorf("expected explicit MaxRetries 9 to be preserved, got %d", explicit.opts.MaxRetries)
	}
	if explicit.opts.DialTimeout != 3*time.Second {
		t.Errorf("expected explicit DialTimeout to be preserved, got %v", explicit.opts.DialTimeout)
	}
}

// TestTarget_AddrBracketsIPv6 proves Addr produces an address a dialer
// and a known_hosts lookup both accept, including for an IPv6 literal,
// which needs brackets before a port can be appended to it.
func TestTarget_AddrBracketsIPv6(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   string
	}{
		{name: "ipv4", target: Target{Host: "10.0.0.1", Port: 22}, want: "10.0.0.1:22"},
		{name: "hostname", target: Target{Host: "device.test", Port: 2222}, want: "device.test:2222"},
		{name: "ipv6", target: Target{Host: "2001:db8::1", Port: 22}, want: "[2001:db8::1]:22"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.Addr(); got != tc.want {
				t.Errorf("Addr() = %q, want %q", got, tc.want)
			}
		})
	}
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

// TestConnect_OpenCircuitFailsBeforeAnyOtherWork proves the breaker
// check in Connect is doing something the check inside dialWithRetry
// does not already do: refusing before the host key source is loaded, so
// an open circuit really does cost nothing.
//
// The Runner below is aimed at a known_hosts path that does not exist,
// so if anything ran before the breaker check the error would be about
// host key verification instead.
//
// The two checks are deliberately not the same call. Connect asks
// Permitted, which only looks; dialWithRetry calls Allow, which claims
// the half-open probe. An earlier version of this file had both calling
// Allow, and TestConnect_ProbeSurvivesToTheDial below is the test that
// exists because of what that cost.
func TestConnect_OpenCircuitFailsBeforeAnyOtherWork(t *testing.T) {
	var dialCalls int32
	r := &Runner{
		opts: applyDefaults(Options{
			KnownHostsPath:   filepath.Join(t.TempDir(), "no-such-known-hosts"),
			BreakerThreshold: 1,
			BreakerCooldown:  time.Hour,
		}),
		breaker: newCircuitBreaker(1, time.Hour),
		dial: func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error) {
			atomic.AddInt32(&dialCalls, 1)
			return nil, errors.New("dial should never be reached")
		},
	}

	// One failure reaches a threshold of one, so the circuit is open.
	r.breaker.RecordFailure(testTarget.Addr())

	_, err := r.Connect(context.Background(), testTarget, testAuth)
	if err == nil {
		t.Fatal("expected the open circuit to reject this call")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("error = %v, want the circuit-open refusal", err)
	}
	if strings.Contains(err.Error(), "host key") {
		t.Errorf("error = %v: the host key source was loaded before the breaker was consulted, so an open circuit is not free", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial attempt against an open circuit, got %d", got)
	}
}

// TestConnect_ProbeSurvivesToTheDial is the regression test for a
// permanently wedged circuit breaker.
//
// Allow is a transaction, not a question: on an open circuit whose
// cooldown has elapsed it hands out the single half-open probe and
// mutates the state to record that it did. Connect used to call Allow
// for its own fast-fail check and then dialWithRetry called it again for
// the same request. The first call took the probe, the second saw a
// probe already in flight and refused, so nothing dialed. Because only a
// real dial produces a RecordSuccess or a RecordFailure, nothing ever
// resolved the half-open state, and the half-open branch has no cooldown
// re-check: every later attempt refused too. A device that was briefly
// down became permanently unreachable for the life of the process, and
// since a Collection method shares one Runner through Shared, that is
// the life of the whole run.
//
// The assertions below are about DIALS, not about error strings, because
// the wedge produced a perfectly reasonable-looking "circuit open" error
// every time.
func TestConnect_ProbeSurvivesToTheDial(t *testing.T) {
	const cooldown = 20 * time.Millisecond

	var dialCalls int32
	var succeed atomic.Bool
	dial := func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		if succeed.Load() {
			// A recovered device. The probe must be able to see this.
			return nil, errors.New("recovered, but this test never completes a handshake")
		}
		return nil, errors.New("simulated unreachable target")
	}
	r := newTestRunner(dial, Options{MaxRetries: 1, BreakerThreshold: 1, BreakerCooldown: cooldown})

	// One failure opens the circuit.
	if _, err := r.Connect(context.Background(), testTarget, testAuth); err == nil {
		t.Fatal("expected the first dial to fail")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 1 {
		t.Fatalf("dial attempts after the first call = %d, want 1", got)
	}

	// Inside the cooldown: fast fail with no dial, which is the breaker
	// working correctly.
	if _, err := r.Connect(context.Background(), testTarget, testAuth); err == nil {
		t.Fatal("expected an open circuit to reject a call inside its cooldown")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 1 {
		t.Errorf("dial attempts inside the cooldown = %d, want no additional dial", got)
	}

	// Past the cooldown: the probe must reach the dial. This is the
	// assertion the wedge failed.
	time.Sleep(cooldown + 20*time.Millisecond)
	succeed.Store(true)
	if _, err := r.Connect(context.Background(), testTarget, testAuth); err == nil {
		t.Fatal("expected an error from this test's non-completing dial")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 2 {
		t.Fatalf("dial attempts after the cooldown elapsed = %d, want 2: the half-open probe never reached the dial, so this target is now unreachable forever", got)
	}

	// And the failed probe left the circuit in a state a later cooldown
	// can still get out of, rather than latched.
	time.Sleep(cooldown + 20*time.Millisecond)
	if _, err := r.Connect(context.Background(), testTarget, testAuth); err == nil {
		t.Fatal("expected an error from this test's non-completing dial")
	}
	if got := atomic.LoadInt32(&dialCalls); got != 3 {
		t.Errorf("dial attempts after a second cooldown = %d, want 3: a failed probe must reopen the circuit rather than latch it", got)
	}
}

// TestBreakerPermitted_DoesNotConsumeTheProbe pins the distinction the
// fix rests on, at the breaker itself: looking must not change anything,
// and claiming must.
func TestBreakerPermitted_DoesNotConsumeTheProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	const key = "host:22"

	b := newCircuitBreaker(1, cooldown)
	b.RecordFailure(key) // threshold of one, so the circuit is open

	if b.Permitted(key) {
		t.Error("Permitted reported true inside the cooldown")
	}
	time.Sleep(cooldown + 10*time.Millisecond)

	// Any number of looks must all say yes and leave the probe unclaimed.
	for i := 0; i < 3; i++ {
		if !b.Permitted(key) {
			t.Fatalf("look %d: Permitted reported false after the cooldown elapsed, so it consumed something", i)
		}
	}

	// The claim then succeeds exactly once.
	if !b.Allow(key) {
		t.Fatal("Allow refused the probe that Permitted said was available")
	}
	if b.Allow(key) {
		t.Error("Allow handed out a second probe while the first was still in flight")
	}
	if b.Permitted(key) {
		t.Error("Permitted reported true while a probe was in flight")
	}
}

// TestConnect_HostKeySourceFailureIsReported covers the branch between
// the breaker check and the dial: a known_hosts source that cannot be
// loaded is a hard refusal, not something Connect proceeds past.
func TestConnect_HostKeySourceFailureIsReported(t *testing.T) {
	var dialCalls int32
	dial := func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("dial should never be reached")
	}
	// A closed circuit, so the breaker cannot be what refuses this.
	r := &Runner{
		opts:    applyDefaults(Options{KnownHostsPath: filepath.Join(t.TempDir(), "absent")}),
		breaker: newCircuitBreaker(5, time.Minute),
		dial:    dial,
	}

	_, err := r.Connect(context.Background(), testTarget, testAuth)
	if err == nil {
		t.Fatal("expected a missing known_hosts source to refuse the connection")
	}
	if !strings.Contains(err.Error(), "host key verification unavailable") {
		t.Errorf("error = %v, want it to name the unavailable host key source", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial without a usable host key source, got %d", got)
	}
}

// TestDialWithRetry_StopsWhenTheCircuitOpensMidLoop proves the retry
// loop re-checks the breaker between attempts rather than only before
// the first.
//
// It matters whenever MaxRetries is at least BreakerThreshold, which the
// shipped defaults do not satisfy but a caller may configure: without
// the re-check, a target whose circuit opens on attempt two is still
// dialed for attempts three through N.
func TestDialWithRetry_StopsWhenTheCircuitOpensMidLoop(t *testing.T) {
	var dialCalls int32
	failing := func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("simulated unreachable target")
	}
	// Five attempts allowed, but the circuit opens after two failures.
	r := newTestRunner(failing, Options{MaxRetries: 5, BreakerThreshold: 2, BreakerCooldown: time.Hour})

	_, err := r.Connect(context.Background(), testTarget, testAuth)
	if err == nil {
		t.Fatal("expected the dial to fail")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("error = %v, want the loop to stop on the open circuit", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 2 {
		t.Errorf("dial attempts = %d, want 2: the loop kept dialing after the circuit opened", got)
	}
}

// TestDialWithRetry_AlreadyCanceledContextNeverDials proves a context
// that is already done stops the loop before it reaches the network.
func TestDialWithRetry_AlreadyCanceledContextNeverDials(t *testing.T) {
	var dialCalls int32
	dial := func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error) {
		atomic.AddInt32(&dialCalls, 1)
		return nil, errors.New("dial should never be reached")
	}
	r := newTestRunner(dial, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Connect(ctx, testTarget, testAuth)
	if err == nil {
		t.Fatal("expected an already-canceled context to refuse the connection")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 0 {
		t.Errorf("expected no dial on an already-canceled context, got %d", got)
	}
}

// TestBreakerPermitted_ClosedCircuitAllowsEveryLook covers the ordinary
// state: a target with no failures on record, and one whose streak a
// success has cleared.
func TestBreakerPermitted_ClosedCircuitAllowsEveryLook(t *testing.T) {
	b := newCircuitBreaker(3, time.Hour)
	const key = "host:22"

	if !b.Permitted(key) {
		t.Error("Permitted refused a target with no history at all")
	}

	b.RecordFailure(key) // one of three, so still closed
	if !b.Permitted(key) {
		t.Error("Permitted refused a target below the failure threshold")
	}

	b.RecordSuccess(key)
	if !b.Permitted(key) {
		t.Error("Permitted refused a target whose streak a success cleared")
	}
}
