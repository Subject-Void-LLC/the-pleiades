package filters

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronFieldBounds names the [min,max] a standard 5-field cron
// expression's fields accept, in field order: minute, hour, day of
// month, month, day of week (0 = Sunday). This is the same 5-field shape
// cron(8) uses, not the 6-or-7-field forms some schedulers add for
// seconds or years.
var cronFieldBounds = [5][2]int{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week
}

// cronSchedule is the parsed form of a 5-field cron expression: each
// field expanded to its exact set of matching values. This structure,
// not just a pass/fail bool, is what Phase 55's CronNextRun/
// CronPreviousRun walk one candidate day, then one candidate minute,
// at a time against; Phase 54's own IsValidCronExpr only needs to know
// parseCronExpr returned no error.
//
// domWildcard/dowWildcard record whether the day-of-month and
// day-of-week fields were literally "*" in the source expression, not
// whether their expanded set happens to cover every value (a field like
// "0-6" for day-of-week expands to the same set "*" would, but is not
// treated as a wildcard). This is real cron's own well-known day-field
// rule, reproduced deliberately rather than simplified away:
// dateMatches below ORs day-of-month against day-of-week when both are
// restricted, instead of ANDing them, matching standard cron(8)
// behavior (a schedule of "0 0 1 * 1", every Monday OR the first of the
// month, not only a Monday that also happens to be the first).
type cronSchedule struct {
	minute, hour, dayOfMonth, month, dayOfWeek map[int]bool
	domWildcard, dowWildcard                   bool
}

// parseCronExpr parses expr as a standard 5-field cron expression
// (minute hour day-of-month month day-of-week). Each field accepts "*",
// a literal, a range ("a-b"), a step ("*/n" or "a-b/n"), or a
// comma-separated list of any of those. It deliberately does not accept
// the non-standard named shorthands some cron implementations add
// ("@daily", "@hourly"): this is a small, hand-rolled parser, matching
// this project's own preference for owning a small parser over taking a
// dependency for one, not a full-featured cron implementation.
func parseCronExpr(expr string) (cronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("filters: cron expression must have 5 fields, got %d", len(fields))
	}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		set, err := parseCronField(f, cronFieldBounds[i][0], cronFieldBounds[i][1])
		if err != nil {
			return cronSchedule{}, fmt.Errorf("filters: cron field %d (%q): %w", i, f, err)
		}
		sets[i] = set
	}
	return cronSchedule{
		minute:      sets[0],
		hour:        sets[1],
		dayOfMonth:  sets[2],
		month:       sets[3],
		dayOfWeek:   sets[4],
		domWildcard: fields[2] == "*",
		dowWildcard: fields[4] == "*",
	}, nil
}

func parseCronField(field string, min, max int) (map[int]bool, error) {
	set := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty list item")
		}
		rangePart, step, err := splitCronStep(part)
		if err != nil {
			return nil, err
		}
		lo, hi := min, max
		if rangePart != "*" {
			lo, hi, err = parseCronRange(rangePart, min, max)
			if err != nil {
				return nil, err
			}
		}
		if step < 1 {
			return nil, fmt.Errorf("step must be at least 1, got %d", step)
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return set, nil
}

// splitCronStep splits "a-b/n" or "*/n" into its range/wildcard part and
// step, defaulting to step 1 when there is no "/n" suffix at all.
func splitCronStep(part string) (rangePart string, step int, err error) {
	idx := strings.IndexByte(part, '/')
	if idx < 0 {
		return part, 1, nil
	}
	rangePart = part[:idx]
	stepStr := part[idx+1:]
	step, err = strconv.Atoi(stepStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid step %q", stepStr)
	}
	return rangePart, step, nil
}

// parseCronRange parses "a-b" or a bare "a" into an inclusive [lo,hi]
// pair, bounds-checked against [min,max].
func parseCronRange(part string, min, max int) (int, int, error) {
	if idx := strings.IndexByte(part, '-'); idx >= 0 {
		loStr, hiStr := part[:idx], part[idx+1:]
		lo, err := strconv.Atoi(loStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start %q", loStr)
		}
		hi, err := strconv.Atoi(hiStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range end %q", hiStr)
		}
		if lo < min || hi > max || lo > hi {
			return 0, 0, fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
		}
		return lo, hi, nil
	}
	v, err := strconv.Atoi(part)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid value %q", part)
	}
	if v < min || v > max {
		return 0, 0, fmt.Errorf("value %d out of bounds [%d,%d]", v, min, max)
	}
	return v, v, nil
}

// IsValidCronExpr reports whether expr parses as a standard 5-field cron
// expression via parseCronExpr, this file's own real parser: there is no
// separate regex-based approximation for this to disagree with, so
// IsValidCronExpr returning true and parseCronExpr succeeding on the
// same input are the same event by construction.
func IsValidCronExpr(expr string) bool {
	if len(expr) > MaxInputBytes {
		return false
	}
	_, err := parseCronExpr(expr)
	return err == nil
}

// cronSearchBoundDays bounds how many days CronNextRun/CronPreviousRun
// will search before giving up: a little over four years, generous for
// any real schedule while still terminating an impossible one (e.g.
// "0 0 31 2 *", day 31 in February, which no date ever satisfies)
// instead of searching forever. BenchmarkCronNextRun_NeverMatches proves
// the worst case is fast enough to matter in practice, not just bounded
// in principle.
const cronSearchBoundDays = 4*366 + 1

// dateMatches reports whether t's date (month, day-of-month, day-of-
// week) satisfies cs, applying real cron's own day-field OR rule: when
// both day-of-month and day-of-week are restricted (neither field was a
// literal "*"), a match on either one counts, not only a match on both.
func (cs cronSchedule) dateMatches(t time.Time) bool {
	if !cs.month[int(t.Month())] {
		return false
	}
	domMatch := cs.dayOfMonth[t.Day()]
	dowMatch := cs.dayOfWeek[int(t.Weekday())]
	switch {
	case cs.domWildcard && cs.dowWildcard:
		return true
	case cs.domWildcard:
		return dowMatch
	case cs.dowWildcard:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

// nextRun returns the earliest time strictly after from that cs
// matches. It searches one candidate day at a time, skipping a whole
// day in one step when dateMatches already rejects it rather than
// walking that day's 1440 minutes individually, and only scans minutes
// within a day whose date does match.
func (cs cronSchedule) nextRun(from time.Time) (time.Time, bool) {
	loc := from.Location()
	dayStart := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	startMinute := from.Hour()*60 + from.Minute() + 1
	for i := 0; i <= cronSearchBoundDays; i++ {
		day := dayStart.AddDate(0, 0, i)
		if !cs.dateMatches(day) {
			continue
		}
		fromMinute := 0
		if i == 0 {
			fromMinute = startMinute
		}
		for m := fromMinute; m < 24*60; m++ {
			hour, minute := m/60, m%60
			if cs.hour[hour] && cs.minute[minute] {
				return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc), true
			}
		}
	}
	return time.Time{}, false
}

// previousRun returns the latest time strictly before from that cs
// matches, the mirror of nextRun searching backward in time.
func (cs cronSchedule) previousRun(from time.Time) (time.Time, bool) {
	loc := from.Location()
	dayStart := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	startMinute := from.Hour()*60 + from.Minute() - 1
	for i := 0; i <= cronSearchBoundDays; i++ {
		day := dayStart.AddDate(0, 0, -i)
		if !cs.dateMatches(day) {
			continue
		}
		toMinute := 24*60 - 1
		if i == 0 {
			toMinute = startMinute
		}
		for m := toMinute; m >= 0; m-- {
			hour, minute := m/60, m%60
			if cs.hour[hour] && cs.minute[minute] {
				return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc), true
			}
		}
	}
	return time.Time{}, false
}

// CronNextRun returns the next timestamp strictly after fromISO that
// matches cronExpr, or "" if cronExpr is invalid, fromISO is malformed,
// or no match exists within cronSearchBoundDays.
func CronNextRun(cronExpr, fromISO string) string {
	if len(cronExpr) > MaxInputBytes {
		return ""
	}
	cs, err := parseCronExpr(cronExpr)
	if err != nil {
		return ""
	}
	from, ok := parseISO8601(fromISO)
	if !ok {
		return ""
	}
	next, ok := cs.nextRun(from)
	if !ok {
		return ""
	}
	return next.Format(time.RFC3339)
}

// CronPreviousRun returns the most recent timestamp strictly before
// fromISO that matches cronExpr, or "" if cronExpr is invalid, fromISO
// is malformed, or no match exists within cronSearchBoundDays.
func CronPreviousRun(cronExpr, fromISO string) string {
	if len(cronExpr) > MaxInputBytes {
		return ""
	}
	cs, err := parseCronExpr(cronExpr)
	if err != nil {
		return ""
	}
	from, ok := parseISO8601(fromISO)
	if !ok {
		return ""
	}
	prev, ok := cs.previousRun(from)
	if !ok {
		return ""
	}
	return prev.Format(time.RFC3339)
}
