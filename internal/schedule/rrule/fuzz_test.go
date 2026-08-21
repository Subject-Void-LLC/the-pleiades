package rrule_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/rrule"
)

// FuzzParse asserts the two properties that make an operator-supplied
// recurrence string safe to accept: parsing is total (every input either
// yields a rule or a typed error, never a panic), and every rule that
// parses can be expanded within a bounded amount of work.
//
// The second half is the one that matters for Phase 23's Schema/Injection
// Hardening gate. A parser that accepts a rule it cannot expand safely has
// moved the failure from save time to run time, inside the scheduler's own
// scan loop, where it stalls every other schedule in the deployment.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"FREQ=DAILY",
		"FREQ=DAILY;INTERVAL=2;COUNT=5",
		"FREQ=WEEKLY;BYDAY=MO,WE,FR",
		"FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYMONTHDAY=-1",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
		"FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1",
		"FREQ=HOURLY;INTERVAL=3;UNTIL=20240301T090000Z",
		"RRULE:FREQ=MINUTELY;INTERVAL=30",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=SU;WKST=SU",

		// Shapes that must be refused rather than mishandled.
		"",
		";;;",
		"FREQ=SECONDLY",
		"FREQ=DAILY;BYWEEKNO=3",
		"FREQ=DAILY;BYYEARDAY=100",
		"FREQ=DAILY;INTERVAL=0",
		"FREQ=DAILY;COUNT=99999999",
		"FREQ=DAILY;COUNT=5;UNTIL=20240101T000000Z",
		"FREQ=WEEKLY;BYDAY=1MO",
		"FREQ=DAILY;BYSETPOS=1",
		"FREQ=DAILY;FREQ=WEEKLY",
		"FREQ=DAILY;BYMONTHDAY=0",
		"FREQ=DAILY;BYHOUR=99",
		"FREQ=DAILY;UNTIL=not-a-date",
		"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30",
		"FREQ=DAILY;INTERVAL=-1",
		"FREQ=DAILY;BYDAY=XX",
		"FREQ=DAILY;BYDAY=",
		"NOTAPART=1",
		strings.Repeat("FREQ=DAILY;", 5000),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		f.Fatal(err)
	}
	dtstart := time.Date(2024, 3, 1, 9, 0, 0, 0, loc)

	f.Fuzz(func(t *testing.T, input string) {
		r, err := rrule.Parse(input)
		if err != nil {
			// Every refusal must be one of the two typed errors, so a
			// caller can tell "you wrote this wrong" from "we do not
			// support this" without string matching.
			if !errors.Is(err, rrule.ErrMalformed) && !errors.Is(err, rrule.ErrUnsupported) {
				t.Fatalf("Parse(%q) returned an untyped error: %v", input, err)
			}
			return
		}

		// A rule that parsed must be expandable without unbounded work.
		// The deadline is generous: it is here to catch a rule that walks
		// forever, not to benchmark.
		done := make(chan struct{})
		go func() {
			defer close(done)
			occurrences, err := rrule.Expand(r, dtstart, dtstart, loc, 25)
			if err != nil && !errors.Is(err, rrule.ErrUnsatisfiable) {
				t.Errorf("Expand of parsed rule %q returned an unexpected error: %v", input, err)
				return
			}
			if len(occurrences) > 25 {
				t.Errorf("Expand of %q returned %d occurrences for a limit of 25", input, len(occurrences))
			}
			for i := 1; i < len(occurrences); i++ {
				if occurrences[i].Before(occurrences[i-1]) {
					t.Errorf("Expand of %q returned occurrences out of order at %d", input, i)
					break
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("Expand of %q did not finish; the rule is not safely bounded", input)
		}
	})
}

// FuzzParseRuleSet extends the same guarantees to exclusions, which are the
// half an operator is most likely to hand-write and the half where an
// unbounded loop is easiest to construct (a rule excluded by itself).
func FuzzParseRuleSet(f *testing.F) {
	f.Add("FREQ=DAILY", "FREQ=WEEKLY;BYDAY=SA,SU")
	f.Add("FREQ=DAILY", "FREQ=DAILY")
	f.Add("FREQ=DAILY", "EXDATE:20240103T000000Z")
	f.Add("FREQ=DAILY", "EXDATE;TZID=America/New_York:20240311T090000")
	f.Add("FREQ=DAILY", "EXDATE;TZID=../../etc/passwd:20240311T090000")
	f.Add("FREQ=DAILY", "EXDATE;TZID=:20240311T090000")
	f.Add("FREQ=HOURLY", "FREQ=HOURLY")
	f.Add("FREQ=DAILY", "")

	loc := time.UTC
	dtstart := time.Date(2024, 1, 1, 0, 0, 0, 0, loc)

	f.Fuzz(func(t *testing.T, rule, exclusion string) {
		set, err := rrule.ParseRuleSet(rule, []string{exclusion})
		if err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if _, err := set.Expand(dtstart, dtstart, loc, 10); err != nil && !errors.Is(err, rrule.ErrUnsatisfiable) {
				t.Errorf("Expand(%q excluding %q): %v", rule, exclusion, err)
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("Expand(%q excluding %q) did not finish", rule, exclusion)
		}
	})
}
