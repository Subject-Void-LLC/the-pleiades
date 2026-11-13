// The circuit breaker every WinRM call in a process shares: one circuit
// per endpoint, kept here, so that whatever reaches a host through this
// package (the winrm_exec transport, exec.winrm.shell, pkg/winrmsvc and the
// rest) counts its failures in one place and is refused in one place.
package winrmexec

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// endpointHealth holds the process's WinRM circuits, keyed by the
// "host:port" Addr names, so 5985 and 5986 on one host are separate
// listeners with separate circuits.
//
// It lives in this package rather than in each caller because every
// caller dials through Execute. A breaker per caller counted one failure
// once in each of them: the winrm_exec transport kept its own until Phase
// 112, on top of the one call it makes here, and every Collection calling
// Execute directly had none, so a dead lab host cost each of their tasks
// its whole timeout.
type endpointHealth struct {
	mu sync.Mutex
	// breaker is unexported on purpose: pkg/breaker is public, but nothing
	// outside this package may open or close a circuit the platform owns
	// (internal/archtest's TestNoPlatformCircuitIsReachable).
	breaker *breaker.Breaker
}

// endpoints is the process's one set of WinRM circuits. It is reached only
// through the methods below.
var endpoints = newEndpointHealth()

// newEndpointHealth returns circuits with the platform's shared defaults,
// the ones pkg/remoteexec uses for SSH, so both transports give up on a
// dead address the same way.
func newEndpointHealth() *endpointHealth {
	return &endpointHealth{breaker: breaker.New(breaker.DefaultThreshold, breaker.DefaultCooldown)}
}

// circuits returns the breaker in use, read under the lock because a test
// seam may swap it.
func (h *endpointHealth) circuits() *breaker.Breaker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.breaker
}

// swap installs b and returns the breaker it replaced.
func (h *endpointHealth) swap(b *breaker.Breaker) *breaker.Breaker {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.breaker
	h.breaker = b
	return old
}

// ResetCircuitsForTest gives the process fresh WinRM circuits with no
// history and returns a function that puts the old ones back.
//
// It exists for LESSONS_LEARNED 159's reason: the circuits outlive every
// test in a binary, so tests that dial a dead address add to one streak
// and, from some -count on, start reading "circuit open" instead of the
// failure they asked about. internal/archtest forbids production code from
// calling it, since closing every circuit at run time is exactly what the
// breaker exists to prevent.
func ResetCircuitsForTest() func() {
	old := endpoints.swap(breaker.New(breaker.DefaultThreshold, breaker.DefaultCooldown))
	return func() { endpoints.swap(old) }
}

// errCircuitOpen is the refusal a call gets from an open circuit.
func errCircuitOpen(addr string) error {
	return &NotStartedError{Err: fmt.Errorf(
		"%w for %s after %d failures in a row to reach it, so nothing was sent; one try is let through %s after it opened",
		breaker.ErrOpen, addr, breaker.DefaultThreshold, breaker.DefaultCooldown)}
}

// admit claims permission for one counted call to addr. A done context is
// checked first, so a canceled call never takes the half-open probe
// (FAILURE_PATTERNS 398), and a refusal says nothing was sent.
func (h *endpointHealth) admit(ctx context.Context, addr string) error {
	if err := ctx.Err(); err != nil {
		return &NotStartedError{Err: err}
	}
	if !h.circuits().Allow(addr) {
		return errCircuitOpen(addr)
	}
	return nil
}

// record tells addr's circuit what one counted attempt proved about the
// path to it, and nothing it did not prove.
//
// A network failure before the shell opened is a failure. A command that
// finished, or a refusal the service sent back before running anything (a
// credential it rejected, a fault instead of a shell), proves the host
// answered, which is what a probe needs to close the circuit. Everything
// else is left unrecorded: a caller's own cancellation says nothing about
// the host, and a failure after the command started is not certain
// evidence about the network either way. A probe that ends unrecorded is
// not lost: pkg/breaker hands out a fresh one after a cooldown.
func (h *endpointHealth) record(addr string, err error) {
	b := h.circuits()
	var notStarted *NotStartedError
	switch {
	case err == nil:
		b.RecordSuccess(addr)
	case !errors.As(err, &notStarted):
		// After the command started: unrecorded.
	case notStarted.Unreached():
		b.RecordFailure(addr)
	case errors.Is(err, context.Canceled):
		// The caller gave up; the host was never heard from. Recording
		// this as an answer closed an open circuit on no evidence.
	default:
		b.RecordSuccess(addr)
	}
}

// answered is what a wait reports once it has heard the listener answer
// without logging in: the open circuit's cooldown ends, so the next
// counted call is the probe rather than a refusal, and nothing closes.
func (h *endpointHealth) answered(addr string) { h.circuits().EndCooldown(addr) }

// served is what a wait reports once an authenticated command ran: the
// host serves, so its circuit closes and the task after the wait is sent
// at once rather than refused until the cooldown ends.
func (h *endpointHealth) served(addr string) { h.circuits().RecordSuccess(addr) }
