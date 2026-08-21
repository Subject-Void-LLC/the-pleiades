package rrule

import (
	"fmt"
	"sort"
	"time"
)

// maxEmptyPeriods bounds how many consecutive candidate periods may yield
// no occurrence before Expand gives up and reports the rule as
// unsatisfiable.
//
// Some legal rules never match: FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30 names
// a date that does not exist in any year. Without a bound, expanding one
// walks the calendar forever. The value has to clear the largest genuine
// gap a satisfiable rule can produce, which is FREQ=YEARLY;BYMONTHDAY=29
// with BYMONTH=2 -- a leap day, so up to 8 empty years around a century
// boundary -- with generous headroom above it. A rule that finds nothing
// in 400 periods is reporting an operator error, not a sparse schedule.
const maxEmptyPeriods = 400

// ErrUnsatisfiable is returned when a rule is well-formed and supported
// but names no real instant, such as 30 February.
//
// It is a distinct error because it is a distinct operator mistake: the
// rule parsed, the parts are all supported, and it still will never run.
// Reporting it at save time is the whole point -- a schedule that silently
// never fires is the failure mode this project has recorded repeatedly.
var ErrUnsatisfiable = fmt.Errorf("rrule: recurrence names no real instant")

// Expand returns up to limit occurrences of r at or after start, in the
// given location, in ascending order.
//
// dtstart is the recurrence anchor: RFC 5545 takes the time-of-day and,
// for the parts a rule leaves unspecified, the day and month, from it. It
// is a separate argument from start because the two answer different
// questions -- dtstart is where the series begins, start is where the
// caller wants to begin reading it -- and conflating them is what makes a
// paginated scheduler re-fire history.
//
// Every occurrence is returned in loc. Callers convert to UTC for storage;
// this package deliberately does not, because the wall-clock value is the
// one an operator recognises and the one daylight-saving behaviour is
// defined in terms of.
//
// limit is mandatory and bounds the work regardless of whether the rule
// itself is bounded, so no caller can ask this package for an infinite
// result by forgetting a COUNT.
func Expand(r Recurrence, dtstart time.Time, start time.Time, loc *time.Location, limit int) ([]time.Time, error) {
	if loc == nil {
		return nil, fmt.Errorf("rrule: a location is required")
	}
	if limit <= 0 {
		return nil, nil
	}
	if r.Freq == 0 || r.Interval < 1 {
		return nil, fmt.Errorf("rrule: expand needs a rule built by Parse")
	}

	// The whole walk happens in civil (wall-clock) time and only becomes a
	// real instant at emission. That is what makes daylight saving come out
	// right: a normalised instant must never feed back into the next step,
	// or an hourly rule crossing a spring-forward gap collapses onto one
	// repeated instant. See localize for the two behaviours involved.
	anchorLocal := dtstart.In(loc)
	anchor := toCivil(anchorLocal)
	anchorInstant := localize(anchor, loc)
	until := r.resolveUntil(loc)

	// count tracks position in the series from dtstart, not from start,
	// because COUNT bounds the series itself. A caller reading page two of
	// a COUNT=10 rule must still stop at the tenth occurrence overall, so
	// occurrences before start are counted and discarded rather than
	// skipped cheaply.
	var (
		out     []time.Time
		count   int
		empties int
	)

	for period := anchor; ; period = r.advance(period) {
		if empties > maxEmptyPeriods {
			if len(out) > 0 || count > 0 {
				// The series was real and simply ended: a bounded rule
				// whose window closed, not an unsatisfiable one.
				return out, nil
			}
			return nil, ErrUnsatisfiable
		}

		candidates := r.expandPeriod(period, anchor)
		if len(candidates) == 0 {
			empties++
			continue
		}
		empties = 0

		for _, c := range candidates {
			t := localize(c, loc)
			if t.Before(anchorInstant) {
				continue
			}
			if !until.IsZero() && t.After(until) {
				return out, nil
			}
			count++
			if r.Count > 0 && count > r.Count {
				return out, nil
			}
			if t.Before(start) {
				continue
			}
			out = append(out, t)
			if len(out) >= limit {
				return out, nil
			}
		}

		// A bounded rule that has emitted its whole COUNT stops here even
		// if the caller asked for more, and an unbounded one keeps walking
		// until limit or until.
		if r.Count > 0 && count >= r.Count {
			return out, nil
		}
	}
}

// Next returns the first occurrence strictly after after, or the zero Time
// when the rule has none.
//
// This is the entry point the scheduler's next_run computation uses. It is
// expressed in terms of Expand rather than duplicating the walk, so the
// two can never disagree about what a rule means -- the failure mode a
// separate "just compute the next one" fast path would inevitably grow.
func Next(r Recurrence, dtstart time.Time, after time.Time, loc *time.Location) (time.Time, error) {
	// One nanosecond past `after` is the smallest representable "strictly
	// after", and every occurrence this package produces is minute-aligned,
	// so this can never skip a real occurrence.
	occurrences, err := Expand(r, dtstart, after.In(loc).Add(time.Nanosecond), loc, 1)
	if err != nil {
		return time.Time{}, err
	}
	if len(occurrences) == 0 {
		return time.Time{}, nil
	}
	return occurrences[0], nil
}

// resolveUntil returns the rule's UNTIL as an instant in loc, or the zero
// Time when the rule has none.
//
// A UTC-qualified UNTIL is a fixed instant and merely changes zone. A
// floating one is a wall-clock reading, so it is re-anchored into loc:
// "UNTIL=20240301T090000" means nine in the morning wherever the schedule
// runs, which is a different instant in Sydney than in New York. That
// distinction is RFC 5545's, and dropping it would silently shift a
// schedule's end by the zone offset.
func (r Recurrence) resolveUntil(loc *time.Location) time.Time {
	if r.Until.IsZero() {
		return time.Time{}
	}
	if r.UntilIsUTC {
		return r.Until.In(loc)
	}
	return time.Date(r.Until.Year(), r.Until.Month(), r.Until.Day(),
		r.Until.Hour(), r.Until.Minute(), r.Until.Second(), 0, loc)
}

// advance steps from one candidate period to the next, INTERVAL periods
// later, in civil time.
//
// It works on calendar fields rather than by adding a fixed duration, and
// takes no location at all. Both matter. Adding 24h to 09:00 on the day
// before a spring-forward would land on 10:00, whereas constructing the
// next day at 09:00 keeps 09:00 -- which is what "daily at nine" means to
// an operator. And doing it with no zone in scope is what guarantees a
// normalised wall clock can never feed back into the walk.
func (r Recurrence) advance(t civil) civil {
	y, m, d := t.Date()
	hh, mm := t.Hour(), t.Minute()
	switch r.Freq {
	case Minutely:
		return time.Date(y, m, d, hh, mm+r.Interval, 0, 0, time.UTC)
	case Hourly:
		return time.Date(y, m, d, hh+r.Interval, mm, 0, 0, time.UTC)
	case Daily:
		return time.Date(y, m, d+r.Interval, hh, mm, 0, 0, time.UTC)
	case Weekly:
		return time.Date(y, m, d+7*r.Interval, hh, mm, 0, 0, time.UTC)
	case Monthly:
		// Anchoring to day 1 before adding months is what stops a rule
		// anchored on the 31st from skipping February entirely: adding a
		// month to 31 January normalises into early March, which would
		// advance two months and drop the intervening period's candidates.
		// The real day of month comes back from expandPeriod.
		return time.Date(y, m+time.Month(r.Interval), 1, hh, mm, 0, 0, time.UTC)
	case Yearly:
		return time.Date(y+r.Interval, m, 1, hh, mm, 0, 0, time.UTC)
	default:
		return time.Date(y, m, d+1, hh, mm, 0, 0, time.UTC)
	}
}

// expandPeriod produces every occurrence one candidate period contains,
// sorted ascending, after applying the BY* narrowing parts and BYSETPOS.
//
// anchor supplies the defaults RFC 5545 takes from DTSTART for whichever
// parts the rule leaves unspecified: the time of day always, and the day
// of month or month itself for the coarser frequencies.
func (r Recurrence) expandPeriod(period, anchor civil) []civil {
	days := r.candidateDays(period, anchor)
	if len(days) == 0 {
		return nil
	}

	// Where an unspecified time of day comes from depends on the
	// frequency, and getting this wrong is silent rather than loud: taking
	// the hour from the anchor under FREQ=HOURLY makes every period expand
	// to the same instant, so the rule appears to fire once and never
	// again. A frequency finer than the field supplies that field from the
	// period it is currently walking; anything coarser inherits it from
	// DTSTART, which is RFC 5545's own defaulting rule.
	hours := r.ByHour
	if len(hours) == 0 {
		if r.Freq == Minutely || r.Freq == Hourly {
			hours = []int{period.Hour()}
		} else {
			hours = []int{anchor.Hour()}
		}
	}
	minutes := r.ByMinute
	if len(minutes) == 0 {
		if r.Freq == Minutely {
			minutes = []int{period.Minute()}
		} else {
			minutes = []int{anchor.Minute()}
		}
	}

	out := make([]civil, 0, len(days)*len(hours)*len(minutes))
	for _, day := range days {
		for _, hh := range hours {
			for _, mm := range minutes {
				y, mo, d := day.Date()
				out = append(out, time.Date(y, mo, d, hh, mm, 0, 0, time.UTC))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	out = dedupeSorted(out)

	if len(r.BySetPos) > 0 {
		out = applySetPos(out, r.BySetPos)
	}
	return out
}

// candidateDays returns the days within period the rule selects, before
// time-of-day expansion.
//
// The period's extent depends on FREQ: for the sub-daily frequencies a
// period is one instant already and the BY parts act as filters on it, so
// this returns at most that single day. For DAILY it is one day, for
// WEEKLY the seven days of the week containing period, and for MONTHLY and
// YEARLY the whole month or year.
func (r Recurrence) candidateDays(period, anchor civil) []civil {
	switch r.Freq {
	case Minutely, Hourly, Daily:
		if !r.dayMatches(period) {
			return nil
		}
		return []civil{period}
	case Weekly:
		return r.weeklyDays(period, anchor)
	case Monthly:
		return r.monthlyDays(period, anchor)
	case Yearly:
		return r.yearlyDays(period, anchor)
	default:
		return nil
	}
}

// dayMatches applies the day-level BY filters to one concrete day. It is
// the filter half of the expansion, used by the frequencies whose period
// is already a single day or finer.
func (r Recurrence) dayMatches(t time.Time) bool {
	if len(r.ByMonth) > 0 && !containsInt(r.ByMonth, int(t.Month())) {
		return false
	}
	if len(r.ByMonthDay) > 0 && !matchesMonthDay(r.ByMonthDay, t) {
		return false
	}
	if len(r.ByDay) > 0 && !matchesAnyWeekday(r.ByDay, t) {
		return false
	}
	return true
}

// weeklyDays returns the days of period's week that the rule selects,
// honouring WKST for where the week starts.
func (r Recurrence) weeklyDays(period, anchor civil) []civil {
	// Days back to the start of this week, given WkSt.
	offset := (int(period.Weekday()) - int(r.WkSt) + 7) % 7
	y, m, d := period.Date()
	weekStart := time.Date(y, m, d-offset, 0, 0, 0, 0, time.UTC)

	wanted := r.ByDay
	if len(wanted) == 0 {
		wanted = []WeekdayNum{{Weekday: anchor.Weekday()}}
	}

	out := make([]civil, 0, len(wanted))
	for i := 0; i < 7; i++ {
		wy, wm, wd := weekStart.Date()
		day := time.Date(wy, wm, wd+i, 0, 0, 0, 0, time.UTC)
		if len(r.ByMonth) > 0 && !containsInt(r.ByMonth, int(day.Month())) {
			continue
		}
		if len(r.ByMonthDay) > 0 && !matchesMonthDay(r.ByMonthDay, day) {
			continue
		}
		for _, w := range wanted {
			// An ordinal is refused for WEEKLY at parse time, so only the
			// weekday itself is consulted here.
			if day.Weekday() == w.Weekday {
				out = append(out, day)
				break
			}
		}
	}
	return out
}

// monthlyDays returns the days of period's month the rule selects.
func (r Recurrence) monthlyDays(period, anchor civil) []civil {
	y, m := period.Year(), period.Month()
	if len(r.ByMonth) > 0 && !containsInt(r.ByMonth, int(m)) {
		return nil
	}
	return r.daysInSpan(time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), daysInMonth(y, m), anchor, monthScope)
}

// yearlyDays returns the days of period's year the rule selects.
func (r Recurrence) yearlyDays(period, anchor civil) []civil {
	y := period.Year()

	months := r.ByMonth
	if len(months) == 0 {
		if len(r.ByDay) > 0 || len(r.ByMonthDay) > 0 {
			// With a day-level part but no BYMONTH, an ordinal BYDAY counts
			// across the whole year, so every month is in scope.
			months = allMonths
		} else {
			// With no day-level part at all, YEARLY repeats DTSTART's own
			// month and day, which is RFC 5545's DTSTART-defaulting rule.
			months = []int{int(anchor.Month())}
		}
	}

	// An ordinal BYDAY with no BYMONTH counts within the year, not within
	// each month, so it is resolved over the whole year as one span.
	if len(r.ByMonth) == 0 && hasOrdinal(r.ByDay) {
		return r.daysInSpan(time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC), daysInYear(y), anchor, yearScope)
	}

	var out []civil
	for _, mi := range sortedInts(months) {
		m := time.Month(mi)
		out = append(out, r.daysInSpan(time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), daysInMonth(y, m), anchor, monthScope)...)
	}
	return out
}

// ordinalScope says which span an ordinal BYDAY counts within.
type ordinalScope int

const (
	monthScope ordinalScope = iota
	yearScope
)

// daysInSpan walks a contiguous span of days starting at spanStart and
// returns those the rule's day-level parts select.
//
// It is shared by the monthly and yearly paths because the selection logic
// is identical once the span is fixed; only the span differs. Writing it
// once is what keeps "last Friday of the month" and "last Friday of the
// year" from drifting apart.
func (r Recurrence) daysInSpan(spanStart civil, length int, anchor civil, scope ordinalScope) []civil {
	sy, sm, sd := spanStart.Date()
	days := make([]civil, 0, length)
	for i := 0; i < length; i++ {
		days = append(days, time.Date(sy, sm, sd+i, 0, 0, 0, 0, time.UTC))
	}

	// With no day-level part, the span contributes DTSTART's day of month.
	if len(r.ByDay) == 0 && len(r.ByMonthDay) == 0 {
		want := anchor.Day()
		for _, d := range days {
			if d.Day() == want {
				return []civil{d}
			}
		}
		return nil
	}

	var out []civil
	for _, d := range days {
		if len(r.ByMonthDay) > 0 && !matchesMonthDay(r.ByMonthDay, d) {
			continue
		}
		if len(r.ByDay) > 0 && !r.matchesByDayInSpan(d, days, scope) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// matchesByDayInSpan reports whether d satisfies any BYDAY entry, resolving
// ordinals against the span rather than the calendar month, so a yearly
// rule's "-1FR" means the last Friday of the year.
func (r Recurrence) matchesByDayInSpan(d civil, span []civil, _ ordinalScope) bool {
	var fromStart, fromEnd int
	if hasOrdinal(r.ByDay) {
		fromStart, fromEnd = ordinalOf(d, span)
	}
	for _, w := range r.ByDay {
		if d.Weekday() != w.Weekday {
			continue
		}
		switch {
		case w.Ordinal == 0:
			return true
		case w.Ordinal > 0 && w.Ordinal == fromStart:
			return true
		case w.Ordinal < 0 && w.Ordinal == fromEnd:
			return true
		}
	}
	return false
}

// ordinalOf returns d's position among the days of span sharing its
// weekday, counted from both ends: (1, -5) for the first of five Mondays,
// (5, -1) for the last.
//
// Both senses are returned together because RFC 5545 permits either in one
// BYDAY list ("1MO,-1MO" is the first and last Monday), and computing them
// in one pass keeps the two from disagreeing about which days are in the
// span. A day whose weekday does not appear in span at all returns (0, 0),
// which matches no ordinal.
func ordinalOf(d civil, span []civil) (fromStart, fromEnd int) {
	var total, index int
	for _, s := range span {
		if s.Weekday() != d.Weekday() {
			continue
		}
		total++
		if s.Equal(d) {
			index = total
		}
	}
	if index == 0 {
		return 0, 0
	}
	return index, index - total - 1
}

// applySetPos selects occurrences from one period's sorted list by
// position, 1-based, with negative positions counting from the end.
func applySetPos(occurrences []civil, positions []int) []civil {
	var out []civil
	seen := make(map[int]bool, len(positions))
	for _, p := range positions {
		idx := p - 1
		if p < 0 {
			idx = len(occurrences) + p
		}
		if idx < 0 || idx >= len(occurrences) || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, occurrences[idx])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
