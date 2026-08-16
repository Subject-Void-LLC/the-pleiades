package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for the bound on /readyz. The claim under test is not "the probe
// returns the right answer", which the handler tests already cover, but
// "what this endpoint costs does not scale with how often it is asked",
// which is a property of the gate and can only be observed by counting real
// check rounds.

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countingCheck is one dependency whose probe counts its calls and can be
// held open, so a test can decide exactly when a round finishes.
type countingCheck struct {
	calls   atomic.Int64
	release chan struct{}
	fail    atomic.Bool
}

func (c *countingCheck) asCheck(name string) ReadinessCheck {
	return ReadinessCheck{
		Name: name,
		Probe: func(ctx context.Context) error {
			c.calls.Add(1)
			if c.release != nil {
				select {
				case <-c.release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if c.fail.Load() {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
}

// TestConcurrentProbesCollapseOntoOneCheck is the burst half of the bound.
//
// Two hundred callers arrive while one round is in flight. Exactly one real
// check must run: that is the difference between work proportional to
// concurrency, which is the quantity an attacker chooses, and work
// proportional to time, which is the quantity this process chooses.
func TestConcurrentProbesCollapseOntoOneCheck(t *testing.T) {
	check := &countingCheck{release: make(chan struct{})}
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, time.Second, 0)

	const callers = 200
	var started, finished sync.WaitGroup
	started.Add(callers)
	finished.Add(callers)
	outcomes := make([]readinessOutcome, callers)

	for i := range callers {
		go func(i int) {
			defer finished.Done()
			started.Done()
			outcomes[i], _ = gate.outcome(context.Background())
		}(i)
	}
	started.Wait()

	// Goroutine startup is not arrival. Wait until one caller is leading a
	// real check and every other caller has reached the decision point and
	// joined it; only then is "no second check ran" a claim about the gate
	// rather than about which goroutine the scheduler happened to run.
	waitFor(t, func() bool { return check.calls.Load() >= 1 }, "the first check to start")
	waitFor(t, func() bool { return gate.joinedCount() == callers-1 }, "every other caller to join the round")
	close(check.release)
	finished.Wait()

	if got := check.calls.Load(); got != 1 {
		t.Fatalf("%d callers caused %d real checks, want exactly 1: the endpoint's cost still scales with how often it is asked", callers, got)
	}
	for i, out := range outcomes {
		if out.status != http.StatusOK {
			t.Fatalf("caller %d got status %d, want %d: a collapsed caller must get the leader's real answer", i, out.status, http.StatusOK)
		}
	}
}

// TestAFreshAnswerIsReusedUntilTheIntervalElapses is the sustained half.
//
// Collapse alone still admits a new round the instant the previous one
// finishes, so a serial caller at any rate drives one check per round trip.
// The minimum interval is what turns that into one per interval.
func TestAFreshAnswerIsReusedUntilTheIntervalElapses(t *testing.T) {
	check := &countingCheck{}
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, time.Second, time.Second)

	// A clock the test moves, so this asserts the interval rather than
	// asserting that a sleep was long enough.
	now := time.Now()
	gate.now = func() time.Time { return now }

	for range 50 {
		if _, ok := gate.outcome(context.Background()); !ok {
			t.Fatal("the gate reported no answer for a caller whose context was live")
		}
	}
	if got := check.calls.Load(); got != 1 {
		t.Fatalf("50 sequential probes inside one interval caused %d real checks, want 1", got)
	}

	// One nanosecond short of the interval is still inside it.
	now = now.Add(time.Second - time.Nanosecond)
	gate.outcome(context.Background())
	if got := check.calls.Load(); got != 1 {
		t.Fatalf("a probe just inside the interval caused a second check (%d total)", got)
	}

	// And at the interval, a real check runs again, so a legitimate probe
	// at a longer period than this always gets a fresh answer.
	now = now.Add(time.Nanosecond)
	gate.outcome(context.Background())
	if got := check.calls.Load(); got != 2 {
		t.Fatalf("a probe at the interval boundary got a stale answer (%d checks): the bound must not outlive the interval it promises", got)
	}
}

// TestTheDefaultIntervalNeverAnswersAShippedProbeFromCache is the guard on
// the number itself, and it is written against the FASTEST cadence this
// repository ships rather than the most common one.
//
// That distinction is the mistake this test exists to prevent recurring.
// Anchoring on the 5 second steady-state period admits an interval of a
// second, which silently hands three of every four of compose's 250ms
// startup probes a cached answer and gives back a quarter of the warm start
// the packaging work bought. Nothing else in the tree would have failed.
//
// The value is asserted against docker-compose.yml directly rather than
// against a constant copied into this file, because a copy is exactly what
// goes stale when somebody tunes the compose file.
func TestTheDefaultIntervalNeverAnswersAShippedProbeFromCache(t *testing.T) {
	fastest := fastestShippedProbeInterval(t)
	if defaultReadinessMinInterval > fastest {
		t.Fatalf("defaultReadinessMinInterval is %s but the fastest probe this repository ships runs every %s; "+
			"at that gap a legitimate probe is answered from cache, so the controller reports state it has "+
			"stopped measuring and the startup path pays for it first",
			defaultReadinessMinInterval, fastest)
	}
}

// fastestShippedProbeInterval reads every probe cadence out of
// docker-compose.yml and returns the shortest.
//
// It parses the file rather than trusting a constant here, so tuning the
// compose file either keeps this bound honest or fails this test.
func fastestShippedProbeInterval(t *testing.T) time.Duration {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRootForTest(t), "docker-compose.yml"))
	if err != nil {
		t.Fatalf("reading docker-compose.yml: %v", err)
	}

	// interval:, start_interval: and start_period: are the three cadence
	// keys compose defines; only the first two decide how often a probe
	// actually runs.
	pattern := regexp.MustCompile(`(?m)^\s*(interval|start_interval):\s*(\S+)\s*$`)
	matches := pattern.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatal("no healthcheck intervals found in docker-compose.yml; this guard is measuring nothing")
	}

	fastest := time.Duration(0)
	for _, m := range matches {
		d, err := time.ParseDuration(m[2])
		if err != nil {
			t.Fatalf("compose declares %s: %q, which is not a duration this guard can read: %v", m[1], m[2], err)
		}
		if fastest == 0 || d < fastest {
			fastest = d
		}
	}
	t.Logf("fastest probe cadence in docker-compose.yml: %s (from %d declared intervals)", fastest, len(matches))
	return fastest
}

// repoRootForTest walks up from the working directory to the module root.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// TestAFailingDependencyIsAlsoBounded is the case that matters most under
// load and is easiest to leave out.
//
// Caching only successes looks conservative and is backwards: a broken
// dependency is exactly when an unbounded probe does the most damage,
// because every caller then piles onto a database that is already failing.
func TestAFailingDependencyIsAlsoBounded(t *testing.T) {
	check := &countingCheck{}
	check.fail.Store(true)
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, time.Second, time.Second)

	now := time.Now()
	gate.now = func() time.Time { return now }

	for range 20 {
		out, ok := gate.outcome(context.Background())
		if !ok {
			t.Fatal("no answer for a live caller")
		}
		if out.status != http.StatusServiceUnavailable {
			t.Fatalf("status %d, want %d while the dependency is failing", out.status, http.StatusServiceUnavailable)
		}
		if out.body.Checks["database"] != "failed" {
			t.Fatalf("checks reported %v, want database failed", out.body.Checks)
		}
	}
	if got := check.calls.Load(); got != 1 {
		t.Fatalf("20 probes against a failing dependency caused %d real checks, want 1: an unbounded probe "+
			"hammers hardest exactly when the dependency is already broken", got)
	}

	// And recovery is still bounded by the interval, not deferred forever.
	check.fail.Store(false)
	now = now.Add(time.Second)
	out, _ := gate.outcome(context.Background())
	if out.status != http.StatusOK {
		t.Fatalf("status %d after recovery and one interval, want %d", out.status, http.StatusOK)
	}
}

// TestALeaderThatDisconnectsDoesNotAbortEverybodyElse is this phase's own
// recurring lesson, applied here.
//
// Three certificate-provisioning designs were torn out for making one
// participant's bad state everybody else's problem. A shared check round
// cancelled by whichever caller happened to arrive first is the same shape:
// the leader hangs up, and every waiter reads a context error as if the
// database were down.
func TestALeaderThatDisconnectsDoesNotAbortEverybodyElse(t *testing.T) {
	check := &countingCheck{release: make(chan struct{})}
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, 5*time.Second, 0)

	leaderCtx, disconnectLeader := context.WithCancel(context.Background())
	leaderDone := make(chan readinessOutcome, 1)
	go func() {
		out, _ := gate.outcome(leaderCtx)
		leaderDone <- out
	}()
	waitFor(t, func() bool { return check.calls.Load() == 1 }, "the leader's check to start")

	waiterDone := make(chan readinessOutcome, 1)
	go func() {
		out, _ := gate.outcome(context.Background())
		waiterDone <- out
	}()
	waitFor(t, func() bool { return gate.joinedCount() == 1 }, "the waiter to join the leader's round")

	// The leader hangs up mid-flight. The check must survive it.
	disconnectLeader()
	close(check.release)

	select {
	case out := <-waiterDone:
		if out.status != http.StatusOK {
			t.Fatalf("the waiter got status %d after the LEADER disconnected, want %d: one caller's "+
				"departure must not become every other caller's failed readiness check", out.status, http.StatusOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never got an answer after the leader disconnected")
	}
	if got := check.calls.Load(); got != 1 {
		t.Fatalf("%d real checks ran, want 1", got)
	}
}

// TestAWaiterThatDisconnectsGetsNoAnswerAndCostsNothing is the other side
// of that seam: a caller that goes away should stop waiting, and must not
// be reported as a readiness failure.
func TestAWaiterThatDisconnectsGetsNoAnswerAndCostsNothing(t *testing.T) {
	check := &countingCheck{release: make(chan struct{})}
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, 5*time.Second, 0)

	go gate.outcome(context.Background())
	waitFor(t, func() bool { return check.calls.Load() == 1 }, "the leader's check to start")

	waiterCtx, disconnectWaiter := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() {
		_, ok := gate.outcome(waiterCtx)
		result <- ok
	}()
	waitFor(t, func() bool { return gate.joinedCount() == 1 }, "the waiter to join the round")
	disconnectWaiter()

	select {
	case ok := <-result:
		if ok {
			t.Error("a caller that disconnected while waiting was handed an answer to write to a closed connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a disconnected waiter never returned; it is pinned for the whole check timeout")
	}
	close(check.release)
}

// TestReadyzServesTheGateThroughTheRealRouter is the end-to-end shape: the
// bound has to hold through the mounted endpoint, not only on the type.
func TestReadyzServesTheGateThroughTheRealRouter(t *testing.T) {
	check := &countingCheck{}
	router, err := NewRouter(RouterConfig{
		Logger:               quietLogger(),
		Readiness:            []ReadinessCheck{check.asCheck("database")},
		AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}

	const requests = 100
	for range requests {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want %d", rec.Code, http.StatusOK)
		}
	}

	if got := check.calls.Load(); got >= requests {
		t.Fatalf("%d requests through the real router caused %d real checks; the mounted endpoint is not bounded", requests, got)
	}
	t.Logf("%d requests through the mounted endpoint cost %d real check(s)", requests, check.calls.Load())
}

// waitFor blocks until cond is true, failing the test rather than hanging
// forever if it never becomes so.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestTheHandlerWritesNothingWhenTheCallerIsAlreadyGone covers readyzHandler's
// one early return.
//
// It matters more than a status code usually does. The gate reports "no
// answer" only for a caller that disappeared while waiting on somebody
// else's check round, and the handler's job there is to write NOTHING: a
// response written to a closed connection is at best wasted and at worst an
// error logged against a request that was fine. The assertion is therefore
// about the absence of a write, which is why an httptest.Recorder's
// untouched state is the thing being checked.
func TestTheHandlerWritesNothingWhenTheCallerIsAlreadyGone(t *testing.T) {
	check := &countingCheck{release: make(chan struct{})}
	gate := newReadinessGate(quietLogger(), []ReadinessCheck{check.asCheck("database")}, 5*time.Second, 0)
	handler := readyzHandler(gate)

	// Somebody else is mid-round, so the next caller must wait rather than
	// lead, which is the only path that can return no answer at all.
	go gate.outcome(context.Background())
	waitFor(t, func() bool { return check.calls.Load() == 1 }, "the leader's check to start")

	gone, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(gone))
	}()
	waitFor(t, func() bool { return gate.joinedCount() == 1 }, "the caller to join the round")
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never returned for a caller that had disconnected")
	}

	if rec.Body.Len() != 0 {
		t.Errorf("the handler wrote %q to a caller that had already gone away", rec.Body.String())
	}
	if rec.Flushed {
		t.Error("the handler flushed a response to a caller that had already gone away")
	}
	close(check.release)
}

// TestTheGateSubstitutesSafeValuesForUnusableOnes covers the constructor's
// defaulting, and the two arguments default differently on purpose.
//
// A non-positive TIMEOUT has no safe reading: a check with no deadline is
// the hang this endpoint exists to avoid, so it becomes the package
// default. A negative INTERVAL is a caller error and becomes zero rather
// than the package default, because zero is a value the tests here
// genuinely want and a negative one would silently behave as zero anyway;
// the configuration boundary is where "unset means default" is applied, in
// readinessMinInterval, and it is deliberately not applied twice.
func TestTheGateSubstitutesSafeValuesForUnusableOnes(t *testing.T) {
	g := newReadinessGate(quietLogger(), nil, 0, -time.Second)
	if g.timeout != readinessProbeTimeout {
		t.Errorf("timeout is %s for a zero argument, want the package default %s: a dependency check with no deadline is the hang a readiness probe must never become", g.timeout, readinessProbeTimeout)
	}
	if g.minInterval != 0 {
		t.Errorf("minInterval is %s for a negative argument, want 0", g.minInterval)
	}

	if got := readinessMinInterval(0); got != defaultReadinessMinInterval {
		t.Errorf("readinessMinInterval(0) = %s, want the default %s: an unset field must not leave /readyz unbounded", got, defaultReadinessMinInterval)
	}
	if got := readinessMinInterval(2 * time.Second); got != 2*time.Second {
		t.Errorf("readinessMinInterval(2s) = %s, want it left alone", got)
	}
}
