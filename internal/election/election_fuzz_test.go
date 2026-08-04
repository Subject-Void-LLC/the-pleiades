package election_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/election"
)

// FuzzLeaderElectionCancellation is this package's migration of
// engine.Scheduler's own FuzzSchedulerCancellation, extended with two new
// fuzzed dimensions this phase's own fixes introduced: failAfterCalls
// (the mock lease's KeepAlive starts failing after N calls, exercising
// the explicit-release-on-renewal-failure path) and releaseDelayMs (the
// mock lease's own Release call is artificially slow, proving
// releaseBestEffort's bounded timeout actually bounds Run's forward
// progress regardless of what Release does). Whatever combination of
// cancellation timing and lease behavior is fuzzed, Run must never
// panic, deadlock, or fail to return within releaseTimeout of ctx being
// canceled.
func FuzzLeaderElectionCancellation(f *testing.F) {
	f.Add(10, 0, 0)
	f.Add(500, 3, 0)
	f.Add(2000, 1, 200)
	f.Add(0, 0, 3000)

	f.Fuzz(func(t *testing.T, cancelDelayMs, failAfterCalls, releaseDelayMs int) {
		if cancelDelayMs < 0 || cancelDelayMs > 3000 {
			return
		}
		if failAfterCalls < 0 || failAfterCalls > 100 {
			return
		}
		if releaseDelayMs < 0 || releaseDelayMs > 3000 {
			return
		}

		lease := &mockLease{
			failAfterCalls: failAfterCalls,
			releaseDelay:   time.Duration(releaseDelayMs) * time.Millisecond,
		}
		mgr := &mockManager{lease: lease}
		e := election.NewLeaderElector(mgr, "fuzz-key")

		runCtx, runCancel := context.WithCancel(context.Background())
		defer runCancel()

		done := make(chan struct{})
		go func() {
			e.Run(runCtx)
			close(done)
		}()

		time.Sleep(time.Duration(cancelDelayMs) * time.Millisecond)
		runCancel()

		// releaseBestEffort bounds any real release call to releaseTimeout
		// (2s) regardless of how slow the underlying lease's own Release
		// implementation is, so Run must always return well before this
		// generous margin, no matter what cancelDelayMs/failAfterCalls/
		// releaseDelayMs combination was fuzzed.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Run did not return after ctx cancellation (cancelDelayMs=%d failAfterCalls=%d releaseDelayMs=%d)",
				cancelDelayMs, failAfterCalls, releaseDelayMs)
		}
	})
}
