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

// recordingLauncher stands in for api.Dispatcher, recording what it was
// asked to launch. It is a test double for the DISPATCH side only; the
// store underneath these tests is the real ent one, so every claim, skip
// and cursor assertion still goes through a real database and a real unique
// index. Faking the store instead would make the duplicate-fire assertions
// meaningless, since the guard lives in the schema.
type recordingLauncher struct {
	mu       sync.Mutex
	launches []launchCall
	err      error
	delay    time.Duration
}

type launchCall struct {
	actor         string
	launchableID  int
	targetType    string
	savedConfigID int
}

func (l *recordingLauncher) Launch(_ context.Context, req launchable.Request) (launchable.Launched, error) {
	if l.delay > 0 {
		time.Sleep(l.delay)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return launchable.Launched{}, l.err
	}
	l.launches = append(l.launches, launchCall{
		actor:         req.Actor,
		launchableID:  req.Target.ID,
		targetType:    req.Target.Type,
		savedConfigID: req.SavedConfigID,
	})
	return launchable.Launched{
		RunID:          fmt.Sprintf("job-%d", len(l.launches)),
		UnifiedJobType: launchable.UnifiedJobJob,
	}, nil
}

func (l *recordingLauncher) calls() []launchCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]launchCall(nil), l.launches...)
}

// dueSchedule creates an hourly schedule whose next_run is already in the
// past by the given amount, simulating time having elapsed unattended.
func (f storeFixture) overdueHourly(t *testing.T, name string, lastFired, dueAt time.Time) schedule.Schedule {
	t.Helper()
	ctx := context.Background()

	s := schedule.Schedule{
		Name:         name,
		LaunchableID: f.launchableA,
		Enabled:      true,
		RRule:        "FREQ=HOURLY",
		Timezone:     "UTC",
		DTStart:      lastFired,
	}
	created, err := f.store.Create(ctx, s, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkFired(ctx, created.ScheduleID, lastFired, &dueAt); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Get(ctx, f.orgA, created.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestSweepCoalescesMissedRuns is the missed-run policy asserted as
// concretely as the daylight-saving gate: after an outage, exactly one job
// runs and every occurrence that did not is a durable, readable row.
//
// This is the case the design exists for. An hourly schedule that missed
// five hours must not launch five jobs at recovery -- that is a thundering
// herd against devices that rate-limit -- and must not silently pretend
// nothing was missed either.
func TestSweepCoalescesMissedRuns(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	// The schedule last ran at 09:00 and the controller was away until
	// 14:02, so 10:00 through 14:00 all passed unrun.
	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	recovery := time.Date(2024, 6, 1, 14, 2, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return recovery }))

	scanner.Sweep(ctx)

	// Exactly one job, for the most recent missed occurrence.
	calls := launcher.calls()
	if len(calls) != 1 {
		t.Fatalf("recovery launched %d jobs, want exactly 1", len(calls))
	}
	if calls[0].launchableID != f.launchableA {
		t.Errorf("launched launchable %d, want %d", calls[0].launchableID, f.launchableA)
	}
	if want := schedule.ScheduleActor(sched.ScheduleID); calls[0].actor != want {
		t.Errorf("actor = %q, want %q so the activity stream names the schedule", calls[0].actor, want)
	}

	history, err := f.store.ListOccurrences(ctx, f.orgA, sched.ScheduleID, 100)
	if err != nil {
		t.Fatal(err)
	}

	var fired, skipped []schedule.Occurrence
	for _, o := range history {
		switch o.Outcome {
		case schedule.OutcomeFired:
			fired = append(fired, o)
		case schedule.OutcomeSkipped:
			skipped = append(skipped, o)
		default:
			t.Errorf("occurrence %s left in state %q", o.OccurrenceAt, o.Outcome)
		}
	}

	if len(fired) != 1 {
		t.Fatalf("%d occurrences recorded as fired, want 1", len(fired))
	}
	want := time.Date(2024, 6, 1, 14, 0, 0, 0, time.UTC)
	if !fired[0].OccurrenceAt.Equal(want) {
		t.Errorf("fired occurrence = %s, want the most recent missed one %s", fired[0].OccurrenceAt, want)
	}
	if fired[0].JobID == "" {
		t.Error("the fired occurrence records no job id")
	}

	// 10:00, 11:00, 12:00, 13:00 -- everything before the one that ran.
	if len(skipped) != 4 {
		t.Fatalf("%d occurrences recorded as skipped, want 4", len(skipped))
	}
	for _, o := range skipped {
		if o.Reason != schedule.ReasonMissedWindow {
			t.Errorf("skipped occurrence %s carries reason %q, want %q",
				o.OccurrenceAt, o.Reason, schedule.ReasonMissedWindow)
		}
		if !o.OccurrenceAt.Before(want) {
			t.Errorf("skipped occurrence %s is not before the one that ran", o.OccurrenceAt)
		}
	}
}

// TestSweepAdvancesNextRun proves the scan converges: a schedule that has
// just fired must not be selected again on the following tick.
func TestSweepAdvancesNextRun(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }))

	scanner.Sweep(ctx)
	if got := len(launcher.calls()); got != 1 {
		t.Fatalf("first sweep launched %d jobs, want 1", got)
	}

	// A second sweep at the same instant must find nothing.
	scanner.Sweep(ctx)
	if got := len(launcher.calls()); got != 1 {
		t.Fatalf("a second sweep at the same instant launched again (%d total)", got)
	}

	after, err := f.store.Get(ctx, f.orgA, sched.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.NextRun == nil {
		t.Fatal("next run was cleared on an enabled, unbounded schedule")
	}
	if !after.NextRun.After(now) {
		t.Errorf("NextRun = %s, want a time after the sweep at %s", after.NextRun, now)
	}
	if after.LastFired == nil || !after.LastFired.Equal(time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("LastFired = %v, want the occurrence that actually ran", after.LastFired)
	}
}

// TestConcurrentSweepsFireOnce is the duplicate-fire gate: several
// controllers sweeping the same due schedule at the same instant -- which
// is exactly what a leader-election failover permits, since the lease has
// no fencing token -- must together produce exactly one job.
func TestConcurrentSweepsFireOnce(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	// One shared launcher so every replica's launches land in one tally,
	// with a delay that widens the window between claim and launch.
	launcher := &recordingLauncher{delay: 5 * time.Millisecond}

	const replicas = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := schedule.NewScanner(f.store, launcher,
				schedule.WithClock(func() time.Time { return now }))
			<-start
			scanner.Sweep(ctx)
		}()
	}
	close(start)
	wg.Wait()

	if got := len(launcher.calls()); got != 1 {
		t.Fatalf("%d replicas sweeping the same due schedule launched %d jobs, want exactly 1", replicas, got)
	}

	// One job is necessary but not sufficient: the test would also pass if
	// seven replicas had crashed on lock contention before reaching the
	// claim, which is how this test read before the fixture moved to a
	// WAL file. Assert the guard actually adjudicated, by checking the
	// occurrence exists exactly once and is the one that fired.
	history, err := f.store.ListOccurrences(ctx, f.orgA, sched.ScheduleID, 50)
	if err != nil {
		t.Fatal(err)
	}
	fired := 0
	for _, o := range history {
		if o.Outcome == schedule.OutcomeFired {
			fired++
		}
		if o.Outcome == schedule.OutcomeClaimed {
			t.Errorf("occurrence %s was left claimed but never resolved", o.OccurrenceAt)
		}
	}
	if fired != 1 {
		t.Errorf("%d occurrences recorded as fired, want exactly 1", fired)
	}
}

// TestSweepTruncatesAnEnormousBacklog proves the skip-row cap holds and,
// crucially, that the truncation is RECORDED rather than silent.
func TestSweepTruncatesAnEnormousBacklog(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	// Two weeks of an hourly schedule: 336 occurrences.
	lastFired := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	now := lastFired.Add(14 * 24 * time.Hour)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }),
		schedule.WithMaxSkipRecords(10))

	scanner.Sweep(ctx)

	if got := len(launcher.calls()); got != 1 {
		t.Fatalf("launched %d jobs after a two-week backlog, want 1", got)
	}

	history, err := f.store.ListOccurrences(ctx, f.orgA, sched.ScheduleID, 500)
	if err != nil {
		t.Fatal(err)
	}

	var truncated *schedule.Occurrence
	individual := 0
	for i := range history {
		switch history[i].Reason {
		case schedule.ReasonMissedWindow:
			individual++
		case schedule.ReasonMissedWindowTruncated:
			truncated = &history[i]
		}
	}

	if individual != 10 {
		t.Errorf("%d individual skip rows written, want the cap of 10", individual)
	}
	if truncated == nil {
		t.Fatal("the backlog was capped with no record that anything was dropped")
	}
	if truncated.SuppressedCount == 0 {
		t.Error("the truncation row carries no count, so the cap is effectively silent")
	}
	if want := 335 - 10; truncated.SuppressedCount != want {
		t.Errorf("suppressed count = %d, want %d", truncated.SuppressedCount, want)
	}
}

// TestSweepRecordsAFailedLaunch proves a launch failure leaves an honest
// row rather than a claim stuck in limbo, and does not disable the
// schedule: the next occurrence is a fresh attempt.
func TestSweepRecordsAFailedLaunch(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	sched := f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{err: errors.New("dispatcher is not wired")}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }))

	scanner.Sweep(ctx)

	history, err := f.store.ListOccurrences(ctx, f.orgA, sched.ScheduleID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history has %d rows, want 1", len(history))
	}
	if history[0].Outcome != schedule.OutcomeSkipped {
		t.Errorf("outcome = %q, want %q", history[0].Outcome, schedule.OutcomeSkipped)
	}
	if history[0].Reason != schedule.ReasonLaunchFailed {
		t.Errorf("reason = %q, want %q", history[0].Reason, schedule.ReasonLaunchFailed)
	}

	after, err := f.store.Get(ctx, f.orgA, sched.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Enabled {
		t.Error("a failed launch disabled the schedule; a transient failure must not")
	}
}

// TestRunHonoursLeadership proves the loop does no work while another
// replica leads, which is what bounds query load across a deployment.
func TestRunHonoursLeadership(t *testing.T) {
	f := newStoreFixture(t)

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }),
		schedule.WithScanInterval(5*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	scanner.Run(ctx, func() bool { return false })

	if got := len(launcher.calls()); got != 0 {
		t.Errorf("a non-leading scanner launched %d jobs", got)
	}
}

// TestRunSweepsWhenLeading is the other half: a leading scanner does fire,
// so the leadership check cannot be satisfied by a loop that never works.
func TestRunSweepsWhenLeading(t *testing.T) {
	f := newStoreFixture(t)

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	f.overdueHourly(t, "health check", lastFired, lastFired.Add(time.Hour))

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }),
		schedule.WithScanInterval(5*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner.Run(ctx, func() bool { return true })
	}()

	deadline := time.After(time.Second)
	for len(launcher.calls()) == 0 {
		select {
		case <-deadline:
			t.Fatal("a leading scanner never launched anything")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// TestSweepPagesThroughMoreThanOnePage proves the scan does not stop at the
// first page. With the page size set to one and three schedules due, a
// scanner that ignored its cursor would fire once and leave two overdue
// forever.
func TestSweepPagesThroughMoreThanOnePage(t *testing.T) {
	f := newStoreFixture(t)

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		f.overdueHourly(t, fmt.Sprintf("check-%d", i), lastFired, lastFired.Add(time.Hour))
	}

	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }),
		schedule.WithDuePageSize(1),
		schedule.WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil))))

	scanner.Sweep(context.Background())

	if got := len(launcher.calls()); got != 3 {
		t.Errorf("a page size of 1 over 3 due schedules launched %d jobs, want 3", got)
	}
}

// TestSweepRepairsAStaleNextRun covers the branch where the cached next_run
// says a schedule is due but the rule no longer produces anything then --
// the state an edit leaves behind. The scan must recompute and stop
// selecting it, not spin on it every tick.
func TestSweepRepairsAStaleNextRun(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	// A schedule whose recurrence is entirely in the future, but whose
	// cached next_run has been left in the past.
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	created, err := f.store.Create(ctx, schedule.Schedule{
		Name:         "future",
		LaunchableID: f.launchableA,
		Enabled:      true,
		RRule:        "FREQ=DAILY",
		Timezone:     "UTC",
		DTStart:      future,
	}, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	if err := f.store.MarkFired(ctx, created.ScheduleID, time.Time{}, &stale); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }))

	scanner.Sweep(ctx)

	if got := len(launcher.calls()); got != 0 {
		t.Errorf("a schedule whose rule produces nothing yet launched %d jobs", got)
	}
	after, err := f.store.Get(ctx, f.orgA, created.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.NextRun == nil {
		t.Fatal("the stale next run was cleared rather than repaired")
	}
	if !after.NextRun.After(now) {
		t.Errorf("next run = %s, want it repaired to a time after %s", after.NextRun, now)
	}
}

// TestSweepStopsAtDTEnd proves the outer bound is honoured by the scan and
// not only by next_run: a schedule past its end must not run even if its
// cached next_run still says it should.
func TestSweepStopsAtDTEnd(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	dtstart := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	end := dtstart.Add(2 * time.Hour)
	created, err := f.store.Create(ctx, schedule.Schedule{
		Name:         "bounded",
		LaunchableID: f.launchableA,
		Enabled:      true,
		RRule:        "FREQ=HOURLY",
		Timezone:     "UTC",
		DTStart:      dtstart,
		DTEnd:        &end,
	}, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	// Pretend it last ran at its end, and force it back into the due set.
	if err := f.store.MarkFired(ctx, created.ScheduleID, end, &end); err != nil {
		t.Fatal(err)
	}

	now := dtstart.Add(48 * time.Hour)
	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }))

	scanner.Sweep(ctx)

	if got := len(launcher.calls()); got != 0 {
		t.Errorf("a schedule past its dtend launched %d jobs", got)
	}
	after, err := f.store.Get(ctx, f.orgA, created.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.NextRun != nil {
		t.Errorf("a schedule past its dtend still advertises next run %s", after.NextRun)
	}
}

// TestSweepCarriesTheSavedConfiguration proves the bundle a schedule points
// at reaches the launch, and covers the SavedConfig edge on the read path.
func TestSweepCarriesTheSavedConfiguration(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	cfg, err := f.client.SavedLaunchConfig.Create().
		SetName("core only").
		SetFields(map[string]any{"limit": "core-*"}).
		// A saved configuration belongs to a TEMPLATE, not to a launchable:
		// the overrides it holds are keyed by that template's own promptable
		// fields.
		SetTemplateID(f.templateA).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	lastFired := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	created, err := f.store.Create(ctx, schedule.Schedule{
		Name:          "with config",
		LaunchableID:  f.launchableA,
		SavedConfigID: cfg.ID,
		Enabled:       true,
		RRule:         "FREQ=HOURLY",
		Timezone:      "UTC",
		DTStart:       lastFired,
	}, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if created.SavedConfigID != cfg.ID {
		t.Fatalf("the saved configuration did not survive the round trip: %+v", created)
	}
	if err := f.store.MarkFired(ctx, created.ScheduleID, lastFired, timePtr(lastFired.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2024, 6, 1, 10, 30, 0, 0, time.UTC)
	launcher := &recordingLauncher{}
	scanner := schedule.NewScanner(f.store, launcher,
		schedule.WithClock(func() time.Time { return now }))
	scanner.Sweep(ctx)

	calls := launcher.calls()
	if len(calls) != 1 {
		t.Fatalf("launched %d jobs, want 1", len(calls))
	}
	if calls[0].savedConfigID != cfg.ID {
		t.Errorf("launched with saved config %d, want %d", calls[0].savedConfigID, cfg.ID)
	}
}

// TestUpdateClearsASavedConfiguration covers the other half of that edge.
func TestUpdateClearsASavedConfiguration(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	cfg, err := f.client.SavedLaunchConfig.Create().
		SetName("core only").
		SetFields(map[string]any{"limit": "core-*"}).
		// A saved configuration belongs to a TEMPLATE, not to a launchable:
		// the overrides it holds are keyed by that template's own promptable
		// fields.
		SetTemplateID(f.templateA).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s := f.newSchedule("nightly")
	s.SavedConfigID = cfg.ID
	created, err := f.store.Create(ctx, s, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}

	created.SavedConfigID = 0
	updated, err := f.store.Update(ctx, created, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if updated.SavedConfigID != 0 {
		t.Errorf("saved config = %d, want it cleared", updated.SavedConfigID)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
