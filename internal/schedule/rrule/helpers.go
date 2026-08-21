package rrule

import (
	"sort"
	"time"
)

// allMonths is every month number, used when a YEARLY rule's day-level
// parts put the whole year in scope.
var allMonths = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

// containsInt reports whether v appears in xs. The lists it searches are
// bounded by their own value ranges (at most 12 months, 31 month-days, 24
// hours, 60 minutes), so a linear scan is the right shape here and a map
// would cost more to build than it saves.
func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// sortedInts returns a sorted copy of xs, leaving the caller's slice
// alone. Expansion must be deterministic regardless of the order the
// operator wrote a BY list in, and sorting in place would mutate the
// parsed Recurrence the caller may reuse.
func sortedInts(xs []int) []int {
	out := append([]int(nil), xs...)
	sort.Ints(out)
	return out
}

// hasOrdinal reports whether any BYDAY entry carries an ordinal, which is
// what decides whether a YEARLY rule counts weekdays across the year or
// within each month.
func hasOrdinal(days []WeekdayNum) bool {
	for _, d := range days {
		if d.Ordinal != 0 {
			return true
		}
	}
	return false
}

// matchesMonthDay reports whether t's day of month satisfies any BYMONTHDAY
// entry, resolving negative entries against the real length of t's own
// month so that -1 is 28 February in a common year and 29 in a leap year.
func matchesMonthDay(monthDays []int, t time.Time) bool {
	length := daysInMonth(t.Year(), t.Month())
	day := t.Day()
	for _, md := range monthDays {
		switch {
		case md > 0 && md == day:
			return true
		case md < 0 && length+md+1 == day:
			return true
		}
	}
	return false
}

// matchesAnyWeekday reports whether t falls on any BYDAY entry's weekday,
// ignoring ordinals.
//
// Ignoring them is correct here rather than lossy: this is only reached
// from the sub-daily and DAILY frequencies, and Parse refuses an ordinal
// BYDAY for exactly those frequencies, so no ordinal can reach it.
func matchesAnyWeekday(days []WeekdayNum, t time.Time) bool {
	for _, d := range days {
		if d.Weekday == t.Weekday() {
			return true
		}
	}
	return false
}

// daysInMonth returns the number of days in the given month, leap years
// included. time.Date normalising day 0 of the next month back to the last
// day of this one is the standard-library idiom for this, and avoids
// hand-written leap-year arithmetic.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// daysInYear returns 365 or 366.
func daysInYear(year int) int {
	return time.Date(year+1, time.January, 0, 0, 0, 0, 0, time.UTC).YearDay()
}

// dedupeSorted removes adjacent duplicate readings from an ascending
// slice, in place.
//
// Duplicates are real rather than hypothetical: a BYDAY list and a
// BYMONTHDAY list can select the same day by two routes, and a period's
// hour/minute fan-out can land twice on one reading.
//
// It deliberately compares WALL CLOCKS, not instants, because it runs
// inside the civil-time walk and dateutil does the same. Across a
// spring-forward gap two distinct readings can denote one instant (02:00
// EST and 03:00 EDT are both 07:00Z); dateutil emits both, so an hourly
// rule crossing the gap yields a repeated instant. Collapsing them here
// would silently drop an occurrence AWX keeps. The scheduler's own unique
// index on (schedule, occurrence_at) is what stops a repeated instant
// firing twice, which is the layer that can actually enforce it.
func dedupeSorted(ts []civil) []civil {
	if len(ts) < 2 {
		return ts
	}
	out := ts[:1]
	for _, t := range ts[1:] {
		if !t.Equal(out[len(out)-1]) {
			out = append(out, t)
		}
	}
	return out
}
