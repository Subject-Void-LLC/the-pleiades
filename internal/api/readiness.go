package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// defaultReadinessMinInterval is the shortest gap between two real
// dependency checks, and therefore the oldest a /readyz answer can be.
//
// 250 milliseconds, which is the FASTEST cadence anything in this
// repository probes at, not the most common one. The distinction is the
// whole of the choice and it was got wrong once on the way here.
//
// The obvious anchor is the steady-state period: docker-compose.yml's
// controller healthcheck and the chart's controller readinessProbe both run
// every 5 seconds, which would allow an interval of a second with room to
// spare. But the same compose healthcheck also sets start_interval: 250ms,
// and that is the one that matters. During start_period compose polls four
// times a second precisely so that a controller becomes healthy the moment
// it is ready rather than at the next 5 second boundary, which is what took
// this stack's warm start from about 14 seconds to about 4. A one second
// interval would have handed three of every four of those startup probes a
// cached answer and given a quarter of that hard-won time back, for no
// reason a reader of the 5 second number would ever have seen.
//
// So the rule is: no legitimate probe should ever be answered from cache,
// and the fastest legitimate probe is 250ms. The bound this leaves is at
// most four real dependency checks per second at any caller rate, which is
// the difference between a ceiling and no ceiling; the gap between four per
// second and one per second is not worth a regression in the number the
// packaging work exists to protect.
//
// Lengthening this past 250ms starts hiding real state from a probe that
// asked for it. Shortening it buys detection nobody consumes and raises the
// ceiling for free.
const defaultReadinessMinInterval = 250 * time.Millisecond

// readinessOutcome is one computed answer, held so that later callers can
// be handed it without repeating the work that produced it.
type readinessOutcome struct {
	body   healthResponse
	status int
}

// readinessFlight is one real check round in progress. Callers that arrive
// while it runs wait on done and read outcome, rather than starting a
// second round.
type readinessFlight struct {
	done    chan struct{}
	outcome readinessOutcome
}

// readinessGate bounds what /readyz costs, by TIME rather than by caller
// rate.
//
// THE DEFECT THIS EXISTS FOR. /readyz is mounted on the root router,
// outside the versioned prefix and therefore outside the rate limiter, and
// every request ran a real database query. Nothing bounded the pool either,
// so an unauthenticated caller who could reach the port could drive
// unbounded concurrent database work and flip a healthy controller out of
// its load balancer. Production packaging made that sharper by pointing the
// container healthcheck at the same endpoint, so the controller reported
// itself unhealthy under a load anyone could generate.
//
// WHY NOT A CACHE. A plain time-to-live cache does not fix this. Under a
// burst of N concurrent requests with an expired entry, all N miss and all
// N run the check, so the work is still proportional to concurrency, which
// is precisely the quantity the caller chooses. A cache applies a divisor
// where what is needed is a bound.
//
// So this is two mechanisms and needs both. Single-flight collapse means at
// most one check is ever in progress, which bounds the burst. A minimum
// interval between rounds means a completed answer is reused for a while,
// which bounds the sustained rate. Either alone leaves a hole: collapse
// alone still admits a new round the instant the previous one finishes, and
// an interval alone still admits N concurrent rounds at each expiry.
//
// WHY NOT golang.org/x/sync/singleflight. It implements the first half
// exactly and nothing of the second, and the two halves share the state
// that decides which one applies: whether to reuse, to join, or to lead is
// one decision over one mutex here, where composing the package with a
// separate expiry cache would spread it over two locks and admit the
// interleavings between them.
//
// THE LEADER IS NOT THE CALLER. The real check runs on a context detached
// from the request that triggered it (context.WithoutCancel plus this
// gate's own timeout), so a caller who disconnects mid-flight cannot abort
// the round that every waiter is about to read. That is deliberate and it
// is this phase's own recurring lesson: three certificate-provisioning
// designs were torn out for making one participant's bad state everybody
// else's problem, and a shared computation cancelled by whichever caller
// happened to arrive first is the same shape. Trace context still crosses
// the boundary, because WithoutCancel keeps values and drops only
// cancellation, which is the same seam internal/runner's
// detachedValueContext draws for a non-interruptible execution.
type readinessGate struct {
	checks      []ReadinessCheck
	timeout     time.Duration
	minInterval time.Duration
	logger      *slog.Logger

	// now is injectable so a test can state the moment it is asking about
	// instead of sleeping to reach one, the same reason CheckHeartbeat in
	// internal/runner takes its own now parameter.
	now func() time.Time

	mu     sync.Mutex
	flight *readinessFlight
	last   *readinessOutcome
	lastAt time.Time

	// joined counts callers that found a round already in progress and
	// waited for it instead of starting their own.
	//
	// It exists for the tests and says so plainly rather than pretending
	// to be a metric. The concurrency claims here are about what did NOT
	// happen (no second check ran), and a test can only assert that once
	// it knows every caller has actually reached the decision point;
	// waiting on goroutine startup instead makes the test pass by timing
	// luck, which is how the first version of it was written and how a
	// mutation run caught it. Incremented under mu, so a reader that takes
	// mu sees a consistent count.
	joined int64
}

// joinedCount reports how many callers have waited on somebody else's
// round. Test-only; see the field.
func (g *readinessGate) joinedCount() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.joined
}

// newReadinessGate builds the gate /readyz answers from. A zero timeout or
// minInterval takes this package's default.
func newReadinessGate(logger *slog.Logger, checks []ReadinessCheck, timeout, minInterval time.Duration) *readinessGate {
	if timeout <= 0 {
		timeout = readinessProbeTimeout
	}
	if minInterval < 0 {
		minInterval = 0
	}
	return &readinessGate{
		checks:      checks,
		timeout:     timeout,
		minInterval: minInterval,
		logger:      logger,
		now:         time.Now,
	}
}

// outcome returns the current readiness answer, running a real check round
// only when there is neither a fresh one nor one already in progress.
//
// The bool reports whether an answer was produced at all. False means the
// CALLER's context ended while it was waiting for somebody else's round, so
// there is nobody left to write a response to; it never means the check
// failed, which is an outcome with a 503 status.
func (g *readinessGate) outcome(ctx context.Context) (readinessOutcome, bool) {
	g.mu.Lock()

	// Fresh enough: hand back the last answer and touch nothing.
	if g.last != nil && g.now().Sub(g.lastAt) < g.minInterval {
		out := *g.last
		g.mu.Unlock()
		return out, true
	}

	// Somebody else is already asking. Join them.
	if f := g.flight; f != nil {
		g.joined++
		g.mu.Unlock()
		select {
		case <-f.done:
			return f.outcome, true
		case <-ctx.Done():
			return readinessOutcome{}, false
		}
	}

	// Lead the round.
	f := &readinessFlight{done: make(chan struct{})}
	g.flight = f
	g.mu.Unlock()

	f.outcome = g.runChecks(ctx)

	g.mu.Lock()
	last := f.outcome
	g.last = &last
	g.lastAt = g.now()
	g.flight = nil
	g.mu.Unlock()

	close(f.done)
	return f.outcome, true
}

// runChecks probes every dependency concurrently under one deadline and
// builds the response body.
//
// The deadline is measured from here rather than inherited, and the
// cancellation of the triggering request is deliberately dropped: see the
// type's doc comment for why the leader must not be cancellable by the
// caller that happened to start it.
func (g *readinessGate) runChecks(ctx context.Context) readinessOutcome {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), g.timeout)
	defer cancel()

	results := make([]error, len(g.checks))
	var wg sync.WaitGroup
	for i, check := range g.checks {
		wg.Add(1)
		go func(i int, check ReadinessCheck) {
			defer wg.Done()
			results[i] = check.Probe(checkCtx)
		}(i, check)
	}
	wg.Wait()

	out := readinessOutcome{
		body:   healthResponse{Status: "ready", Checks: make(map[string]string, len(g.checks))},
		status: http.StatusOK,
	}
	for i, check := range g.checks {
		if results[i] != nil {
			out.body.Checks[check.Name] = "failed"
			out.body.Status = "not ready"
			out.status = http.StatusServiceUnavailable
			// Logged here rather than per request, which is the one
			// behavior change a reader might not expect: under load the
			// log now carries one line per real check round instead of one
			// per request. That is the point. A failing dependency used to
			// turn an unauthenticated request flood into an unbounded log
			// flood on the same machine that was already struggling.
			g.logger.LogAttrs(checkCtx, slog.LevelWarn, "readiness check failed",
				slog.String("check", check.Name),
				slog.String("error", results[i].Error()),
			)
			continue
		}
		out.body.Checks[check.Name] = "ok"
	}
	return out
}

// readinessMinInterval resolves the configured minimum interval, taking
// this package's default when a caller left the field zero.
//
// A separate function rather than a branch inside newReadinessGate because
// zero has to mean "unset, take the default" at the CONFIGURATION boundary
// while still meaning "no interval at all" to the gate itself, which is
// what the tests need in order to exercise the collapse behavior on its own
// without waiting out a real second.
func readinessMinInterval(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultReadinessMinInterval
	}
	return configured
}
