// These tests hold the scanner to a promise its comment made and no test
// checked: a launch that fails after its occurrence was claimed is tried
// again on the next sweep (FAILURE_PATTERNS 1225). They run the real ent
// store on real SQLite, as scanner_test.go does, because the claim that
// decides them is the database's unique index.
package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// flakyLauncher fails its first failFirst launches with err, then succeeds,
// counting every attempt and recording every launch that succeeded.
type flakyLauncher struct {
	mu        sync.Mutex
	failFirst int
	err       error
	delay     time.Duration
	attempts  int
	launched  int
}

// Launch implements schedule.Launcher.
func (l *flakyLauncher) Launch(_ context.Context, _ launchable.Request) (launchable.Launched, error) {
	if l.delay > 0 {
		time.Sleep(l.delay)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts++
	if l.attempts <= l.failFirst {
		return launchable.Launched{}, l.err
	}
	l.launched++
	return launchable.Launched{RunID: fmt.Sprintf("job-%d", l.launched), UnifiedJobType: launchable.UnifiedJobJob}, nil
}

// counts returns the attempts and the launches that succeeded.
func (l *flakyLauncher) counts() (attempts, launched int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attempts, l.launched
}

// quietLogger discards the scanner's log lines, which these tests do not read.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// occurrenceAt returns the history row for one occurrence, failing the test
// when there is not exactly one.
func occurrenceAt(t *testing.T, f storeFixture, scheduleID string, at time.Time) schedule.Occurrence {
	t.Helper()
	history, err := f.store.ListOccurrences(context.Background(), f.orgA, scheduleID, 500)
	if err != nil {
		t.Fatal(err)
	}
	var found []schedule.Occurrence
	for _, o := range history {
		if o.OccurrenceAt.Equal(at) {
			found = append(found, o)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d history rows for occurrence %s, want 1", len(found), at)
	}
	return found[0]
}

// TestSweepRetriesAnOccurrenceWhoseLaunchFailed is FAILURE_PATTERNS 1225's
// test: a launch that fails once and then succeeds, across two sweeps, gives
// that occurrence exactly one launch. Before the fix the second sweep found
// the occurrence already claimed, read that as done, and advanced past it,
// so the run was lost though Sweep's comment promised one interval of
// lateness.
func TestSweepRetriesAnOccurrenceWhoseLaunchFailed(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	target := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &flakyLauncher{failFirst: 1, err: errors.New("the dispatcher's bus is down")}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }), schedule.WithLogger(quietLogger()))

	scanner.Sweep(ctx)
	if attempts, launched := launcher.counts(); attempts != 1 || launched != 0 {
		t.Fatalf("after the first sweep: %d attempts, %d launches; want 1 failed attempt", attempts, launched)
	}
	if o := occurrenceAt(t, f, sched.ScheduleID, target); o.Outcome != schedule.OutcomeSkipped || o.Reason != schedule.ReasonLaunchFailed {
		t.Errorf("after the failure the occurrence reads %s/%s, want skipped/%s", o.Outcome, o.Reason, schedule.ReasonLaunchFailed)
	}

	scanner.Sweep(ctx)
	if attempts, launched := launcher.counts(); attempts != 2 || launched != 1 {
		t.Fatalf("after the second sweep: %d attempts, %d launches; want the occurrence launched once on its retry", attempts, launched)
	}
	o := occurrenceAt(t, f, sched.ScheduleID, target)
	if o.Outcome != schedule.OutcomeFired || o.JobID == "" || o.Reason != "" {
		t.Errorf("after the retry the occurrence reads %s, reason %q, job %q; want fired with a job and no reason", o.Outcome, o.Reason, o.JobID)
	}

	// The schedule moved on, so a third sweep launches nothing more.
	scanner.Sweep(ctx)
	if _, launched := launcher.counts(); launched != 1 {
		t.Errorf("a third sweep launched again (%d launches in all)", launched)
	}
	after, err := f.store.Get(ctx, f.orgA, sched.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastFired == nil || !after.LastFired.Equal(target) {
		t.Errorf("LastFired = %v, want the occurrence that finally ran, %s", after.LastFired, target)
	}
}

// TestSweepDoesNotRetryARefusedLaunch: a refusal (the template is gone, or
// it needs a value nobody is there to give) can only fail again, so it is
// recorded and the schedule moves on at once rather than retrying every
// sweep.
func TestSweepDoesNotRetryARefusedLaunch(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	target := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &flakyLauncher{failFirst: 100, err: launchable.Refusal{Field: launchable.TargetField, Message: "That template no longer exists."}}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }), schedule.WithLogger(quietLogger()))

	scanner.Sweep(ctx)
	scanner.Sweep(ctx)
	if attempts, _ := launcher.counts(); attempts != 1 {
		t.Errorf("a refused launch was attempted %d times over two sweeps, want 1", attempts)
	}
	if o := occurrenceAt(t, f, sched.ScheduleID, target); o.Outcome != schedule.OutcomeSkipped || o.Reason != schedule.ReasonLaunchFailed {
		t.Errorf("the refused occurrence reads %s/%s, want skipped/%s", o.Outcome, o.Reason, schedule.ReasonLaunchFailed)
	}
	after, err := f.store.Get(ctx, f.orgA, sched.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastFired == nil || !after.LastFired.Equal(target) {
		t.Errorf("LastFired = %v, want the schedule advanced past the refused occurrence %s", after.LastFired, target)
	}
}

// TestConcurrentSweepsRetryAFailedLaunchOnce: the retry keeps the
// duplicate-fire guarantee. After a failed launch, eight replicas sweeping
// together re-claim the occurrence once between them and launch it once.
func TestConcurrentSweepsRetryAFailedLaunchOnce(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	target := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	// A delay widens the window between the re-claim and the launch.
	launcher := &flakyLauncher{failFirst: 1, err: errors.New("the dispatcher's bus is down"), delay: 5 * time.Millisecond}
	clock := schedule.WithClock(func() time.Time { return now })
	schedule.NewScanner(f.store, launcher, clock, schedule.WithLogger(quietLogger())).Sweep(ctx)

	const replicas = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := schedule.NewScanner(f.store, launcher, clock, schedule.WithLogger(quietLogger()))
			<-start
			scanner.Sweep(ctx)
		}()
	}
	close(start)
	wg.Wait()

	if _, launched := launcher.counts(); launched != 1 {
		t.Fatalf("%d replicas retrying one failed occurrence launched it %d times, want once", replicas, launched)
	}
	if o := occurrenceAt(t, f, sched.ScheduleID, target); o.Outcome != schedule.OutcomeFired {
		t.Errorf("the retried occurrence reads %s, want fired", o.Outcome)
	}
}

// TestClaimOccurrenceReclaimsOnlyAFailedLaunch is the store half of the
// retry: an occurrence whose launch failed can be claimed again, exactly
// once, and no other row can be.
func TestClaimOccurrenceReclaimsOnlyAFailedLaunch(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()
	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	id := created.ScheduleID
	at := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)

	first, err := f.store.ClaimOccurrence(ctx, id, at)
	if err != nil {
		t.Fatal(err)
	}
	// Held: a claim still being launched is never taken over.
	if _, err := f.store.ClaimOccurrence(ctx, id, at); !errors.Is(err, schedule.ErrAlreadyClaimed) {
		t.Fatalf("claiming a held occurrence = %v, want ErrAlreadyClaimed", err)
	}
	if err := f.store.ResolveOccurrence(ctx, first.ID, schedule.OutcomeSkipped, schedule.ReasonLaunchFailed, launchable.Launched{}); err != nil {
		t.Fatal(err)
	}

	again, err := f.store.ClaimOccurrence(ctx, id, at)
	if err != nil {
		t.Fatalf("re-claiming an occurrence whose launch failed: %v", err)
	}
	if again.ID != first.ID || again.Outcome != schedule.OutcomeClaimed || again.Reason != "" {
		t.Errorf("re-claim = %+v, want row %d back in the claimed state with no reason", again, first.ID)
	}
	if _, err := f.store.ClaimOccurrence(ctx, id, at); !errors.Is(err, schedule.ErrAlreadyClaimed) {
		t.Fatalf("a second re-claim = %v, want ErrAlreadyClaimed", err)
	}

	// No other outcome is re-claimable: a fired one, a collision with a run
	// in flight, and a missed one.
	if err := f.store.ResolveOccurrence(ctx, again.ID, schedule.OutcomeFired, "", launchable.Launched{RunID: "job-1", UnifiedJobType: launchable.UnifiedJobJob}); err != nil {
		t.Fatal(err)
	}
	busy, err := f.store.ClaimOccurrence(ctx, id, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ResolveOccurrence(ctx, busy.ID, schedule.OutcomeSkipped, schedule.ReasonAlreadyRunning, launchable.Launched{}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordSkip(ctx, id, at.Add(2*time.Hour), schedule.ReasonMissedWindow, 0); err != nil {
		t.Fatal(err)
	}
	for _, settled := range []time.Time{at, at.Add(time.Hour), at.Add(2 * time.Hour)} {
		if _, err := f.store.ClaimOccurrence(ctx, id, settled); !errors.Is(err, schedule.ErrAlreadyClaimed) {
			t.Errorf("claiming the settled occurrence at %s = %v, want ErrAlreadyClaimed", settled, err)
		}
	}
}
