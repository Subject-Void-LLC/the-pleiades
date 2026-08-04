package ssh

import (
	"sync"
	"time"
)

// breakerState is the state of one target's circuit within circuitBreaker.
type breakerState int

const (
	// breakerClosed is the normal state: dials are allowed and failures
	// simply accumulate toward BreakerThreshold.
	breakerClosed breakerState = iota

	// breakerOpen means BreakerThreshold consecutive dial failures have
	// been recorded and BreakerCooldown has not yet elapsed since the
	// most recent failure; Allow fails fast with no dial attempted.
	breakerOpen

	// breakerHalfOpen means BreakerCooldown has elapsed since the
	// circuit opened and exactly one probe dial has been let through;
	// further Allow calls fail fast until that probe's outcome (success
	// or failure) is recorded.
	breakerHalfOpen
)

// breakerEntry tracks one target's ("host:port") circuit state.
type breakerEntry struct {
	state               breakerState
	consecutiveFailures int
	openedAt            time.Time // when this entry most recently transitioned into breakerOpen
}

// circuitBreaker is a minimal, in-memory, per-target circuit breaker:
// closed (normal) -> open (fail fast, no dial attempted) after
// BreakerThreshold consecutive dial failures -> half-open (allow exactly
// one probe dial) once BreakerCooldown has elapsed -> back to closed on
// probe success, or back to open (with a fresh cooldown clock) on probe
// failure.
//
// This is scoped narrowly to one sshTransport's dial phase, not a
// general-purpose project-wide primitive (.SPECIFICATION/PATTERNS.md's
// "Circuit Breaker" entry, marked POTENTIALLY there; this is the first
// real implementation). It coordinates only within this process, the
// same in-memory, mutex-guarded-map shape internal/lock's
// inProcessManager and internal/engine's inProcessWorkflowContext both
// already use.
type circuitBreaker struct {
	mu        sync.Mutex
	threshold int           // consecutive failures required to open a target's circuit
	cooldown  time.Duration // how long an open circuit waits before allowing one probe
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

// Allow reports whether a dial attempt against key ("host:port") may
// proceed right now. A key with no recorded history is always allowed
// (closed, by definition, with zero failures on record). Calling Allow
// on an open circuit whose cooldown has just elapsed transitions that
// circuit to half-open and returns true for exactly this one caller;
// every other concurrent or subsequent caller sees false until the
// resulting probe's outcome is recorded via RecordSuccess or
// RecordFailure.
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
		// half-open probe. Flipping the state here, still under the
		// lock, is what keeps this to exactly one probe even under
		// concurrent callers.
		e.state = breakerHalfOpen
		return true
	case breakerHalfOpen:
		// A probe is already in flight; fail fast rather than pile on
		// more dials while its outcome is still unknown.
		return false
	default:
		return true
	}
}

// RecordSuccess clears key's failure streak and fully closes its
// circuit. This covers both a plain success while closed (simply resets
// the counter that was accumulating toward threshold) and a successful
// half-open probe (which is what actually closes a previously open
// circuit).
func (b *circuitBreaker) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		// Nothing on record for key, so there is nothing to reset; a
		// bare success needs no entry at all.
		return
	}
	e.state = breakerClosed
	e.consecutiveFailures = 0
}

// RecordFailure records one more consecutive dial failure against key.
// If key's circuit was half-open (a probe was in flight), the failed
// probe reopens the circuit and restarts its cooldown clock immediately,
// regardless of threshold. Otherwise, the circuit opens once
// consecutiveFailures reaches threshold.
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
