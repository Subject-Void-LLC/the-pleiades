// This file is how the JWKS provider fetches outside its background loop,
// and how it says that fetching has failed.
//
// A request reaches here when its token names a kid the cache does not hold,
// or when the cached key set is past its staleness bound. Both are chosen by
// the caller, and both happen before the token's signature is checked and
// before the rate limiter runs, so the rule here is that no rate of requests
// becomes the same rate of fetches: concurrent requests share one fetch, and
// after one begins no request starts another until a cooldown has passed
// (FAILURE_PATTERNS 1227). Every failed fetch, from a request or from the
// background loop, is logged with the key set's age and the bound, so an
// operator sees the outage before the bound refuses every token
// (FAILURE_PATTERNS 1228).
package auth

import (
	"context"
	"fmt"
	"log/slog"
	"time"
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
// the cancellation of the request that started it (context.WithoutCancel
// keeps that request's values, a trace among them, and drops its deadline):
// a caller that gives up returns at once, and its leaving does not fail the
// fetch for the others waiting on it. Were the fetch to carry the first
// caller's cancellation instead, a client hanging up would cancel it, and the
// cooldown would then hold off the next attempt for everyone.
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
		go p.fly(context.WithoutCancel(ctx), f)
	}
	p.flightMu.Unlock()

	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fly runs one request-path fetch and releases everyone waiting on it. ctx
// carries no cancellation (refetchOnDemand detached it), so the timeout
// applied here is the only bound on the fetch.
func (p *jwksKeyProvider) fly(ctx context.Context, f *jwksFlight) {
	ctx, cancel := context.WithTimeout(ctx, defaultJWKSTimeout)
	defer cancel()
	f.err = p.refreshAndReport(ctx, "request")

	// Cleared before done closes, so a request that sees the fetch finished
	// and asks again meets the cooldown rather than this finished flight.
	p.flightMu.Lock()
	p.inflight = nil
	p.flightMu.Unlock()
	close(f.done)
}

// refreshAndReport fetches the key set and logs the outcome when it is news:
// every failure, at Error once the cached keys are past their bound and at
// Warn before that, and the first success after a failure. trigger names
// what asked for the fetch ("background" or "request").
//
// The URL is logged as given, since ValidateJWKSURL refuses one carrying a
// user name or password, and the error is the fetch's own, which never
// carries a token or a kid.
func (p *jwksKeyProvider) refreshAndReport(ctx context.Context, trigger string) error {
	err := p.refresh(ctx)

	p.mu.Lock()
	age := keyAge(p.now(), p.fetchedAt)
	failures := p.failures
	if err != nil {
		p.failures++
	} else {
		p.failures = 0
	}
	p.mu.Unlock()

	logger := p.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		slog.String("jwks_url", p.jwksURL),
		slog.String("trigger", trigger),
	}
	switch {
	case err != nil && age > p.maxStaleness:
		logger.Error("JWKS refresh failed, and the cached keys are past their staleness bound, so every token is refused until a refresh succeeds",
			append(attrs, slog.String("error", err.Error()), slog.Duration("keys_age", age),
				slog.Duration("max_staleness", p.maxStaleness), slog.Int("failures_in_a_row", failures+1))...)
	case err != nil:
		logger.Warn("JWKS refresh failed; the cached keys stay trusted until they reach their staleness bound",
			append(attrs, slog.String("error", err.Error()), slog.Duration("keys_age", age),
				slog.Duration("max_staleness", p.maxStaleness), slog.Int("failures_in_a_row", failures+1))...)
	case failures > 0:
		logger.Info("JWKS refresh succeeded again", append(attrs, slog.Int("failures_before", failures))...)
	}
	return err
}

// staleError is the refusal for a known kid in a key set past its bound,
// naming the fetch that just failed or was held off when there was one.
func (p *jwksKeyProvider) staleError(fetchErr error) error {
	p.mu.RLock()
	age := keyAge(p.now(), p.fetchedAt)
	p.mu.RUnlock()
	msg := fmt.Sprintf("auth: the JWKS keys were last refreshed %s ago, past their %s staleness bound, so no token is accepted until a refresh succeeds",
		age.Round(time.Second), p.maxStaleness)
	if fetchErr != nil {
		return fmt.Errorf("%s: %w", msg, fetchErr)
	}
	return fmt.Errorf("%s", msg)
}

// keyAge is how long ago fetched was, measured both on the monotonic clock
// and on the wall clock, taking the larger.
//
// Either one alone can understate the age. The wall clock can be stepped
// backward; the monotonic clock may stop while the machine is suspended. A
// smaller age would extend trust in keys nothing has confirmed, so the
// larger one is used, and a clock jumping forward can only refuse early,
// which the next successful refresh repairs. A zero fetched (no fetch has
// succeeded) is infinitely old.
func keyAge(now, fetched time.Time) time.Duration {
	if fetched.IsZero() {
		return time.Duration(1<<63 - 1)
	}
	monotonic := now.Sub(fetched)
	wall := now.Round(0).Sub(fetched.Round(0))
	if wall > monotonic {
		return wall
	}
	return monotonic
}
