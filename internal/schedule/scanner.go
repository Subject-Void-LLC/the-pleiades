package schedule

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Defaults for a Scanner. They are exported so a composition root can state
// them explicitly rather than inherit them silently, the same way
// dispatch.DefaultFanOutLeaseTTL is passed to both the Worker and the
// Reaper that must agree on it.
const (
	// DefaultScanInterval is how often a leading Scanner looks for due
	// schedules.
	//
	// Thirty seconds is chosen against the finest recurrence this
	// scheduler supports. rrule refuses SECONDLY, so the shortest possible
	// gap between two occurrences is one minute; scanning twice per minute
	// bounds a schedule's lateness at half its own shortest period. A tick
	// that finds nothing costs one indexed query, which is the
	// overwhelmingly common case.
	DefaultScanInterval = 30 * time.Second

	// DefaultDuePageSize is how many due schedules one keyset page holds.
	DefaultDuePageSize = 100

	// DefaultMaxSkipRecords bounds how many individual skipped rows one
	// schedule's recovery may write before the rest are collapsed into a
	// single counted row.
	//
	// The number is set where it is because of what the extremes cost. A
	// one-minute schedule down for a year is over half a million missed
	// occurrences; writing a row each would turn a recovery into an outage
	// of its own. A thousand rows covers seventeen hours of a one-minute
	// schedule, or six weeks of an hourly one, which is longer than any
	// outage anybody intends to explain occurrence by occurrence.
	DefaultMaxSkipRecords = 1000

	// maxCatchUpExpansion bounds how many missed occurrences are expanded
	// per schedule per tick, independent of how many rows get written.
	//
	// It is separate from DefaultMaxSkipRecords because they bound
	// different resources: that one bounds database writes, this one
	// bounds the recurrence walk itself. Without it, a minutely schedule
	// whose last_fired is years old would expand millions of occurrences
	// in memory before the row cap ever came into play.
	maxCatchUpExpansion = 10000
)

// Launcher is the narrow port the Scanner needs from the dispatch side: the
// ability to launch a template and get back a job id.
//
// It is deliberately one method wide. api.Dispatcher satisfies it, so a
// scheduled run goes through exactly the path a person clicking Launch
// goes through -- template resolution, credential binding, job creation,
// JetStream publication, the activity stream -- with no second
// implementation to drift. Declaring the narrow interface here rather than
// taking *api.Dispatcher is also what keeps internal/api out of this
// package's imports, and therefore out of a cycle.
type Launcher interface {
	// LaunchScheduled launches templateID under actor, optionally reusing
	// a saved launch configuration, and returns the new job's id.
	LaunchScheduled(ctx context.Context, actor string, templateID, savedConfigID int) (string, error)
}

// Scanner is the loop that turns due schedules into jobs.
//
// It is shaped exactly like dispatch.Reaper, which is the existing,
// production-wired precedent for a leader-gated periodic scan, and the
// resemblance is deliberate rather than incidental: two background sweeps
// in one binary that behaved differently under leadership loss would be two
// things to reason about instead of one.
type Scanner struct {
	store    Store
	launcher Launcher
	logger   *slog.Logger

	interval       time.Duration
	pageSize       int
	maxSkipRecords int
	now            func() time.Time
}

// ScannerOption configures optional, non-default behaviour.
type ScannerOption func(*Scanner)

// WithScanInterval overrides the sweep interval. Chiefly for tests that
// must prove sweep behaviour without waiting out the real interval, the
// same role dispatch.WithReapInterval plays.
func WithScanInterval(d time.Duration) ScannerOption {
	return func(s *Scanner) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithDuePageSize overrides the keyset page size.
func WithDuePageSize(n int) ScannerOption {
	return func(s *Scanner) {
		if n > 0 {
			s.pageSize = n
		}
	}
}

// WithMaxSkipRecords overrides how many individual skipped rows a recovery
// writes before collapsing the rest into one counted row.
func WithMaxSkipRecords(n int) ScannerOption {
	return func(s *Scanner) {
		if n > 0 {
			s.maxSkipRecords = n
		}
	}
}

// WithClock replaces the Scanner's source of the current time.
//
// This exists so the missed-run gate can simulate an outage of hours
// without sleeping for hours. It is the only way to test the coalescing
// policy at all: the behaviour under test is defined entirely in terms of
// time having passed while nothing was leading.
func WithClock(now func() time.Time) ScannerOption {
	return func(s *Scanner) {
		if now != nil {
			s.now = now
		}
	}
}

// WithLogger sets the logger. Defaults to slog.Default.
func WithLogger(l *slog.Logger) ScannerOption {
	return func(s *Scanner) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewScanner builds a Scanner over a store and a launcher.
func NewScanner(store Store, launcher Launcher, opts ...ScannerOption) *Scanner {
	s := &Scanner{
		store:          store,
		launcher:       launcher,
		logger:         slog.Default(),
		interval:       DefaultScanInterval,
		pageSize:       DefaultDuePageSize,
		maxSkipRecords: DefaultMaxSkipRecords,
		now:            time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Run blocks until ctx is done, ticking every interval and sweeping only
// while isLeader reports true.
//
// isLeader is a plain function rather than an *election.LeaderElector, so
// this package never imports internal/election -- a caller passes
// elector.IsLeader directly. That is not a stylistic preference: Phase 23's
// Adversarial Pattern Justification gate asks for proof that election was
// consumed rather than reimplemented, and a package that cannot see the
// election primitive cannot have rebuilt it. internal/archtest asserts the
// missing import directly.
//
// Leadership bounds how many replicas scan; it does NOT make firing unique.
// See fire for what does.
func (s *Scanner) Run(ctx context.Context, isLeader func() bool) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !isLeader() {
				continue
			}
			s.Sweep(ctx)
		}
	}
}

// Sweep runs one scan-and-fire pass.
//
// It is exported so a test can drive exactly one pass deterministically
// instead of racing a ticker, and so an operator-facing "run the scan now"
// could be added later without a second implementation.
//
// A failure on one schedule is logged and does not abort the pass or any
// future one: the schedule stays due and the next tick finds it again, so a
// transient failure costs one interval of lateness rather than a lost run.
func (s *Scanner) Sweep(ctx context.Context) {
	now := s.now().UTC()
	var cursor DueCursor

	for {
		due, err := s.store.ListDue(ctx, now, cursor, s.pageSize)
		if err != nil {
			s.logger.Error("scheduler failed to list due schedules",
				slog.String("error", err.Error()))
			return
		}
		if len(due) == 0 {
			return
		}

		for _, sched := range due {
			if err := s.process(ctx, sched, now); err != nil {
				s.logger.Error("scheduler failed to process a due schedule",
					slog.String("schedule_id", sched.ScheduleID),
					slog.String("name", sched.Name),
					slog.String("error", err.Error()))
			}
		}

		last := due[len(due)-1]
		if last.NextRun == nil {
			// A schedule with no next_run cannot have been selected as
			// due, so this is unreachable; returning rather than looping
			// on an unchanged cursor is the safe way to be wrong.
			return
		}
		cursor = DueCursor{NextRun: *last.NextRun, ScheduleID: last.ScheduleID}

		if len(due) < s.pageSize {
			return
		}
	}
}

// process handles one due schedule: work out which occurrences it owes,
// record the ones it will not run, and fire the one it will.
func (s *Scanner) process(ctx context.Context, sched Schedule, now time.Time) error {
	missed, target, err := s.due(sched, now)
	if err != nil {
		return err
	}
	if target.IsZero() {
		// Nothing is actually due. The cached next_run was stale -- the
		// rule was edited, or the schedule ran out of occurrences -- so
		// recompute it and stop selecting this schedule.
		next, err := sched.ComputeNextRun(now)
		if err != nil {
			return err
		}
		return s.store.MarkFired(ctx, sched.ScheduleID, timeOrZero(sched.LastFired), next)
	}

	s.recordMissed(ctx, sched, missed)

	if err := s.fire(ctx, sched, target); err != nil {
		return err
	}

	next, err := sched.ComputeNextRun(target)
	if err != nil {
		return err
	}
	return s.store.MarkFired(ctx, sched.ScheduleID, target, next)
}

// due works out which occurrences have passed unrun, and which single one
// will actually run.
//
// This is the coalescing missed-run policy, and it is the answer to "the
// controller was down for four hours; what happens now". Everything up to
// the most recent missed occurrence is recorded as skipped; only the most
// recent one runs.
//
// The alternative shapes were both rejected for concrete reasons. Firing
// every missed occurrence turns a recovery into a thundering herd -- a
// fifteen-minute health check down for four hours would launch sixteen jobs
// at once, against devices that rate-limit or lock accounts. Firing nothing
// and silently advancing leaves an unexplained gap: a nightly backup that
// missed its window because of a five-minute database restart would go 48
// hours between runs with nothing recording why. Coalescing gives the
// current desired state once, and leaves an auditable row for every run
// that did not happen.
func (s *Scanner) due(sched Schedule, now time.Time) (missed []time.Time, target time.Time, err error) {
	loc, err := sched.Location()
	if err != nil {
		return nil, time.Time{}, err
	}
	set, err := sched.RuleSet()
	if err != nil {
		return nil, time.Time{}, err
	}

	// Start from just after the last occurrence that ran, so a run is
	// never repeated; from DTStart on a schedule that has never run.
	//
	// The comparison is "not before" rather than "after" deliberately. A
	// schedule whose first occurrence IS its DTStart -- the ordinary case
	// for an unqualified rule -- has LastFired exactly equal to DTStart
	// once it has run once, and a strict After would leave `from` at
	// DTStart and re-select that same first occurrence forever after.
	from := sched.DTStart
	if sched.LastFired != nil && !sched.LastFired.Before(from) {
		from = sched.LastFired.Add(time.Nanosecond)
	}

	occurrences, err := set.Expand(sched.DTStart.In(loc), from.In(loc), loc, maxCatchUpExpansion)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, time.Time{}, err
		}
		return nil, time.Time{}, err
	}

	var passed []time.Time
	for _, o := range occurrences {
		utc := o.UTC()
		if utc.After(now) {
			break
		}
		if sched.DTEnd != nil && utc.After(*sched.DTEnd) {
			break
		}
		passed = append(passed, utc)
	}
	if len(passed) == 0 {
		return nil, time.Time{}, nil
	}
	return passed[:len(passed)-1], passed[len(passed)-1], nil
}

// recordMissed writes the audit rows for occurrences that will not run.
//
// Beyond maxSkipRecords the remainder collapses into one row carrying the
// count, and that truncation is logged as a warning. A silent cap would
// make an incomplete history indistinguishable from a complete one, which
// is the same reasoning gosec-waivers.json and flaky-packages.json apply to
// their own exceptions: a bound nobody can see is a bound nobody can check.
func (s *Scanner) recordMissed(ctx context.Context, sched Schedule, missed []time.Time) {
	if len(missed) == 0 {
		return
	}

	written := missed
	var truncated []time.Time
	if len(missed) > s.maxSkipRecords {
		written = missed[:s.maxSkipRecords]
		truncated = missed[s.maxSkipRecords:]
	}

	for _, occ := range written {
		if err := s.store.RecordSkip(ctx, sched.ScheduleID, occ, ReasonMissedWindow, 0); err != nil {
			// A skip row that cannot be written must not stop the run
			// that can: losing an audit row is bad, losing the run is
			// worse.
			s.logger.Error("scheduler could not record a missed occurrence",
				slog.String("schedule_id", sched.ScheduleID),
				slog.Time("occurrence_at", occ),
				slog.String("error", err.Error()))
		}
	}

	if len(truncated) > 0 {
		s.logger.Warn("scheduler collapsed missed occurrences into one record",
			slog.String("schedule_id", sched.ScheduleID),
			slog.String("name", sched.Name),
			slog.Int("recorded", len(written)),
			slog.Int("collapsed", len(truncated)))
		last := truncated[len(truncated)-1]
		if err := s.store.RecordSkip(ctx, sched.ScheduleID, last, ReasonMissedWindowTruncated, len(truncated)); err != nil {
			s.logger.Error("scheduler could not record the collapsed missed occurrences",
				slog.String("schedule_id", sched.ScheduleID),
				slog.String("error", err.Error()))
		}
	}

	s.logger.Info("scheduler skipped occurrences that passed while nothing was running",
		slog.String("schedule_id", sched.ScheduleID),
		slog.String("name", sched.Name),
		slog.Int("missed", len(missed)))
}

// fire claims one occurrence and launches it.
//
// The ORDER here is the entire duplicate-fire guarantee, so it is worth
// stating plainly: the claim is an insert against a unique index on
// (schedule, occurrence_at), and it happens BEFORE anything is launched or
// published. Two replicas that both believe they lead -- which internal/
// election permits briefly, since it runs a two-second lease with a
// half-second poll and exposes no fencing token -- both attempt the insert,
// and exactly one succeeds. The loser gets ErrAlreadyClaimed and moves on.
//
// Leader election is a load-reduction mechanism here, not a correctness
// one. Reversing these two steps, or replacing the insert with a
// read-then-write, would silently reintroduce double firing under exactly
// the failover conditions the scheduler exists to survive.
func (s *Scanner) fire(ctx context.Context, sched Schedule, occurrenceAt time.Time) error {
	occ, err := s.store.ClaimOccurrence(ctx, sched.ScheduleID, occurrenceAt)
	if err != nil {
		if errors.Is(err, ErrAlreadyClaimed) {
			s.logger.Debug("scheduler skipped an occurrence another replica had claimed",
				slog.String("schedule_id", sched.ScheduleID),
				slog.Time("occurrence_at", occurrenceAt))
			return nil
		}
		return err
	}

	jobID, launchErr := s.launcher.LaunchScheduled(ctx, ScheduleActor(sched.ScheduleID), sched.TemplateID, sched.SavedConfigID)
	if launchErr != nil {
		if err := s.store.ResolveOccurrence(ctx, occ.ID, OutcomeSkipped, ReasonLaunchFailed, ""); err != nil {
			s.logger.Error("scheduler could not record a failed launch",
				slog.String("schedule_id", sched.ScheduleID),
				slog.String("error", err.Error()))
		}
		return launchErr
	}

	if err := s.store.ResolveOccurrence(ctx, occ.ID, OutcomeFired, "", jobID); err != nil {
		// The job is already running; only the bookkeeping failed. Say so
		// loudly and carry on rather than returning an error that would
		// read as "the schedule did not run".
		s.logger.Error("scheduler launched a job but could not record the occurrence",
			slog.String("schedule_id", sched.ScheduleID),
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
	}

	s.logger.Info("scheduler launched a scheduled job",
		slog.String("schedule_id", sched.ScheduleID),
		slog.String("name", sched.Name),
		slog.String("job_id", jobID),
		slog.Time("occurrence_at", occurrenceAt))
	return nil
}

// ScheduleActor is the actor string a scheduled run is attributed to.
//
// A scheduled launch has no human behind it, and internal/access's audited
// store refuses an unattributed write outright (ErrUnattributed). Its own
// doc comment anticipates this case and prescribes exactly this answer: "a
// future scheduler firing an unattended run supplies its own constant
// actor at the composition root, deliberately and visibly."
//
// The schedule id is included so the activity stream says WHICH schedule,
// not merely that something was scheduled. A reader looking at an unexpected
// job needs to get from it to the schedule that caused it.
func ScheduleActor(scheduleID string) string {
	return "scheduler:" + scheduleID
}

// timeOrZero dereferences an optional time.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
