// Package ssh implements internal/transport.Transport over real SSH
// connections using golang.org/x/crypto/ssh. It is the first real
// Adapter in this repository to ever contact an actual device: dialing,
// authenticating, verifying the remote host key against a known_hosts
// file, and running one command per Exec call.
//
// Retry with backoff+jitter and a per-target circuit breaker guard the
// dial phase only. Once a command has actually been sent to the remote
// side, it is never retried: a command may have partially executed
// remotely already, and blindly retrying it could re-apply an unknown
// side effect (delete a file twice, restart a service twice, ...), which
// would violate this codebase's Convergence principle
// (.SPECIFICATION/PATTERNS.md's "Convergence (Idempotent Desired
// State)" entry). See Exec's doc comment for the precise contract.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// Default tuning values applied when the corresponding Options field is
// left at its Go zero value. These are deliberately conservative:
// DialTimeout and MaxRetries bound how long one Exec call can spend
// failing to connect before giving up, and BreakerThreshold/
// BreakerCooldown bound how long a target stays "fast failed" after it
// starts refusing connections.
const (
	// defaultDialTimeout is used when Options.DialTimeout is zero. Ten
	// seconds is generous enough for a real network hop to a managed
	// device (including a slow jump host) without letting one
	// unreachable target stall a caller for an unbounded time.
	defaultDialTimeout = 10 * time.Second

	// defaultMaxRetries is used when Options.MaxRetries is zero or
	// negative. It is the TOTAL number of dial attempts (not additional
	// retries beyond a first attempt), so the default of 3 means at most
	// three dials before Exec gives up and reports a wrapped error.
	defaultMaxRetries = 3

	// defaultBreakerThreshold is used when Options.BreakerThreshold is
	// zero or negative: how many consecutive dial failures against one
	// target open its circuit.
	defaultBreakerThreshold = 5

	// defaultBreakerCooldown is used when Options.BreakerCooldown is
	// zero or negative: how long an open circuit stays fast-failing
	// before allowing exactly one half-open probe dial.
	defaultBreakerCooldown = 30 * time.Second

	// defaultBackoffBase and defaultBackoffMax bound the jittered
	// exponential backoff (via pkg/retry.Backoff) applied between dial
	// attempts within a single Exec call's retry loop.
	defaultBackoffBase = 250 * time.Millisecond
	defaultBackoffMax  = 5 * time.Second
)

// Options configures a Transport backed by real SSH connections. Every
// field has a documented default applied by New when left at its Go zero
// value, so Options{} is itself a usable, if conservative, configuration.
type Options struct {
	// KnownHostsPath is the OpenSSH-format known_hosts file used to
	// verify a target's host key. Defaults to "$HOME/.ssh/known_hosts"
	// if empty. See known_hosts.go for exactly how a missing or
	// non-matching file is handled (always fail closed, never silently
	// trust).
	KnownHostsPath string

	// InsecureSkipHostKeyVerify, when true, bypasses host key
	// verification entirely (ssh.InsecureIgnoreHostKey). It must default
	// to false: this is an explicit, loud opt-in for a caller that has
	// already decided it does not need MITM protection (a lab sandbox,
	// for example), never a fallback silently taken when known_hosts is
	// unavailable.
	InsecureSkipHostKeyVerify bool

	// DialTimeout bounds a single dial attempt (TCP connect plus SSH
	// handshake). Defaults to defaultDialTimeout if zero. This is a
	// fallback bound only: the primary cancellation signal is the ctx
	// passed to Exec, so a caller's context.WithTimeout or explicit
	// cancellation genuinely aborts an in-flight dial rather than
	// waiting out this fixed duration.
	DialTimeout time.Duration

	// MaxRetries is the total number of dial attempts Exec makes against
	// one target before giving up, with backoff+jitter between attempts.
	// Defaults to defaultMaxRetries if zero or negative. This bounds the
	// dial phase ONLY; a command that has already been sent to the
	// remote side is never retried, regardless of this value.
	MaxRetries int

	// BreakerThreshold is how many consecutive dial failures against one
	// target open that target's circuit, after which further Exec calls
	// fail fast with no dial attempted until BreakerCooldown elapses.
	// Defaults to defaultBreakerThreshold if zero or negative.
	BreakerThreshold int

	// BreakerCooldown is how long a target's open circuit stays
	// fast-failing before allowing exactly one half-open probe dial.
	// Defaults to defaultBreakerCooldown if zero or negative.
	BreakerCooldown time.Duration
}

// applyDefaults returns a copy of opts with every zero-valued field
// replaced by its documented default. Options is passed by value
// throughout this package specifically so this can return a new value
// instead of mutating a caller-owned struct.
func applyDefaults(opts Options) Options {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = defaultDialTimeout
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = defaultMaxRetries
	}
	if opts.BreakerThreshold <= 0 {
		opts.BreakerThreshold = defaultBreakerThreshold
	}
	if opts.BreakerCooldown <= 0 {
		opts.BreakerCooldown = defaultBreakerCooldown
	}
	if opts.KnownHostsPath == "" {
		// $HOME may be unavailable in some sandboxed environments; if so,
		// KnownHostsPath stays empty and hostKeyCallback (known_hosts.go)
		// reports a clear, fail-closed error the first time it is
		// needed, rather than this function silently producing an
		// unusable path.
		if home, err := os.UserHomeDir(); err == nil {
			opts.KnownHostsPath = filepath.Join(home, ".ssh", "known_hosts")
		}
	}
	return opts
}

// dialFunc dials addr and returns a fully handshaken SSH client, or an
// error if the TCP connection or the SSH handshake failed. It exists as
// a named type so sshTransport.dial can be swapped out in this package's
// own test files (never from outside the package) to deterministically
// exercise retry, backoff, and circuit-breaker behavior without any real
// socket.
type dialFunc func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error)

// sshTransport is the real Adapter behind transport.Transport, backed by
// golang.org/x/crypto/ssh. Construct one with New; the zero value is not
// usable (dial and breaker are both required internal collaborators New
// wires up).
type sshTransport struct {
	opts Options

	// breaker tracks consecutive dial failures per "host:port" target
	// key and short-circuits Exec while a target's circuit is open. See
	// circuit_breaker.go.
	breaker *circuitBreaker

	// dial performs the actual dial-plus-handshake. New sets this to
	// realDial; only this package's test files ever override it.
	dial dialFunc
}

// New returns a transport.Transport backed by real SSH connections,
// configured by opts. Any zero-valued field in opts is replaced by its
// documented default (see the Options doc comments).
func New(opts Options) transport.Transport {
	opts = applyDefaults(opts)
	return &sshTransport{
		opts:    opts,
		breaker: newCircuitBreaker(opts.BreakerThreshold, opts.BreakerCooldown),
		dial:    realDial,
	}
}

// Exec implements transport.Transport. See transport.Transport's doc
// comment (internal/transport/transport.go) for the precise, load-bearing
// distinction between a non-zero Result.ExitCode (not a Go error) and a
// non-nil error return (the outcome could not be determined at all);
// this method honors that contract exactly.
//
// Retry is scoped strictly to the dial phase: constructing the TCP
// connection and completing the SSH handshake, via dialWithRetry
// (backoff.go). Once a Session's Run has actually been sent to the
// remote side below, this method NEVER retries it. A command may have
// partially executed remotely already; blindly retrying it could
// re-apply an unknown side effect (delete a file twice, restart a
// service twice, ...), which would violate this codebase's Convergence
// principle. A connection lost mid-session is reported as a genuine
// error, never silently retried and never folded into Result.ExitCode.
func (t *sshTransport) Exec(ctx context.Context, target transport.Target, cred credential.Credential, command string) (transport.Result, error) {
	addr := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))

	// Step 1: consult the circuit breaker before doing any other work.
	// A target with too many recent consecutive dial failures fails fast
	// here, with zero network I/O and no auth/host-key work performed.
	if !t.breaker.Allow(addr) {
		return transport.Result{}, fmt.Errorf("circuit open for %s, too many recent failures", addr)
	}

	// Step 2: build the ssh.ClientConfig. A credential that cannot
	// produce a usable auth method, or a host-key source that cannot be
	// loaded, is a hard error here; this method never proceeds with a
	// nil or empty Auth list, and never silently skips host key
	// verification.
	auth, err := buildAuthMethod(cred)
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: %w", err)
	}

	hostKeyCB, err := hostKeyCallback(t.opts)
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: host key verification unavailable: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            cred.Username,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: hostKeyCB,
		// Timeout is a fallback bound only. The primary cancellation
		// signal is ctx, honored inside realDial and dialWithRetry, so a
		// caller's own context.WithTimeout or cancellation genuinely
		// aborts an in-flight dial rather than waiting out this fixed
		// duration.
		Timeout: t.opts.DialTimeout,
	}

	// Step 3: dial with retry+backoff, scoped to the dial phase only.
	// dialWithRetry itself records each failed attempt against the
	// breaker and re-checks Allow before every attempt, so a circuit
	// that opens mid-loop (MaxRetries >= BreakerThreshold) stops
	// dialing immediately rather than exhausting every remaining
	// attempt.
	client, err := t.dialWithRetry(ctx, addr, config)
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: dial %s: %w", addr, err)
	}
	// Step 4: a successful dial clears this target's failure streak,
	// closing the breaker (or completing a half-open probe
	// successfully).
	t.breaker.RecordSuccess(addr)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return transport.Result{}, fmt.Errorf("ssh: open session on %s: %w", addr, err)
	}
	defer session.Close()

	// Stdout and Stderr are captured into SEPARATE buffers (never
	// CombinedOutput), since transport.Result requires them as separate
	// strings.
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// command runs VERBATIM: no local shell invocation, and no string
	// concatenation with target.Host, addr, or anything else. This is a
	// hard security requirement, not a convenience default; the caller
	// is solely responsible for what command contains.
	runErr := session.Run(command)

	result := transport.Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var exitErr *ssh.ExitError
	switch {
	case runErr == nil:
		result.ExitCode = 0
	case errors.As(runErr, &exitErr):
		// A non-zero remote exit status is real, useful information,
		// NOT a Go error (see the doc comment above): the command ran
		// to completion and reported failure. Translate it into
		// Result.ExitCode and return a nil error.
		result.ExitCode = exitErr.ExitStatus()
	default:
		// Anything else (connection dropped mid-session, protocol
		// error, ...) means command's true outcome on the remote side
		// is unknown. This is a genuine error, and per the doc comment
		// above it is never retried and never folded into ExitCode.
		return transport.Result{}, fmt.Errorf("ssh: run command on %s: %w", addr, runErr)
	}

	return result, nil
}
