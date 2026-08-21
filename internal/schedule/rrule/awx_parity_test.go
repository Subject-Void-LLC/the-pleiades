package rrule_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/rrule"
)

// parityFixture is one generated case: a rule, its exclusions, and the
// occurrences python-dateutil produced for it.
//
// The fixtures exist because this package hand-rolls its recurrence engine
// rather than depending on one, and the risk that carries is silent
// divergence from AWX rather than an outright bug. AWX expands schedules
// with python-dateutil, so agreeing with dateutil IS agreeing with AWX.
// tools/genrrulefixtures/gen.py generates the file; Python is not a build
// or CI dependency and the JSON is committed.
type parityFixture struct {
	Name        string   `json:"name"`
	Timezone    string   `json:"timezone"`
	DTStart     string   `json:"dtstart"`
	RRule       string   `json:"rrule"`
	Exclusions  []string `json:"exclusions"`
	Occurrences []struct {
		Local string `json:"local"`
		UTC   string `json:"utc"`
	} `json:"occurrences"`
}

type parityFile struct {
	Cases []parityFixture `json:"cases"`
}

func loadParityFixtures(t *testing.T) []parityFixture {
	t.Helper()
	path := filepath.Join("testdata", "awx_parity.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var f parityFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s contains no cases", path)
	}
	return f.Cases
}

// TestAWXParity is the Phase 23 Release Gate assertion, generalised: every
// generated case must expand to exactly the instants dateutil produced,
// compared as absolute instants rather than as formatted strings, so a
// zone-offset error cannot hide behind matching wall clocks.
func TestAWXParity(t *testing.T) {
	for _, fixture := range loadParityFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			loc, err := time.LoadLocation(fixture.Timezone)
			if err != nil {
				t.Fatalf("loading zone %q: %v", fixture.Timezone, err)
			}
			dtstart, err := time.ParseInLocation("2006-01-02T15:04:05", fixture.DTStart, loc)
			if err != nil {
				t.Fatalf("parsing dtstart %q: %v", fixture.DTStart, err)
			}

			set, err := rrule.ParseRuleSet(fixture.RRule, fixture.Exclusions)
			if err != nil {
				t.Fatalf("ParseRuleSet(%q, %v): %v", fixture.RRule, fixture.Exclusions, err)
			}

			got, err := set.Expand(dtstart, dtstart, loc, len(fixture.Occurrences))
			if err != nil {
				t.Fatalf("Expand: %v", err)
			}
			if len(got) != len(fixture.Occurrences) {
				t.Fatalf("got %d occurrences, dateutil produced %d\n got: %s\nwant: %s",
					len(got), len(fixture.Occurrences), formatTimes(got), formatFixture(fixture))
			}

			for i, want := range fixture.Occurrences {
				wantUTC, err := time.Parse("2006-01-02T15:04:05Z", want.UTC)
				if err != nil {
					t.Fatalf("parsing fixture utc %q: %v", want.UTC, err)
				}
				if !got[i].Equal(wantUTC) {
					t.Errorf("occurrence %d: got %s (%s), dateutil says %s (%s)",
						i,
						got[i].UTC().Format(time.RFC3339), got[i].Format("2006-01-02T15:04:05-07:00"),
						wantUTC.Format(time.RFC3339), want.Local)
				}
				// The rendered local time must agree too, except in the
				// one case where agreement is impossible and disagreement
				// is correct.
				//
				// When a rule lands in a spring-forward gap, Python
				// represents the occurrence as the wall clock the operator
				// wrote paired with the pre-transition offset --
				// "02:30-0500" -- which is a reading that never appears on
				// any clock in that zone. Go cannot represent it: a
				// time.Time is an instant, and rendering that instant in
				// New York necessarily gives 03:30-0400. The two describe
				// the SAME instant, which is what actually decides when a
				// job runs and is asserted unconditionally above; only the
				// notation differs, and Go's is the one an operator's clock
				// would have shown.
				//
				// So the local rendering is compared strictly whenever the
				// fixture's own wall clock is a real reading, and skipped
				// only when it is not. Skipping it silently for every case
				// would give up a genuine check to accommodate a rare one.
				gotLocal := got[i].Format("2006-01-02T15:04:05-0700")
				if gotLocal != want.Local && wallClockExists(t, want.Local, loc) {
					t.Errorf("occurrence %d local: got %s, dateutil says %s", i, gotLocal, want.Local)
				}
			}
		})
	}
}

// TestReleaseGate_ExclusionAcrossDaylightSaving is the Release Gate as
// IMPLEMENTATION.md words it: "A recurrence with an exclusion rule produces
// the same ten occurrences as AWX across a daylight saving boundary."
//
// It is written out separately from TestAWXParity's table even though the
// same case appears there, because the gate names one specific claim and a
// reader checking whether this phase met it should find that claim asserted
// by name rather than have to trust that a table row covers it.
func TestReleaseGate_ExclusionAcrossDaylightSaving(t *testing.T) {
	const caseName = "daily_excluding_weekends_across_spring_forward"

	var fixture parityFixture
	for _, f := range loadParityFixtures(t) {
		if f.Name == caseName {
			fixture = f
			break
		}
	}
	if fixture.Name == "" {
		t.Fatalf("fixture %q is missing; regenerate testdata with tools/genrrulefixtures/gen.py", caseName)
	}
	if len(fixture.Occurrences) != 10 {
		t.Fatalf("the gate is stated in terms of ten occurrences; fixture has %d", len(fixture.Occurrences))
	}
	if len(fixture.Exclusions) == 0 {
		t.Fatal("the gate requires an exclusion rule; fixture has none")
	}

	loc, err := time.LoadLocation(fixture.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	dtstart, err := time.ParseInLocation("2006-01-02T15:04:05", fixture.DTStart, loc)
	if err != nil {
		t.Fatal(err)
	}
	set, err := rrule.ParseRuleSet(fixture.RRule, fixture.Exclusions)
	if err != nil {
		t.Fatal(err)
	}
	got, err := set.Expand(dtstart, dtstart, loc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d occurrences, want 10", len(got))
	}

	// The boundary must actually be inside the window, or the gate proves
	// nothing: the offset has to change across the ten occurrences.
	first, last := got[0].Format("-0700"), got[9].Format("-0700")
	if first == last {
		t.Fatalf("no daylight saving transition inside the window: offset stayed %s", first)
	}

	for i, want := range fixture.Occurrences {
		wantUTC, err := time.Parse("2006-01-02T15:04:05Z", want.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if !got[i].Equal(wantUTC) {
			t.Errorf("occurrence %d: got %s, AWX (dateutil) says %s",
				i, got[i].UTC().Format(time.RFC3339), wantUTC.Format(time.RFC3339))
		}
		if got[i].Weekday() == time.Saturday || got[i].Weekday() == time.Sunday {
			t.Errorf("occurrence %d fell on a %s, which the exclusion rule removes", i, got[i].Weekday())
		}
	}
}

// wallClockExists reports whether the wall-clock reading in a fixture's
// local timestamp is one that actually occurs in loc, by round-tripping it:
// a reading inside a daylight-saving gap comes back as a different wall
// clock, because there is no instant it could name.
func wallClockExists(t *testing.T, localStamp string, loc *time.Location) bool {
	t.Helper()
	// The fixture stamp carries an offset, which is exactly the part that
	// is fictional in the gap case, so only the wall-clock half is used.
	wall, err := time.Parse("2006-01-02T15:04:05-0700", localStamp)
	if err != nil {
		t.Fatalf("parsing fixture local stamp %q: %v", localStamp, err)
	}
	y, m, d := wall.Date()
	round := time.Date(y, m, d, wall.Hour(), wall.Minute(), wall.Second(), 0, loc)
	ry, rm, rd := round.Date()
	return ry == y && rm == m && rd == d &&
		round.Hour() == wall.Hour() && round.Minute() == wall.Minute()
}

func formatTimes(ts []time.Time) string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Format("2006-01-02T15:04:05-0700"))
	}
	return joinLines(out)
}

func formatFixture(f parityFixture) string {
	out := make([]string, 0, len(f.Occurrences))
	for _, o := range f.Occurrences {
		out = append(out, o.Local)
	}
	return joinLines(out)
}

func joinLines(xs []string) string {
	s := ""
	for _, x := range xs {
		s += "\n  " + x
	}
	return s
}
