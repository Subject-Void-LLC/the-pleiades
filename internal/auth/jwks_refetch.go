// This file is how the JWKS provider fetches outside its background loop.
//
// A request reaches here when its token names a kid the cache does not hold.
// The kid is chosen by the caller, and read before the token's signature is
// checked and before the rate limiter runs, so the rule here is that no rate
// of requests becomes the same rate of fetches: concurrent requests share one
// fetch, and after one begins no request starts another until a cooldown has
// passed (FAILURE_PATTERNS 1227).
package auth

import (
	"context"
)

// jwksFlight is one request-path fetch. done closes when it ends, and err is
// its outcome, written before done closes and read only after.
type jwksFlight struct {
	done chan struct{}
	err  error
}

// refetchOnDemand fetches the key set for a request, sharing a fetch already
// running and refusing to start one inside the cooldown.
//
// The fetch runs on its own goroutine under its own timeout, detached from
// any one request: a caller that gives up returns at once, and its leaving
// does not fail the fetch for the others waiting on it. Were the fetch to
// carry the first caller's context instead, a client hanging up would cancel
// it, and the cooldown would then hold off the next attempt for everyone.
func (p *jwksKeyProvider) refetchOnDemand(ctx context.Context) error {
	p.flightMu.Lock()
	f := p.inflight
	if f == nil {
		// No fetch running: start one, unless the last began too recently.
		if !p.lastOnDemand.IsZero() && p.now().Sub(p.lastOnDemand) < p.unknownKidCooldown {
			p.flightMu.Unlock()
			return errRefetchCoolingDown
		}
		f = &jwksFlight{done: make(chan struct{})}
		p.inflight = f
		p.lastOnDemand = p.now()
		go p.fly(f)
	}
	p.flightMu.Unlock()

	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fly runs one request-path fetch and releases everyone waiting on it.
func (p *jwksKeyProvider) fly(f *jwksFlight) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultJWKSTimeout)
	defer cancel()
	f.err = p.refresh(ctx)

	// Cleared before done closes, so a request that sees the fetch finished
	// and asks again meets the cooldown rather than this finished flight.
	p.flightMu.Lock()
	p.inflight = nil
	p.flightMu.Unlock()
	close(f.done)
}
