package rrule_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/rrule"
)

// TestParseRefusals pins the constraint set down case by case.
//
// Each entry is a rule this package must refuse, paired with which of the
// two typed errors it must refuse with. The distinction is the point: an
// operator who wrote valid iCalendar that this scheduler will not run needs
// a different message from one who made a typo, and a test that only
// asserted "some error" would let the two drift together.
func TestParseRefusals(t *testing.T) {
	cases := []struct {
		name string
		rule string
		want error
	}{
		{"empty", "", rrule.ErrMalformed},
		{"no freq", "INTERVAL=2", rrule.ErrMalformed},
		{"unknown part", "FREQ=DAILY;NOPE=1", rrule.ErrMalformed},
		{"not name=value", "FREQ=DAILY;JUSTAWORD", rrule.ErrMalformed},
		{"repeated part", "FREQ=DAILY;INTERVAL=1;INTERVAL=2", rrule.ErrMalformed},
		{"count and until together", "FREQ=DAILY;COUNT=5;UNTIL=20240101T000000Z", rrule.ErrMalformed},
		{"interval zero", "FREQ=DAILY;INTERVAL=0", rrule.ErrMalformed},
		{"interval negative", "FREQ=DAILY;INTERVAL=-1", rrule.ErrMalformed},
		{"count over cap", "FREQ=DAILY;COUNT=99999999", rrule.ErrMalformed},
		{"count zero", "FREQ=DAILY;COUNT=0", rrule.ErrMalformed},
		{"bymonthday zero", "FREQ=MONTHLY;BYMONTHDAY=0", rrule.ErrMalformed},
		{"byhour out of range", "FREQ=DAILY;BYHOUR=24", rrule.ErrMalformed},
		{"byminute out of range", "FREQ=DAILY;BYMINUTE=60", rrule.ErrMalformed},
		{"bymonth out of range", "FREQ=YEARLY;BYMONTH=13", rrule.ErrMalformed},
		{"byday not a weekday", "FREQ=WEEKLY;BYDAY=XX", rrule.ErrMalformed},
		{"byday empty", "FREQ=WEEKLY;BYDAY=", rrule.ErrMalformed},
		{"byday duplicate", "FREQ=WEEKLY;BYDAY=MO,MO", rrule.ErrMalformed},
		{"byday ordinal zero", "FREQ=MONTHLY;BYDAY=0MO", rrule.ErrMalformed},
		{"ordinal byday under weekly", "FREQ=WEEKLY;BYDAY=1MO", rrule.ErrMalformed},
		{"ordinal byday under daily", "FREQ=DAILY;BYDAY=-1FR", rrule.ErrMalformed},
		{"bysetpos with nothing to select", "FREQ=DAILY;BYSETPOS=1", rrule.ErrMalformed},
		{"bad until", "FREQ=DAILY;UNTIL=yesterday", rrule.ErrMalformed},
		{"bad wkst", "FREQ=WEEKLY;WKST=FUNDAY", rrule.ErrMalformed},

		{"secondly", "FREQ=SECONDLY", rrule.ErrUnsupported},
		{"unknown freq", "FREQ=FORTNIGHTLY", rrule.ErrUnsupported},
		{"byweekno", "FREQ=YEARLY;BYWEEKNO=3", rrule.ErrUnsupported},
		{"byyearday", "FREQ=YEARLY;BYYEARDAY=100", rrule.ErrUnsupported},
		{"bysecond", "FREQ=DAILY;BYSECOND=30", rrule.ErrUnsupported},
		{"rdate", "FREQ=DAILY;RDATE=20240101T000000Z", rrule.ErrUnsupported},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rrule.Parse(tc.rule)
			if err == nil {
				t.Fatalf("Parse(%q) was accepted; it must be refused", tc.rule)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("Parse(%q) = %v, want it to wrap %v", tc.rule, err, tc.want)
			}
		})
	}
}

// TestParseAccepts covers the other side: rules inside the constraint set
// must parse, with their defaults applied as RFC 5545 states them rather
// than left as Go zero values.
func TestParseAccepts(t *testing.T) {
	r, err := rrule.Parse("FREQ=WEEKLY;BYDAY=MO,WE")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Interval != 1 {
		t.Errorf("Interval = %d, want the RFC default of 1", r.Interval)
	}
	if r.WkSt != time.Monday {
		t.Errorf("WkSt = %v, want the RFC default of Monday", r.WkSt)
	}
	if r.Bounded() {
		t.Error("a rule with neither COUNT nor UNTIL reported itself as bounded")
	}

	// The RRULE: prefix is what an operator gets when copying out of AWX
	// or an .ics file, so it must be accepted rather than be a syntax error.
	if _, err := rrule.Parse("RRULE:FREQ=DAILY"); err != nil {
		t.Errorf("Parse with an RRULE: prefix: %v", err)
	}
	if _, err := rrule.Parse("freq=daily;byday=mo"); err != nil {
		t.Errorf("Parse is meant to be case-insensitive: %v", err)
	}
	if _, err := rrule.Parse("FREQ=DAILY;"); err != nil {
		t.Errorf("a trailing semicolon should be tolerated: %v", err)
	}

	bounded, err := rrule.Parse("FREQ=DAILY;COUNT=3")
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.Bounded() {
		t.Error("a COUNT rule reported itself as unbounded")
	}
}

// TestExpandUnsatisfiable proves a rule naming a date that never occurs is
// reported at save time rather than becoming a schedule that silently never
// fires.
func TestExpandUnsatisfiable(t *testing.T) {
	r, err := rrule.Parse("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30")
	if err != nil {
		t.Fatalf("the rule is well-formed and supported, so it must parse: %v", err)
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := rrule.Expand(r, start, start, time.UTC, 5); !errors.Is(err, rrule.ErrUnsatisfiable) {
		t.Errorf("Expand = %v, want ErrUnsatisfiable for 30 February", err)
	}
}

// TestNext is the entry point the scheduler's next_run computation uses, so
// its "strictly after" contract is asserted directly: handing it an instant
// that is itself an occurrence must return the following one, never the
// same one, or a fired schedule would immediately look due again.
func TestNext(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	r, err := rrule.Parse("FREQ=DAILY")
	if err != nil {
		t.Fatal(err)
	}
	dtstart := time.Date(2024, 3, 8, 9, 0, 0, 0, loc)

	next, err := rrule.Next(r, dtstart, dtstart, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, 3, 9, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("Next from an occurrence = %s, want the following day %s", next, want)
	}

	// Across the spring-forward boundary the wall clock is preserved, so
	// the gap between two runs is 23 hours rather than 24.
	from := time.Date(2024, 3, 9, 9, 0, 0, 0, loc)
	next, err = rrule.Next(r, dtstart, from, loc)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 9 {
		t.Errorf("Next across spring-forward = %s, want the wall clock preserved at 09:00", next)
	}
	if gap := next.Sub(from); gap != 23*time.Hour {
		t.Errorf("gap across spring-forward = %s, want 23h", gap)
	}
}

// TestNextExhausted proves a bounded rule that has run out reports "no more
// occurrences" as a zero time rather than an error, which is what lets the
// scheduler distinguish a finished schedule from a broken one.
func TestNextExhausted(t *testing.T) {
	r, err := rrule.Parse("FREQ=DAILY;COUNT=2")
	if err != nil {
		t.Fatal(err)
	}
	dtstart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := rrule.Next(r, dtstart, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatalf("an exhausted rule is not an error: %v", err)
	}
	if !next.IsZero() {
		t.Errorf("Next = %s, want the zero time for an exhausted rule", next)
	}
}
