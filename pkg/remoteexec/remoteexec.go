// Package remoteexec runs one command on one device over a real SSH
// connection, and is the single place this platform does that.
//
// It exists because of a constraint, not a preference. A Collection
// method may import pkg/ and the standard library and nothing else in
// this module, which internal/archtest enforces
// (TestCatalogPackagesImportOnlyPkg). That is the same constraint a
// third-party Collection will have to satisfy once out-of-tree
// distribution exists, so it is not going to be relaxed. Before this
// package existed, the one SSH-backed Collection method hand-rolled its
// own dial, its own authentication and its own host key check, and its
// own doc comment recorded that the tradeoff "would need revisiting if
// this package grew a second, write-capable method." A catalog of
// twenty write-capable methods would have meant twenty copies of a
// security-critical dial loop, each with its own host key verification
// and none of them sharing a circuit breaker.
//
// So the mechanism lives here, once, and everything else adapts to it.
// internal/transport/ssh keeps its transport.Transport identity, its
// credential.Credential translation and its Options surface, and
// delegates the actual dialing to this package. A Collection method
// calls it directly. Neither has a second copy of the parts that must
// not diverge: retry with backoff, the per-target circuit breaker,
// fail-closed known_hosts verification, and turning a secret into
// exactly one authentication method.
//
// This package takes plain scalars and its own small value types, never
// an inventory item, a credential store or a runbook task. That is
// pkg/catalystcenter's established shape for the same reason: the code
// on both sides of the pkg/-only line has to be able to call it, and
// neither side should need the other's types to do so.
//
// # What is deliberately not retried
//
// Retry with backoff and the circuit breaker guard the dial phase only:
// the TCP connect and the SSH handshake. Once a command has been sent
// to the remote side it is never retried. The command may have already
// partially run, and re-sending it could apply an unknown side effect
// twice (delete a file twice, restart a service twice), which is
// exactly what this codebase's Convergence principle forbids. A
// connection lost mid-command is reported as a real error, never folded
// into an exit code and never quietly re-attempted.
package remoteexec

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Default tuning values, applied by applyDefaults to any Options field
// left at its Go zero value. They are deliberately conservative:
// together they bound how long one call can spend failing to connect,
// and how long a target stays fast-failing after it starts refusing
// connections.
const (
	// defaultDialTimeout bounds a single dial attempt (TCP connect plus
	// SSH handshake). Ten seconds is generous enough for a real network
	// hop to a managed device, including a slow jump host, without
	// letting one unreachable target stall a caller indefinitely.
	defaultDialTimeout = 10 * time.Second

	// defaultMaxRetries is the TOTAL number of dial attempts, not extra
	// attempts beyond a first one, so 3 means at most three dials before
	// the call gives up.
	defaultMaxRetries = 3

	// defaultBreakerThreshold is how many consecutive dial failures
	// against one target open its circuit.
	defaultBreakerThreshold = 5

	// defaultBreakerCooldown is how long an open circuit stays
	// fast-failing before it allows exactly one half-open probe dial.
	defaultBreakerCooldown = 30 * time.Second

	// defaultBackoffBase and defaultBackoffMax bound the jittered
	// exponential backoff (pkg/retry.Backoff) between dial attempts
	// inside one call's retry loop.
	defaultBackoffBase = 250 * time.Millisecond
	defaultBackoffMax  = 5 * time.Second
)

// Target is the address of one device: where to connect, and nothing
// else.
//
// It carries no device identity, no capability and no credential on
// purpose. Turning an inventory item into a Target is the caller's job,
// and keeping this type this narrow is what lets both a Collection
// method and internal/transport/ssh build one without either of them
// needing the other's vocabulary.
type Target struct {
	// Host is the address or hostname to connect to.
	Host string

	// Port is the TCP port to connect to.
	Port int
}

// Addr returns the "host:port" string a dialer and a known_hosts lookup
// both need, with IPv6 bracketing handled by net.JoinHostPort.
func (t Target) Addr() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// Result is what one remote command produced.
//
// A non-zero ExitCode is NOT an error. It means the command reached the
// device, ran to completion, and reported failure, which is real and
// useful information the caller is expected to interpret. An error
// return means the opposite: the command's true outcome on the remote
// side could not be determined at all, and Result is meaningless.
type Result struct {
	// Stdout is everything the command wrote to standard output,
	// captured separately from Stderr, never combined.
	Stdout string

	// Stderr is everything the command wrote to standard error.
	Stderr string

	// ExitCode is the command's remote exit status. Zero means success.
	ExitCode int
}

// Options configures a Runner. Every field has a documented default
// applied when left at its Go zero value, so Options{} is itself usable,
// if conservative.
//
// Options is comparable on purpose: Shared uses it as a map key, so a
// field added here must stay comparable (no slices, maps or functions).
type Options struct {
	// KnownHostsPath is the OpenSSH-format known_hosts file used to
	// verify a target's host key, and is the most specific of three
	// sources. When empty it falls back to the KnownHostsEnv environment
	// variable, and then to "$HOME/.ssh/known_hosts". All three resolve at
	// the moment a connection is made, not when the Runner is built, so a
	// file written or a variable exported after construction still counts.
	// A missing file is a hard error, never trust on first use; see
	// knownhosts.go.
	//
	// Note for anything that keys off an Options value, as Shared does:
	// two Runners built from an equal Options can still verify against
	// different files, because the environment is read per connection and
	// is not part of this struct. That is deliberate and harmless. What
	// Shared memoizes is circuit-breaker state, which is about whether a
	// target answers, not about which key it presented.
	KnownHostsPath string

	// InsecureSkipHostKeyVerify bypasses host key verification
	// completely. It must default to false: this is an explicit, loud
	// opt-in for a caller that has already decided it does not need MITM
	// protection (a lab sandbox, a throwaway container), never a
	// fallback taken silently because known_hosts was unavailable.
	InsecureSkipHostKeyVerify bool

	// DialTimeout bounds a single dial attempt. It is a fallback bound
	// only: the caller's ctx is the primary cancellation signal, so a
	// context deadline genuinely aborts an in-flight dial rather than
	// waiting this duration out. It matters most when the caller passes
	// a context with no deadline at all.
	DialTimeout time.Duration

	// MaxRetries is the total number of dial attempts against one target
	// before giving up, with backoff and jitter in between. It bounds the
	// dial phase ONLY. A command already sent to the remote side is never
	// retried no matter what this is set to.
	MaxRetries int

	// BreakerThreshold is how many consecutive dial failures against one
	// target open that target's circuit, after which further calls fail
	// fast with no dial attempted until BreakerCooldown elapses.
	BreakerThreshold int

	// BreakerCooldown is how long an open circuit stays fast-failing
	// before allowing exactly one half-open probe dial.
	BreakerCooldown time.Duration
}

// applyDefaults returns a copy of opts with every zero-valued field
// replaced by its documented default. Options is passed by value
// throughout this package so this can return a new value rather than
// mutate one the caller still owns.
//
// KnownHostsPath is deliberately NOT defaulted here. Resolving $HOME
// once at construction time would freeze a path that the caller may not
// have populated yet, and would make Shared's memoized Runner ignore a
// later change; hostKeyCallback resolves it per connection instead.
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
	return opts
}

// Runner dials devices and runs commands on them, applying this
// package's retry, backoff and circuit-breaker policy. Build one with
// New or Shared; the zero value is not usable.
type Runner struct {
	opts Options

	// breaker tracks consecutive dial failures per "host:port" target and
	// short-circuits a call while a target's circuit is open. It is
	// per-Runner, which is why Shared exists: a Runner constructed fresh
	// for every task would carry a breaker that has never seen a failure.
	breaker *circuitBreaker

	// dial performs the dial plus handshake. New sets this to realDial;
	// only this package's own tests substitute anything else.
	dial dialFunc
}

// New returns a Runner configured by opts, with its own private circuit
// breaker.
//
// Prefer Shared for a caller that runs many short-lived operations
// against the same fleet inside one process, which is what a Collection
// method does. A Runner built fresh per task starts with an empty
// breaker every time, so the breaker can never open and an unreachable
// device is dialed its full retry budget on every single task.
func New(opts Options) *Runner {
	opts = applyDefaults(opts)
	return &Runner{
		opts:    opts,
		breaker: newCircuitBreaker(opts.BreakerThreshold, opts.BreakerCooldown),
		dial:    realDial,
	}
}

// shared memoizes one Runner per distinct Options value for the lifetime
// of the process, so callers that cannot hold a Runner of their own
// still share breaker state.
var shared = struct {
	mu      sync.Mutex
	runners map[Options]*Runner
}{runners: map[Options]*Runner{}}

// Shared returns the process-wide Runner for opts, creating it on first
// use. Two callers passing an equal Options get the same Runner, and
// therefore the same circuit-breaker state.
//
// This is what a Collection method should call. A method is invoked once
// per task with no place to keep a Runner between invocations, so
// without this every task against a dead device would pay the full
// MaxRetries budget and the breaker would never open. With it, a runbook
// of twenty tasks against an unreachable device stops dialing after
// BreakerThreshold failures.
//
// The honest limit: this shares state within ONE process. Under the
// Walk tier every Collection method runs in its own short-lived
// subprocess, so there the breaker is scoped to a single task and buys
// nothing beyond what New would. It is the Crawl tier, and any future
// in-process execution model, that this helps.
//
// The map is unbounded in principle. In practice Options varies over a
// small set (a known_hosts path, an insecure opt-out, a few timeouts),
// so it does not grow with the size of a fleet or the length of a run.
func Shared(opts Options) *Runner {
	shared.mu.Lock()
	defer shared.mu.Unlock()

	if r, ok := shared.runners[opts]; ok {
		return r
	}
	r := New(opts)
	shared.runners[opts] = r
	return r
}

// SnapshotForTest captures the process-wide Runner memo and returns a
// function that puts it back, for a test whose subject is what a dial
// FAILURE looks like.
//
// Such a test is the one caller that cannot tolerate this memo. The
// breaker counts CONSECUTIVE failures per Runner with no time window and
// no decay, and Shared keys its Runners by Options, so every test in a
// binary that dials the same dead address shares one counter and every
// iteration of every one of them adds to it. Cross a threshold and the
// error stops naming the dial failure and starts saying "circuit open"
// instead -- which is the breaker working exactly as designed, arriving
// as a test failure in a test that never asked for it.
//
// That is not hypothetical: internal/catalog/net/ssh's own
// TestPing_DialFailureIsReported passes at -count=1 and -count=2 and
// fails from -count=3, because one Ping spends three dial attempts and
// the threshold is five.
//
// It EMPTIES the memo as well as capturing it, and both halves are
// load-bearing. Capturing alone is not enough and was tried first: the map
// holds Runner POINTERS, so putting the same map back hands the next
// iteration the very same Runner with its failure count intact, and a
// Runner built by some earlier test in the binary is never dropped at all.
// Emptying is what guarantees the caller a Runner with a zero counter,
// which is the whole point.
//
// Emptying is safe in a way that emptying a registry would not be. This is
// a memo, not a vocabulary: Shared rebuilds any entry on next use, so the
// only cost of starting empty is one allocation. Nothing looks a Runner up
// here expecting to find it.
//
// Reaching into the Runner to zero its breaker instead was rejected. That
// would couple every caller of this seam to the breaker's internal shape,
// and it would leave a second Runner for the same Options -- one held
// directly by a caller that used New -- still counting.
//
// internal/archtest forbids production code from calling this.
func SnapshotForTest() func() {
	shared.mu.Lock()
	saved := make(map[Options]*Runner, len(shared.runners))
	for k, v := range shared.runners {
		saved[k] = v
	}
	shared.runners = map[Options]*Runner{}
	shared.mu.Unlock()

	return func() {
		restored := make(map[Options]*Runner, len(saved))
		for k, v := range saved {
			restored[k] = v
		}

		shared.mu.Lock()
		defer shared.mu.Unlock()
		shared.runners = restored
	}
}

// Run dials through hops (if any) to target, runs command, and closes the
// connection.
//
// It is the whole-operation convenience for a caller that needs exactly
// one command. A caller that needs several against the same device
// should use Connect and reuse the returned Conn, which pays for one
// chain of handshakes instead of one per command.
//
// command is sent VERBATIM. Nothing here wraps it in a local shell or
// concatenates it with the target address, and the caller is solely
// responsible for what it contains. QuoteArg and QuoteCommand are how a
// caller builds one safely from untrusted parts.
func (r *Runner) Run(ctx context.Context, hops []Hop, target Target, auth Auth, command string) (Result, error) {
	conn, err := r.Connect(ctx, hops, target, auth)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = conn.Close() }()

	return conn.Run(ctx, command)
}

// connectLeg is one address this Runner authenticates to on the way to a
// caller's requested target: either one of hops, in order, or the target
// itself, always last.
type connectLeg struct {
	addr                      string
	auth                      Auth
	insecureSkipHostKeyVerify bool
}

// Connect dials through hops, in order, then to target, completing an
// independent SSH handshake at every leg, and returns a live connection to
// target the caller must Close. hops may be nil or empty, in which case
// this is exactly a direct connection to target: every existing caller
// keeps working unedited by passing nil.
//
// Each leg past the first is reached by tunneling through the previous
// leg's already-authenticated connection (see dialThroughHop); the target
// is always the LAST leg dialed, so a bastion sees only ciphertext for
// everything past its own hop. The circuit breaker, retry and backoff are
// applied identically to every leg, keyed by that leg's own address, via
// the same dialWithRetry a direct (zero-hop) connection already used.
//
// The circuit breaker is consulted before any network I/O for each leg, so
// a leg with too many recent consecutive failures costs nothing at all.
// Every failed attempt is recorded against that leg's own breaker entry
// immediately, and a successful connection clears its failure streak.
func (r *Runner) Connect(ctx context.Context, hops []Hop, target Target, auth Auth) (conn *Conn, err error) {
	legs := make([]connectLeg, 0, len(hops)+1)
	for _, h := range hops {
		legs = append(legs, connectLeg{addr: h.Target.Addr(), auth: h.Auth, insecureSkipHostKeyVerify: h.InsecureSkipHostKeyVerify})
	}
	legs = append(legs, connectLeg{addr: target.Addr(), auth: auth, insecureSkipHostKeyVerify: r.opts.InsecureSkipHostKeyVerify})

	chain, err := r.dialChain(ctx, legs)
	if err != nil {
		return nil, err
	}
	return &Conn{client: chain[len(chain)-1], chain: chain, addr: legs[len(legs)-1].addr}, nil
}

// dialChain dials through legs, in order, completing an independent SSH
// handshake at each one, tunneling every leg past the first through the
// previous leg's already-authenticated connection (dialThroughHop). It is
// Connect's own per-leg loop, extracted so DialThroughHops (tunnel.go) can
// reuse the identical circuit-breaker, retry, backoff, and host-key
// machinery for its own hops-only chain, with target handled differently.
//
// On any failure it closes every client already established, innermost
// (most recently dialed) first, the same order Conn.Close itself uses and
// for the identical reason (a hop's client owns the tunneled connection
// the next one was built on), so a partial chain is never left dangling
// for the caller to notice only when something later breaks.
func (r *Runner) dialChain(ctx context.Context, legs []connectLeg) (chain []*ssh.Client, err error) {
	chain = make([]*ssh.Client, 0, len(legs))
	defer func() {
		if err != nil {
			for i := len(chain) - 1; i >= 0; i-- {
				_ = chain[i].Close()
			}
		}
	}()

	for i, leg := range legs {
		if !leg.auth.usable() {
			// Refused before any dial. Proceeding with no authentication
			// would let a device with no stored credential silently
			// attempt an unauthenticated login, which is never what the
			// caller meant. Checked per leg: a hop with no credential
			// must refuse naming that hop, never fall through to
			// whatever the target's own Auth happens to be.
			return nil, fmt.Errorf("remoteexec: no usable authentication method for %s", leg.addr)
		}

		// Step 1: fail fast on an open circuit, with zero network I/O and
		// no host key work performed.
		//
		// Permitted, never Allow. Allow is a transaction that consumes
		// the half-open probe, and calling it here as well as inside the
		// retry loop below wedged a recovering target permanently: this
		// call took the probe, the loop's own call then saw a probe
		// already in flight and refused, so nothing dialed, no outcome
		// was recorded, and the circuit never left half-open. The loop
		// is where the dial happens, so the loop is where the probe is
		// claimed.
		if !r.breaker.Permitted(leg.addr) {
			return nil, fmt.Errorf("remoteexec: circuit open for %s, too many recent failures", leg.addr)
		}

		// Step 2: build this leg's own host key check. A host key source
		// that cannot be loaded is a hard error here; this never
		// proceeds with verification silently skipped for any leg,
		// including a hop.
		hostKeyCB, hkErr := hostKeyCallbackFor(r.opts, leg.insecureSkipHostKeyVerify)
		if hkErr != nil {
			return nil, fmt.Errorf("remoteexec: host key verification unavailable for %s: %w", leg.addr, hkErr)
		}

		config := leg.auth.clientConfig(hostKeyCB, r.opts.DialTimeout)

		// Step 3: dial with retry and backoff, scoped to the dial phase
		// only. The first leg dials directly (r.dial); every leg after
		// it tunnels through the previous leg's own client, chain[i-1].
		dial := r.dial
		if i > 0 {
			dial = dialThroughHop(chain[i-1])
		}
		next, dialErr := r.dialWithRetry(ctx, dial, leg.addr, config)
		if dialErr != nil {
			return nil, fmt.Errorf("remoteexec: dial %s: %w", leg.addr, dialErr)
		}

		// Step 4: a successful dial closes the circuit, or completes a
		// half-open probe successfully, for this leg.
		r.breaker.RecordSuccess(leg.addr)
		chain = append(chain, next)
	}

	return chain, nil
}
