// Package breaker is the platform's one circuit breaker: an in-memory,
// per-target record of consecutive connection failures that makes a
// caller fail fast, with no network traffic, against a target that has
// just failed several times in a row.
//
// Both real remote-execution paths use it: pkg/remoteexec for SSH, telnet
// and serial-over-TCP, and pkg/winrmexec for WinRM. It lives in
// pkg/ because pkg/remoteexec may not import internal/, and it is
// exported for the same reason, not so that a Collection can reach a
// circuit the platform owns. It cannot: this package holds no state of
// its own, every Breaker the platform builds is an unexported field of the
// thing that dials, and internal/archtest refuses a package-level or
// exported Breaker anywhere else. A Collection that builds its own
// Breaker can only open its own circuits.
//
// A target's circuit moves through three states:
//
//   - closed: calls are allowed, and failures count toward the threshold;
//   - open: the threshold was reached, and calls fail fast until the
//     cooldown has passed since the most recent failure;
//   - half-open: the cooldown passed and exactly one caller holds the
//     probe. Its recorded outcome closes the circuit or opens it again.
//
// A call has three outcomes a caller can record, and they are not two:
// RecordSuccess for a target that served, RecordFailure for one that did
// not answer, and RecordRefusal for one that answered by refusing (a login
// it turned down, or a host key or certificate this side turned down). A
// refusal is not an outage, so it never opens a circuit, and it is not
// proof the target serves, so it never closes one or clears a streak.
package breaker

import (
	"errors"
	"sync"
	"time"
)

// The platform's defaults, shared by every transport so SSH and WinRM
// give up on a dead address the same way.
const (
	// DefaultThreshold is how many consecutive failures against one
	// target open its circuit.
	DefaultThreshold = 5

	// DefaultCooldown is how long an open circuit fails fast before it
	// lets one probe through.
	DefaultCooldown = 30 * time.Second
)

// ErrOpen is what a caller refused by an open circuit should wrap, so
// that code further up can tell "nothing was attempted" apart from a real
// connection failure with errors.Is. Its text is the phrase every
// open-circuit message in this module already contains.
var ErrOpen = errors.New("circuit open")

// state is the state of one target's circuit.
type state int

const (
	// closed is the normal state: calls are allowed and failures
	// accumulate toward the threshold.
	closed state = iota

	// open means the threshold was reached and the cooldown has not yet
	// passed since the circuit opened; calls fail fast.
	open

	// halfOpen means the cooldown passed and one probe was handed out;
	// every other caller fails fast until the probe's outcome is recorded
	// or its lease runs out.
	halfOpen
)

// entry is one target's circuit.
type entry struct {
	state               state
	consecutiveFailures int

	// openedAt is when the circuit most recently opened.
	openedAt time.Time

	// probeAt is when the current half-open probe was handed out, which
	// is what its lease is measured from.
	probeAt time.Time
}

// Breaker tracks circuits for any number of targets, keyed by whatever
// string the caller uses to name one (the dialed "host:port" everywhere
// in this module). It is safe for concurrent use. Build one with New;
// the zero value is not usable.
type Breaker struct {
	mu        sync.Mutex
	threshold int           // consecutive failures that open a circuit
	cooldown  time.Duration // how long an open circuit waits before a probe
	entries   map[string]*entry
}

// New returns a Breaker that opens a target's circuit after threshold
// consecutive failures and hands out one probe once cooldown has passed
// since it opened. A threshold below one behaves as one: the first
// failure opens the circuit.
func New(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{
		threshold: threshold,
		cooldown:  cooldown,
		entries:   make(map[string]*entry),
	}
}

// Permitted reports whether a call against key would be allowed right
// now, WITHOUT consuming anything.
//
// Allow is not a question but a transaction: on an open circuit whose
// cooldown has passed it hands out the one half-open probe and records
// that it did, so a second caller asking the same thing gets a different
// answer. A caller that only wants to look, such as an early fast-fail
// check before any expensive setup, must ask here. Two Allow calls on one
// dial path once wedged a circuit permanently (FAILURE_PATTERNS 146).
func (b *Breaker) Permitted(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		// No history at all is a closed circuit with no failures.
		return true
	}
	switch e.state {
	case open:
		return time.Since(e.openedAt) >= b.cooldown
	case halfOpen:
		// A probe is out. Until its outcome is recorded nothing else may
		// call, unless its lease has run out (see Allow).
		return time.Since(e.probeAt) >= b.cooldown
	}
	return true
}

// Allow claims permission to call key, taking the half-open probe when
// there is one to take. It must be called by the code that is about to
// make the call and by nothing else, and that code must then record the
// outcome with RecordSuccess, RecordFailure or RecordRefusal. A call its
// own caller canceled records nothing: it says nothing about the target,
// and a probe it held is reissued after its lease.
//
// Exactly one caller holds the probe at a time, which is what stops a
// recovering target being hit by every waiting caller at once. The probe
// is leased rather than owned: one that nobody records an outcome for
// within one cooldown of being handed out is treated as lost, and the
// next caller gets a fresh one. Without the lease, a single caller that
// took the probe and then returned early latched the circuit half-open
// for the life of the process, which happened twice by two different
// routes (FAILURE_PATTERNS 146 and 398). The cost is that a probe slower
// than the cooldown can have a second one issued beside it, which is one
// more attempt against a target that has been quiet for a whole cooldown.
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		return true
	}
	switch e.state {
	case open:
		if time.Since(e.openedAt) < b.cooldown {
			return false
		}
		// Flipping the state here, still under the lock, is what keeps
		// it to one probe under concurrent callers.
		e.state = halfOpen
		e.probeAt = time.Now()
		return true
	case halfOpen:
		if time.Since(e.probeAt) < b.cooldown {
			// The probe is still out and its lease has not run out.
			return false
		}
		// The probe was lost: hand out a fresh one.
		e.probeAt = time.Now()
		return true
	}
	return true
}

// RecordSuccess clears key's failure streak and closes its circuit. It
// covers both a plain success while closed, which resets the count, and
// a successful probe, which is what closes an open circuit.
func (b *Breaker) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		// Nothing on record, so nothing to reset.
		return
	}
	e.state = closed
	e.consecutiveFailures = 0
}

// EndCooldown cuts short the cooldown of key's open circuit, so the next
// Allow hands out the half-open probe at once instead of failing fast for
// the rest of the cooldown. It changes nothing else: a closed circuit
// stays closed with its streak, and a probe already out keeps its lease.
//
// It is for a caller that has seen the target answer without making a
// call that proves it serves: a listener accepting, before anything has
// logged in. That is evidence enough to stop refusing without traffic, and
// not enough to close the circuit, so the call after it is still the one
// probe whose outcome decides.
func (b *Breaker) EndCooldown(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok || e.state != open {
		return
	}
	// Backdated by one cooldown, so Permitted and Allow read it as over.
	e.openedAt = time.Now().Add(-b.cooldown)
}

// RecordRefusal records that key answered a call by refusing it: it
// turned down the login, or this side turned down its host key or
// certificate. Such an answer proves the path to key carries traffic and
// nothing more, so it is recorded as neither of the other two outcomes.
//
//   - A closed circuit keeps its streak, neither cleared nor lengthened.
//     Recording a refusal as a success once cleared the evidence of an
//     outage: an AAA server that refused some logins and timed out others
//     reset the count on every refusal, so the circuit never opened.
//   - An open circuit stays open, its cooldown unchanged: no call was let
//     through to refuse, so this changes nothing.
//   - A half-open probe answered with a refusal is handed back. The
//     circuit returns to open with its cooldown already over, so the next
//     caller's call is the probe whose outcome decides, at once. It does not
//     close, since nothing proved the target serves, and it does not get a
//     fresh cooldown, since that would count the answer as an outage and
//     make a caller with the right credential wait out a wrong one's turn.
//
// Stopping a refused credential from being sent again is not done here:
// a circuit is per target and shared by every caller, so refusing calls
// after one caller's credential was refused would refuse every other
// caller's too (FAILURE_PATTERNS 399). The caller holding the credential
// stops at its first refusal.
func (b *Breaker) RecordRefusal(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok || e.state != halfOpen {
		// No history, a closed streak or an open circuit: all stay as they
		// are.
		return
	}
	// Backdated by one cooldown, so Permitted and Allow read it as over and
	// hand the probe to the next caller.
	e.state = open
	e.openedAt = time.Now().Add(-b.cooldown)
}

// RecordFailure records one more consecutive failure against key. A
// failed probe opens the circuit again at once, with a fresh cooldown,
// whatever the threshold; otherwise the circuit opens when the streak
// reaches the threshold.
func (b *Breaker) RecordFailure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		e = &entry{}
		b.entries[key] = e
	}
	e.consecutiveFailures++

	if e.state == halfOpen || e.consecutiveFailures >= b.threshold {
		e.state = open
		e.openedAt = time.Now()
	}
}
