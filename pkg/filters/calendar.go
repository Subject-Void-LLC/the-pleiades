package filters

import (
	"strconv"
	"strings"
	"time"
)

// IsPast reports whether iso is strictly before asOf, or false if either
// is malformed. asOf is an explicit argument, not the wall clock:
// PLAN.md Section 36 requires every filter to be a pure, deterministic
// function of its arguments, so a caller supplies its own reference
// timestamp (typically a value captured once at runbook load time, e.g.
// vars.now) rather than this package reading time.Now() itself. Every
// past/future/older/expiring predicate in this file shares that shape.
func IsPast(iso, asOf string) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	ref, ok := parseISO8601(asOf)
	if !ok {
		return false
	}
	return t.Before(ref)
}

// IsFuture reports whether iso is strictly after asOf, or false if
// either is malformed.
func IsFuture(iso, asOf string) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	ref, ok := parseISO8601(asOf)
	if !ok {
		return false
	}
	return t.After(ref)
}

// IsOlderThan reports whether iso is at least thresholdSeconds before
// asOf, or false if either timestamp is malformed.
func IsOlderThan(iso, asOf string, thresholdSeconds int) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	ref, ok := parseISO8601(asOf)
	if !ok {
		return false
	}
	return ref.Sub(t) >= time.Duration(thresholdSeconds)*time.Second
}

// IsExpiringWithin reports whether iso falls within windowSeconds after
// asOf; an iso that is already in the past relative to asOf does not
// count as "expiring", it has already expired. Returns false if either
// timestamp is malformed.
func IsExpiringWithin(iso, asOf string, windowSeconds int) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	ref, ok := parseISO8601(asOf)
	if !ok {
		return false
	}
	delta := t.Sub(ref)
	return delta >= 0 && delta <= time.Duration(windowSeconds)*time.Second
}

// StartOfDay floors iso to 00:00:00 in its own timezone, returning "" if
// iso is malformed.
func StartOfDay(iso string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Format(time.RFC3339)
}

// StartOfWeek floors iso to 00:00:00 on the most recent Monday (ISO 8601
// weeks start on Monday) in its own timezone, returning "" if iso is
// malformed.
func StartOfWeek(iso string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	// time.Weekday: Sunday=0 .. Saturday=6. Days since the most recent
	// Monday: Sunday is 6 days after last Monday, Monday itself is 0.
	offset := (int(t.Weekday()) + 6) % 7
	monday := t.AddDate(0, 0, -offset)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, t.Location()).Format(time.RFC3339)
}

// StartOfMonth floors iso to 00:00:00 on the first of its own month, in
// its own timezone, returning "" if iso is malformed.
func StartOfMonth(iso string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Format(time.RFC3339)
}

// IsLeapYear reports whether year is a Gregorian leap year: divisible by
// 4, except a century year (divisible by 100) unless also divisible by
// 400.
func IsLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// DayOfWeek returns iso's weekday name in its own timezone ("Monday"
// through "Sunday"), or "" if iso is malformed.
func DayOfWeek(iso string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	return t.Weekday().String()
}

// mapString fetches a string-typed value at key, reporting false if the
// key is absent or its value is not a string. Shared by IsBusinessHour
// and IsMaintenanceWindow, both of which read a schedule/window map
// built from arbitrary CEL dyn values (celToMap in
// internal/engine/cel_filters.go), never a typed Go struct.
func mapString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// parseHHMM parses a 24 hour "HH:MM" time-of-day string into minutes
// since midnight (e.g. "09:30" -> 570), or reports false for anything
// else: a missing colon, a non-numeric component, or a component out of
// its own [0,23]/[0,59] range.
func parseHHMM(s string) (int, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	hh, err := strconv.Atoi(parts[0])
	if err != nil || hh < 0 || hh > 23 {
		return 0, false
	}
	mm, err := strconv.Atoi(parts[1])
	if err != nil || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// isBusinessWeekday reports whether wd is Monday through Friday, the
// default IsBusinessHour uses when schedule carries no "days" entry.
func isBusinessWeekday(wd time.Weekday) bool {
	return wd >= time.Monday && wd <= time.Friday
}

// dayNameMatches compares wd against a weekday name in schedule's "days"
// list case-insensitively, matching either an abbreviated ("Mon") or
// full ("Monday") spelling by comparing only the first three letters, a
// deliberately small, exact rule rather than a fuzzy one: a name shorter
// than three letters, or one whose first three letters do not match any
// real weekday, never matches.
func dayNameMatches(wd time.Weekday, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) < 3 {
		return false
	}
	return name[:3] == strings.ToLower(wd.String())[:3]
}

// IsBusinessHour reports whether iso falls within schedule's business
// hours, or false if iso or schedule is malformed. schedule carries:
//
//   - "start", "end": required "HH:MM" 24 hour time-of-day strings,
//     inclusive of both ends. An overnight window (end before start,
//     e.g. "22:00" to "06:00") is not supported and always returns
//     false rather than silently misevaluating one; PLAN.md Section 36's
//     own scope is a pure value transform, not a scheduling engine, and
//     an overnight business-hours schedule is unusual enough that a
//     clear "not supported" beats a guessed answer.
//   - "days": an optional list of weekday names (see dayNameMatches).
//     Absent entirely, this defaults to Monday through Friday. Present
//     but not a list is treated as malformed (false), distinct from
//     simply being absent.
//
// iso is evaluated in its own timezone, exactly as received; a caller
// comparing against a schedule defined in a different zone is expected
// to call filters.shiftTimezone first.
func IsBusinessHour(schedule map[string]any, iso string) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	startStr, ok := mapString(schedule, "start")
	if !ok {
		return false
	}
	endStr, ok := mapString(schedule, "end")
	if !ok {
		return false
	}
	startMin, ok := parseHHMM(startStr)
	if !ok {
		return false
	}
	endMin, ok := parseHHMM(endStr)
	if !ok {
		return false
	}
	if startMin > endMin {
		return false
	}
	nowMin := t.Hour()*60 + t.Minute()
	if nowMin < startMin || nowMin > endMin {
		return false
	}

	rawDays, present := schedule["days"]
	if !present {
		return isBusinessWeekday(t.Weekday())
	}
	days, ok := rawDays.([]any)
	if !ok {
		return false
	}
	for _, d := range days {
		name, ok := d.(string)
		if !ok {
			continue
		}
		if dayNameMatches(t.Weekday(), name) {
			return true
		}
	}
	return false
}

// IsMaintenanceWindow reports whether iso falls within window's "start"
// and "end" timestamps, inclusive of both ends, or false if any of the
// three is malformed or window's end precedes its start.
func IsMaintenanceWindow(window map[string]any, iso string) bool {
	t, ok := parseISO8601(iso)
	if !ok {
		return false
	}
	startStr, ok := mapString(window, "start")
	if !ok {
		return false
	}
	endStr, ok := mapString(window, "end")
	if !ok {
		return false
	}
	start, ok := parseISO8601(startStr)
	if !ok {
		return false
	}
	end, ok := parseISO8601(endStr)
	if !ok {
		return false
	}
	if end.Before(start) {
		return false
	}
	return !t.Before(start) && !t.After(end)
}
