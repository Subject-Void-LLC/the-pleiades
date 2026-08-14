// The bounded concurrency gate in front of the KDF.
//
// A memory-hard key derivation is a memory-exhaustion denial of service
// waiting for an unauthenticated caller, and POST /ui/login is exactly
// that. The measured cost of one verification is ~19.9 MB and ~29 ms
// (BenchmarkVerify), so an endpoint that starts a derivation per request
// hands an attacker a multiplier of about twenty megabytes per socket
// against a process that also holds the ent pool, the NATS connection, the
// leader-elected session sweeper and the DAG executor.
//
// This bounds MEMORY. It is not a substitute for either of the other two
// mechanisms and does not overlap with them: internal/api's rate limiter
// bounds RATE and keys on the caller, and LockoutPolicy bounds ATTEMPTS and
// keys on the account under attack. Three mechanisms, three different keys,
// and removing any one of them leaves a gap the other two do not cover.
package localauth

import (
	"context"
	"errors"
	"fmt"
)

// DefaultMaxConcurrentDerivations is how many Argon2id derivations may be
// in flight at once.
//
// Derived from the benchmark rather than chosen: BenchmarkVerify reports
// 19,927,335 B/op at DefaultParams, so four concurrent derivations is a
// worst-case transient footprint just under 80 MB. That is a number a
// controller can carry alongside its other work. The rejected alternative
// is instructive: 64 MiB parameters with no gate at all, which is both a
// stronger hash and a far worse system, because the footprint then has no
// upper bound at all and is set by how fast an attacker can open sockets.
//
// Raising this is a real decision with a real cost, and the cost is
// linear: N times ~20 MB of resident memory during a login burst. Lowering
// it makes concurrent logins queue rather than fail, which is the correct
// direction to be wrong in.
const DefaultMaxConcurrentDerivations = 4

// ErrBusy is returned when a derivation could not start before its
// context expired.
//
// It is deliberately NOT one of the authentication errors. A caller that
// waited too long for a slot learned nothing about whether the account
// exists or the password was right, and answering it with
// ErrInvalidCredentials would be a lie that also trains an operator to
// ignore real failures during load.
var ErrBusy = errors.New("localauth: too many password derivations in flight")

// Gate bounds how many derivations run at once.
//
// A buffered channel rather than a semaphore package, because the whole
// mechanism is two operations and adding a dependency for it would be the
// larger cost. The zero value is not usable; construct one with NewGate.
type Gate struct {
	slots chan struct{}
}

// NewGate builds a gate admitting at most n concurrent derivations.
//
// n below one is treated as one rather than as "unlimited". An unlimited
// gate is the configuration this type exists to prevent, so it must not be
// reachable by typing a zero into a config file.
func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{slots: make(chan struct{}, n)}
}

// Do runs fn while holding a slot, waiting for one if the gate is full.
//
// It respects ctx, so a caller whose client already hung up stops waiting
// instead of holding a slot for a response nobody will read. That matters
// more than it looks: without it, a flood of abandoned requests would keep
// the gate saturated against the legitimate ones behind them.
func (g *Gate) Do(ctx context.Context, fn func()) error {
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
	defer func() { <-g.slots }()

	fn()
	return nil
}
