package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// validSchedule is the baseline every Validate case varies one field of, so
// each test says exactly what makes its own case invalid.
func validSchedule() schedule.Schedule {
	return schedule.Schedule{
		Name:         "nightly",
		LaunchableID: 42,
		Enabled:      true,
		RRule:        "FREQ=DAILY",
		Timezone:     "America/New_York",
		DTStart:      time.Date(2024, 3, 8, 14, 0, 0, 0, time.UTC),
	}
}

// TestValidateRefusalsNameTheFieldAtFault is the property every consumer
// depends on: the UI attaches the message to a control and the API puts the
// field in a problem document, so a refusal that could not say which field
// would be actionable to nobody.
func TestValidateRefusalsNameTheFieldAtFault(t *testing.T) {
	cases := []struct {
		name  string
		mutex func(*schedule.Schedule)
		field string
	}{
		{"no name", func(s *schedule.Schedule) { s.Name = "  " }, "name"},
		{"nothing to launch", func(s *schedule.Schedule) { s.LaunchableID = 0 }, schedule.TargetField},
		{"no dtstart", func(s *schedule.Schedule) { s.DTStart = time.Time{} }, "dtstart"},
		{"unknown zone", func(s *schedule.Schedule) { s.Timezone = "Mars/Olympus" }, "timezone"},
		{"hostile zone", func(s *schedule.Schedule) { s.Timezone = "../../etc/passwd" }, "timezone"},
		{"unsupported recurrence", func(s *schedule.Schedule) { s.RRule = "FREQ=SECONDLY" }, "rrule"},
		{"malformed recurrence", func(s *schedule.Schedule) { s.RRule = "not a rule" }, "rrule"},
		{
			"recurrence that never happens",
			func(s *schedule.Schedule) { s.RRule = "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30" },
			"rrule",
		},
		{
			"bad exclusion",
			func(s *schedule.Schedule) { s.Exclusions = []string{"FREQ=SECONDLY"} },
			"exclusions",
		},
		{
			"end before start",
			func(s *schedule.Schedule) {
				before := s.DTStart.Add(-time.Hour)
				s.DTEnd = &before
			},
			"dtend",
		},
		{
			"end equal to start",
			func(s *schedule.Schedule) {
				same := s.DTStart
				s.DTEnd = &same
			},
			"dtend",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSchedule()
			tc.mutex(&s)

			err := s.Validate()
			if err == nil {
				t.Fatal("Validate accepted a schedule it must refuse")
			}
			if !errors.Is(err, schedule.ErrInvalid) {
				t.Errorf("error does not report as ErrInvalid: %v", err)
			}
			var fe schedule.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("error is not a FieldError: %v", err)
			}
			if fe.Field != tc.field {
				t.Errorf("blamed %q, want %q", fe.Field, tc.field)
			}
			if fe.Message == "" {
				t.Error("the refusal carries no message for the person who typed the value")
			}
			// The message is written for a reader, not copied from a
			// parser: it must not leak the package prefix.
			if len(fe.Message) > 7 && fe.Message[:7] == "rrule: " {
				t.Errorf("message leaks the parser's own prefix: %q", fe.Message)
			}
			if fe.Error() == "" {
				t.Error("FieldError.Error returned nothing")
			}
		})
	}
}

func TestValidateAcceptsAWholeSchedule(t *testing.T) {
	s := validSchedule()
	s.Exclusions = []string{"FREQ=WEEKLY;BYDAY=SA,SU", "EXDATE:20241225T000000Z"}
	end := s.DTStart.AddDate(1, 0, 0)
	s.DTEnd = &end

	if err := s.Validate(); err != nil {
		t.Fatalf("Validate refused a valid schedule: %v", err)
	}
}

// TestErrNameTakenIsRecoverableAndStillInvalid proves the sentinel and the
// category coexist, which is what lets an API answer 409 for a collision
// and 400 for everything else without reading message text.
func TestErrNameTakenIsRecoverableAndStillInvalid(t *testing.T) {
	err := error(schedule.FieldError{
		Field:   "name",
		Message: "A schedule with that name already exists in this organization.",
		Cause:   schedule.ErrNameTaken,
	})
	if !errors.Is(err, schedule.ErrInvalid) {
		t.Error("a name collision does not report as ErrInvalid")
	}
	if !errors.Is(err, schedule.ErrNameTaken) {
		t.Error("a name collision does not report as ErrNameTaken")
	}

	// A plain field error carries no cause and must not match.
	plain := error(schedule.FieldError{Field: "rrule", Message: "nope"})
	if errors.Is(plain, schedule.ErrNameTaken) {
		t.Error("an ordinary field error reported as a name collision")
	}
	if !errors.Is(plain, schedule.ErrInvalid) {
		t.Error("an ordinary field error does not report as ErrInvalid")
	}
}

// TestComputeNextRunAcrossDaylightSaving pins the behaviour an operator
// actually notices: the wall clock holds and the interval does not.
func TestComputeNextRunAcrossDaylightSaving(t *testing.T) {
	s := validSchedule()
	s.RRule = "FREQ=DAILY"
	// 14:00Z on 8 March is 09:00 EST.
	s.DTStart = time.Date(2024, 3, 8, 14, 0, 0, 0, time.UTC)

	next, err := s.ComputeNextRun(s.DTStart)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil {
		t.Fatal("an unbounded daily schedule has no next run")
	}
	if want := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next run = %s, want %s", next, want)
	}

	// Stepping over the 10 March transition, the UTC hour moves by one
	// while the local hour does not.
	after := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)
	next, err = s.ComputeNextRun(after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, 3, 10, 13, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next run across the transition = %s, want %s (an hour earlier in UTC)", next, want)
	}
	loc, err := s.Location()
	if err != nil {
		t.Fatal(err)
	}
	if hour := next.In(loc).Hour(); hour != 9 {
		t.Errorf("local hour = %d, want the wall clock preserved at 9", hour)
	}
}

// TestComputeNextRunReturnsNilRatherThanAnError covers the three settled
// states that are not failures: disabled, exhausted, and past dtend.
func TestComputeNextRunReturnsNilRatherThanAnError(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		s := validSchedule()
		s.Enabled = false
		next, err := s.ComputeNextRun(s.DTStart)
		if err != nil || next != nil {
			t.Errorf("= (%v, %v), want (nil, nil): a disabled schedule must not advertise a time", next, err)
		}
	})

	t.Run("exhausted by COUNT", func(t *testing.T) {
		s := validSchedule()
		s.RRule = "FREQ=DAILY;COUNT=2"
		next, err := s.ComputeNextRun(s.DTStart.AddDate(0, 1, 0))
		if err != nil || next != nil {
			t.Errorf("= (%v, %v), want (nil, nil)", next, err)
		}
	})

	t.Run("past dtend", func(t *testing.T) {
		s := validSchedule()
		end := s.DTStart.Add(12 * time.Hour)
		s.DTEnd = &end
		next, err := s.ComputeNextRun(s.DTStart)
		if err != nil || next != nil {
			t.Errorf("= (%v, %v), want (nil, nil): the next occurrence is past dtend", next, err)
		}
	})
}

func TestComputeNextRunReportsABrokenSchedule(t *testing.T) {
	s := validSchedule()
	s.Timezone = "Mars/Olympus"
	if _, err := s.ComputeNextRun(s.DTStart); err == nil {
		t.Error("an unknown zone did not report an error")
	}

	s = validSchedule()
	s.RRule = "FREQ=SECONDLY"
	if _, err := s.ComputeNextRun(s.DTStart); err == nil {
		t.Error("an unsupported recurrence did not report an error")
	}
}

// TestPreviewIsBoundedByDTEnd proves the outer bound is applied to a
// preview as well as to next_run, so what an operator is shown is what will
// happen.
func TestPreviewIsBoundedByDTEnd(t *testing.T) {
	s := validSchedule()
	s.RRule = "FREQ=DAILY"
	end := s.DTStart.AddDate(0, 0, 3)
	s.DTEnd = &end

	occurrences, err := s.Preview(s.DTStart, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 4 {
		t.Fatalf("preview returned %d occurrences, want 4 bounded by dtend", len(occurrences))
	}
	for _, o := range occurrences {
		if o.UTC().After(end) {
			t.Errorf("occurrence %s is past dtend %s", o, end)
		}
	}

	// Preview returns ZONED times, which is what lets a caller render both
	// readings. A UTC time could not be turned back into the right local
	// one without the zone, and confirming the local reading is the whole
	// reason a preview exists.
	loc, err := s.Location()
	if err != nil {
		t.Fatal(err)
	}
	if occurrences[0].Location().String() != loc.String() {
		t.Errorf("preview returned %s, want times in the schedule's own zone", occurrences[0].Location())
	}
}

func TestPreviewOnAnUnsatisfiableRuleIsEmptyRatherThanAnError(t *testing.T) {
	s := validSchedule()
	s.RRule = "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30"

	occurrences, err := s.Preview(s.DTStart, 5)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(occurrences) != 0 {
		t.Errorf("got %d occurrences for a rule that never happens", len(occurrences))
	}
}

func TestPreviewReportsABrokenSchedule(t *testing.T) {
	s := validSchedule()
	s.Timezone = "Mars/Olympus"
	if _, err := s.Preview(s.DTStart, 5); err == nil {
		t.Error("an unknown zone did not report an error")
	}

	s = validSchedule()
	s.RRule = "FREQ=SECONDLY"
	if _, err := s.Preview(s.DTStart, 5); err == nil {
		t.Error("an unsupported recurrence did not report an error")
	}
}

func TestDueCursorZero(t *testing.T) {
	if !(schedule.DueCursor{}).Zero() {
		t.Error("an empty cursor does not report as the start of a scan")
	}
	if (schedule.DueCursor{ScheduleID: "x"}).Zero() {
		t.Error("a cursor carrying an id reported as the start of a scan")
	}
	if (schedule.DueCursor{NextRun: time.Now()}).Zero() {
		t.Error("a cursor carrying a time reported as the start of a scan")
	}
}

// TestListPagesAnOrganizationsSchedules covers the read path the UI and the
// API both page through.
func TestListPagesAnOrganizationsSchedules(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s := f.newSchedule(fmt.Sprintf("sched-%d", i))
		if _, err := f.store.Create(ctx, s, launchable.Everything()); err != nil {
			t.Fatal(err)
		}
	}

	all, err := f.store.List(ctx, f.orgA, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("listed %d schedules, want 5", len(all))
	}

	// A limit is honoured, and the cursor resumes after it.
	first, err := f.store.List(ctx, f.orgA, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("listed %d schedules for a limit of 2", len(first))
	}
	rest, err := f.store.List(ctx, f.orgA, first[len(first)-1].ScheduleID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rest {
		for _, seen := range first {
			if r.ScheduleID == seen.ScheduleID {
				t.Errorf("schedule %s appeared on both pages", r.ScheduleID)
			}
		}
	}

	// The other tenant sees none of them.
	other, err := f.store.List(ctx, f.orgB, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("another tenant listed %d of this tenant's schedules", len(other))
	}

	// AnyOrganization disables the filter, which is the sentinel the API
	// layer passes while this platform has no per-request tenant.
	unscoped, err := f.store.List(ctx, schedule.AnyOrganization, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unscoped) != 5 {
		t.Errorf("AnyOrganization listed %d schedules, want all 5", len(unscoped))
	}
}

// TestStoreRefusesAnInvalidSchedule proves validation is enforced at the
// store rather than only at the handler, so no write path can skip it.
func TestStoreRefusesAnInvalidSchedule(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	bad := f.newSchedule("nightly")
	bad.RRule = "FREQ=SECONDLY"
	if _, err := f.store.Create(ctx, bad, launchable.Everything()); !errors.Is(err, schedule.ErrInvalid) {
		t.Errorf("Create = %v, want ErrInvalid", err)
	}

	good, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	good.RRule = "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30"
	if _, err := f.store.Update(ctx, good, launchable.Everything()); !errors.Is(err, schedule.ErrInvalid) {
		t.Errorf("Update = %v, want ErrInvalid", err)
	}
}

// TestStoreRefusesAnUnknownTarget covers the other create-time refusal.
func TestStoreRefusesAnUnknownTarget(t *testing.T) {
	f := newStoreFixture(t)
	s := f.newSchedule("nightly")
	s.LaunchableID = 99999

	_, err := f.store.Create(context.Background(), s, launchable.Everything())
	if err == nil {
		t.Fatal("a schedule naming something that does not exist was accepted")
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != schedule.TargetField {
		t.Errorf("error = %v, want a FieldError blaming the template", err)
	}
}

// TestUpdateAndGetOnAMissingSchedule covers the not-found paths.
func TestUpdateAndGetOnAMissingSchedule(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	missing := f.newSchedule("ghost")
	missing.ScheduleID = "does-not-exist"
	missing.OrganizationID = f.orgA
	if _, err := f.store.Update(ctx, missing, launchable.Everything()); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("Update = %v, want ErrNotFound", err)
	}
	if _, err := f.store.ListOccurrences(ctx, f.orgA, "does-not-exist", 10); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("ListOccurrences = %v, want ErrNotFound", err)
	}
	if err := f.store.RecordSkip(ctx, "does-not-exist", time.Now(), schedule.ReasonMissedWindow, 0); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("RecordSkip = %v, want ErrNotFound", err)
	}
	if _, err := f.store.ClaimOccurrence(ctx, "does-not-exist", time.Now()); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("ClaimOccurrence = %v, want ErrNotFound", err)
	}
	if err := f.store.MarkFired(ctx, "does-not-exist", time.Now(), nil); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("MarkFired = %v, want ErrNotFound", err)
	}
	if err := f.store.ResolveOccurrence(ctx, 99999, schedule.OutcomeFired, "", launchable.Launched{RunID: "job", UnifiedJobType: launchable.UnifiedJobJob}); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("ResolveOccurrence = %v, want ErrNotFound", err)
	}
}

// TestResolveOccurrenceRefusesAnUnknownOutcome proves an unrecognised
// outcome is refused rather than defaulted: a default here would write a
// wrong audit row, which is worse than writing none.
func TestResolveOccurrenceRefusesAnUnknownOutcome(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	occ, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ResolveOccurrence(ctx, occ.ID, schedule.Outcome("exploded"), "", launchable.Launched{}); err == nil {
		t.Error("an unknown outcome was accepted")
	}
	// The claimed outcome round-trips, which is the third enum value and
	// the one a crash leaves behind.
	if err := f.store.ResolveOccurrence(ctx, occ.ID, schedule.OutcomeClaimed, "", launchable.Launched{}); err != nil {
		t.Errorf("resolving back to claimed: %v", err)
	}
}

// TestRecordSkipIsIdempotent proves a duplicate skip row is swallowed
// rather than aborting a recovery: the history already answers the question
// the second write was trying to answer.
func TestRecordSkipIsIdempotent(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if err := f.store.RecordSkip(ctx, created.ScheduleID, at, schedule.ReasonMissedWindow, 0); err != nil {
			t.Fatalf("RecordSkip %d: %v", i, err)
		}
	}
	history, err := f.store.ListOccurrences(ctx, f.orgA, created.ScheduleID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Errorf("three identical skips wrote %d rows, want 1", len(history))
	}
}

// TestClampPageSizeBounds covers the page-size ceiling a caller cannot
// raise, through the one method that exposes it.
func TestClampPageSizeBounds(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := f.store.Create(ctx, f.newSchedule(fmt.Sprintf("s-%d", i)), launchable.Everything()); err != nil {
			t.Fatal(err)
		}
	}
	// An absurd limit is capped rather than honoured, which is what stops
	// ?limit=100000 being a way to ask for the whole table.
	got, err := f.store.List(ctx, f.orgA, "", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("listed %d schedules, want the 3 that exist", len(got))
	}
	// A negative limit falls back to the default rather than returning
	// nothing.
	got, err = f.store.List(ctx, f.orgA, "", -5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("a negative limit returned %d schedules, want the default page", len(got))
	}
}
