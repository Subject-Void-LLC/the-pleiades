package remoteexec

import (
	"sync"
	"time"
)

// breakerState is the state of one target's circuit within
// circuitBreaker.
type breakerState int

const (
	// breakerClosed is the normal state: dials are allowed and failures
	// simply accumulate toward the threshold.
	breakerClosed breakerState = iota

	// breakerOpen means the threshold has been reached and the cooldown
	// has not elapsed since the most recent failure; Allow fails fast
	// with no dial attempted.
	breakerOpen

	// breakerHalfOpen means the cooldown elapsed and exactly one probe
	// dial has been let through; further Allow calls fail fast until
	// that probe's outcome is recorded.
	breakerHalfOpen
)

// breakerEntry tracks one target's ("host:port") circuit state.
type breakerEntry struct {
	state               breakerState
	consecutiveFailures int

	// openedAt is when this entry most recently moved into breakerOpen.
	openedAt time.Time
}

// circuitBreaker is a minimal, in-memory, per-target circuit breaker:
// closed (normal) turns to open (fail fast, no dial attempted) after
// threshold consecutive dial failures, turns to half-open (allow exactly
// one probe dial) once the cooldown has elapsed, then back to closed on
// probe success or back to open with a fresh cooldown clock on probe
// failure.
//
// It coordinates only within one process, the same in-memory,
// mutex-guarded-map shape internal/lock's inProcessManager and
// internal/engine's inProcessWorkflowContext already use. Runner.Shared
// is what lets several short-lived callers in one process share one of
// these rather than each starting with a clean slate.
type circuitBreaker struct {
	mu        sync.Mutex
	threshold int           // consecutive failures required to open a circuit
	cooldown  time.Duration // how long an open circuit waits before one probe
	entries   map[string]*breakerEntry
}

// newCircuitBreaker returns a circuitBreaker that opens a target's
// circuit after threshold consecutive dial failures, and allows one
// half-open probe dial after cooldown has elapsed since it opened.
func newCircuitBreaker(threshold int, cooldown time.Duration) *circuitBreaker {
	return &circuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		entries:   make(map[string]*breakerEntry),
	}
}

// Permitted reports whether a dial against key would be allowed right
// now, WITHOUT consuming anything.
//
// It exists because Allow is not a question, it is a transaction: on an
// open circuit whose cooldown has elapsed, Allow hands out the single
// half-open probe and mutates the state to record that it did. A second
// caller asking the same question therefore gets a different answer, so
// any caller that only wants to look, rather than to dial, must ask
// through here.
//
// That distinction is not hypothetical. Two Allow calls on one dial
// path, one checking early and one inside the retry loop, wedge a
// circuit permanently: the first consumes the probe, the second sees a
// probe already in flight and refuses, nothing dials, no outcome is ever
// recorded, and the state never leaves half-open.
func (b *circuitBreaker) Permitted(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return true
	}

	switch e.state {
	case breakerClosed:
		return true
	case breakerOpen:
		return time.Since(e.openedAt) >= b.cooldown
	case breakerHalfOpen:
		// A probe is already in flight. Its outcome decides what happens
		// next, and until it is recorded nothing else may dial.
		return false
	default:
		return true
	}
}

// Allow claims permission to dial against key ("host:port"), consuming
// the half-open probe when there is one to consume.
//
// Exactly one caller may hold that probe at a time, which is what stops
// a recovering target from being hit by every waiting caller at once, so
// this must be called by the code that is about to dial and by nothing
// else. A caller that only wants to know whether dialing is currently
// possible wants Permitted.
//
// A key with no recorded history is always allowed: closed, by
// definition, with zero failures on record. Calling Allow on an open
// circuit whose cooldown has just elapsed moves that circuit to half-open
// and returns true for exactly this one caller; every other concurrent
// or later caller sees false until the resulting probe's outcome is
// recorded through RecordSuccess or RecordFailure.
func (b *circuitBreaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return true
	}

	switch e.state {
	case breakerClosed:
		return true
	case breakerOpen:
		if time.Since(e.openedAt) < b.cooldown {
			return false
		}
		// Cooldown elapsed: let exactly this one caller through as the
		// half-open probe. Flipping the state here, still under the lock,
		// is what keeps it to exactly one probe under concurrent callers.
		e.state = breakerHalfOpen
		return true
	case breakerHalfOpen:
		// A probe is already in flight; fail fast rather than piling on
		// more dials while its outcome is unknown.
		return false
	default:
		return true
	}
}

// RecordSuccess clears key's failure streak and fully closes its
// circuit. This covers both a plain success while closed (which resets
// the counter that was accumulating toward the threshold) and a
// successful half-open probe (which is what actually closes a previously
// open circuit).
func (b *circuitBreaker) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		// Nothing on record for key, so there is nothing to reset; a bare
		// success needs no entry at all.
		return
	}
	e.state = breakerClosed
	e.consecutiveFailures = 0
}

// RecordFailure records one more consecutive dial failure against key.
//
// A failed half-open probe reopens the circuit and restarts its cooldown
// clock immediately, regardless of the threshold. Otherwise the circuit
// opens once consecutiveFailures reaches the threshold.
func (b *circuitBreaker) RecordFailure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		e = &breakerEntry{}
		b.entries[key] = e
	}
	e.consecutiveFailures++

	if e.state == breakerHalfOpen {
		e.state = breakerOpen
		e.openedAt = time.Now()
		return
	}
	if e.consecutiveFailures >= b.threshold {
		e.state = breakerOpen
		e.openedAt = time.Now()
	}
}
