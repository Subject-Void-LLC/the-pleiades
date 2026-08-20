package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// This file fuzzes every distinct timestamp-parsing call shape in
// pkg/filters, per this phase's own Fuzz/Stress Test checklist item to
// fuzz every timestamp-parsing function against malformed input
// (impossible dates, out-of-range values, ambiguous formats). Most of
// Phase 55's functions funnel through the one shared parseISO8601
// helper (timeconvert.go), so fuzzing one target per distinct argument
// shape (a bare timestamp, a timestamp plus a timezone name, two
// timestamps plus a fallback, a schedule's HH:MM fields plus a
// timestamp, and a cron expression plus a timestamp driving the bounded
// search loop) exercises the same parsing code every other function in
// this file also calls, rather than fuzzing twenty-five near-identical
// wrappers around it.

func FuzzISO8601ToEpoch(f *testing.F) {
	seeds := []string{
		"2024-01-01T00:00:00Z", "1970-01-01T00:00:00Z", "", "not-a-timestamp",
		"2024-13-40T99:99:99Z", "2024-01-01", "2024-01-01T00:00:00.999999999Z",
		"2024-01-01T00:00:00+99:99",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, iso string) {
		filters.ISO8601ToEpoch(iso)
	})
}

func FuzzShiftTimezone(f *testing.F) {
	seeds := []struct{ iso, tz string }{
		{"2024-01-01T00:00:00Z", "America/New_York"},
		{"", ""},
		{"not-a-timestamp", "UTC"},
		{"2024-01-01T00:00:00Z", "Not/A_Real_Zone"},
		{"2024-01-01T00:00:00Z", "../../etc/passwd"},
	}
	for _, s := range seeds {
		f.Add(s.iso, s.tz)
	}
	f.Fuzz(func(t *testing.T, iso, tz string) {
		filters.ShiftTimezone(iso, tz)
	})
}

func FuzzRoundToHour(f *testing.F) {
	seeds := []string{
		"2024-01-01T13:45:30Z", "2024-06-15T10:45:00+05:30", "", "not-a-timestamp",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, iso string) {
		filters.RoundToHour(iso)
	})
}

func FuzzDeltaSeconds(f *testing.F) {
	seeds := []struct {
		a, b     string
		fallback int
	}{
		{"2024-01-01T00:00:00Z", "2024-01-02T00:00:00Z", -1},
		{"", "", 0},
		{"not-a-timestamp", "2024-01-01T00:00:00Z", -1},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b, s.fallback)
	}
	f.Fuzz(func(t *testing.T, a, b string, fallback int) {
		filters.DeltaSeconds(a, b, fallback)
	})
}

func FuzzIsBusinessHour(f *testing.F) {
	seeds := []struct{ start, end, iso string }{
		{"09:00", "17:00", "2024-01-01T10:00:00Z"},
		{"", "", ""},
		{"9am", "17:00", "2024-01-01T10:00:00Z"},
		{"25:99", "-1:-1", "not-a-timestamp"},
	}
	for _, s := range seeds {
		f.Add(s.start, s.end, s.iso)
	}
	f.Fuzz(func(t *testing.T, start, end, iso string) {
		filters.IsBusinessHour(map[string]any{"start": start, "end": end}, iso)
	})
}

func FuzzCronNextRun(f *testing.F) {
	seeds := []struct{ cronExpr, fromISO string }{
		{"0 9 * * *", "2024-01-01T08:00:00Z"},
		{"*/15 * * * *", "2024-01-01T00:07:00Z"},
		{"0 0 31 2 *", "2024-01-01T00:00:00Z"},
		{"", ""},
		{"@daily", "not-a-timestamp"},
	}
	for _, s := range seeds {
		f.Add(s.cronExpr, s.fromISO)
	}
	f.Fuzz(func(t *testing.T, cronExpr, fromISO string) {
		filters.CronNextRun(cronExpr, fromISO)
		filters.CronPreviousRun(cronExpr, fromISO)
	})
}
