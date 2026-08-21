package rrule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupported is returned when a rule is well-formed RFC 5545 that this
// package deliberately does not implement.
//
// It is distinct from ErrMalformed because the two mean different things
// to an operator: one says "you wrote this wrong", the other says "this is
// valid iCalendar and we still will not run it". Collapsing them would
// send somebody hunting for a typo in a correct BYWEEKNO.
var ErrUnsupported = errors.New("rrule: recurrence part is outside this scheduler's supported set")

// ErrMalformed is returned when a rule is not valid RFC 5545 at all.
var ErrMalformed = errors.New("rrule: recurrence rule is malformed")

// maxPartLen bounds a single name=value pair before any parsing happens.
//
// Every accepted part is short; the longest realistic BYMINUTE listing all
// sixty minutes is under 250 bytes. This exists so a hostile multi-megabyte
// part is rejected on length before strings.Split allocates a slice per
// comma, which a fuzzer finds immediately and a validator written only in
// terms of value ranges never would.
const maxPartLen = 1024

// maxRuleLen bounds the whole rule string for the same reason.
const maxRuleLen = 8192

// weekdayCodes maps RFC 5545's two-letter weekday codes to Go weekdays.
var weekdayCodes = map[string]time.Weekday{
	"SU": time.Sunday,
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
}

// weekdayNames is weekdayCodes inverted, for String.
var weekdayNames = [7]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// frequencyNames maps RFC 5545's FREQ keywords to the frequencies this
// package accepts. SECONDLY is absent, so it falls through to the same
// unsupported-frequency error an outright misspelling produces -- with the
// keyword echoed, so the two are still distinguishable to a reader.
var frequencyNames = map[string]Frequency{
	"MINUTELY": Minutely,
	"HOURLY":   Hourly,
	"DAILY":    Daily,
	"WEEKLY":   Weekly,
	"MONTHLY":  Monthly,
	"YEARLY":   Yearly,
}

// unsupportedParts names the RFC 5545 recurrence parts this package
// refuses by name, so the error says which part was the problem instead of
// "unknown part".
//
// BYSECOND and SECONDLY are refused because a scheduler whose scan
// interval is measured in tens of seconds cannot honour sub-minute
// recurrence, and pretending otherwise would fire late and look broken.
// BYWEEKNO and BYYEARDAY are calendar-application features with no
// infrastructure use, and each multiplies the expansion surface a fuzzer
// has to cover. RDATE is refused because an explicit date list belongs in
// the schedule's own storage, not smuggled inside a recurrence string.
var unsupportedParts = map[string]string{
	"BYSECOND":  "sub-minute recurrence is finer than the scheduler's scan interval",
	"BYWEEKNO":  "week-number recurrence has no infrastructure use",
	"BYYEARDAY": "day-of-year recurrence has no infrastructure use",
	"RDATE":     "explicit date lists belong in the schedule, not the rule",
}

// Parse parses one RFC 5545 recurrence rule, enforcing this package's
// constraint set. An optional "RRULE:" or "EXRULE:" prefix is accepted and
// ignored, because that is how the rules arrive when an operator copies
// one out of an AWX schedule or an .ics file.
//
// Parse is the only supported way to build a Recurrence. It is total: for
// any input string it returns either a Recurrence this package can expand
// without unbounded work, or an error wrapping ErrMalformed or
// ErrUnsupported. It never panics, which FuzzParse asserts directly.
func Parse(s string) (Recurrence, error) {
	if len(s) > maxRuleLen {
		return Recurrence{}, fmt.Errorf("%w: rule is %d bytes, limit is %d", ErrMalformed, len(s), maxRuleLen)
	}

	s = strings.TrimSpace(s)
	for _, prefix := range []string{"RRULE:", "EXRULE:"} {
		if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	if s == "" {
		return Recurrence{}, fmt.Errorf("%w: rule is empty", ErrMalformed)
	}

	r := Recurrence{Interval: 1, WkSt: time.Monday}
	var (
		seen     = map[string]bool{}
		haveFreq bool
		untilRaw string
	)

	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			// A trailing semicolon is common enough in hand-written rules
			// that refusing it would be pedantry, not safety.
			continue
		}
		if len(part) > maxPartLen {
			return Recurrence{}, fmt.Errorf("%w: rule part is %d bytes, limit is %d", ErrMalformed, len(part), maxPartLen)
		}

		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return Recurrence{}, fmt.Errorf("%w: rule part %q is not NAME=VALUE", ErrMalformed, part)
		}
		name = strings.ToUpper(strings.TrimSpace(name))
		value = strings.TrimSpace(value)

		if reason, bad := unsupportedParts[name]; bad {
			return Recurrence{}, fmt.Errorf("%w: %s (%s)", ErrUnsupported, name, reason)
		}
		if seen[name] {
			// RFC 5545 allows a part at most once. A repeated part is
			// almost always a copy-paste error, and silently taking the
			// last one would run a schedule the operator did not write.
			return Recurrence{}, fmt.Errorf("%w: rule part %s appears more than once", ErrMalformed, name)
		}
		seen[name] = true

		var err error
		switch name {
		case "FREQ":
			freq, ok := frequencyNames[strings.ToUpper(value)]
			if !ok {
				return Recurrence{}, fmt.Errorf("%w: FREQ=%s", ErrUnsupported, value)
			}
			r.Freq, haveFreq = freq, true
		case "INTERVAL":
			r.Interval, err = parseBoundedInt(value, 1, 1000)
		case "COUNT":
			r.Count, err = parseBoundedInt(value, 1, MaxCount)
		case "UNTIL":
			untilRaw = value
		case "WKST":
			wd, ok := weekdayCodes[strings.ToUpper(value)]
			if !ok {
				return Recurrence{}, fmt.Errorf("%w: WKST=%s is not a weekday code", ErrMalformed, value)
			}
			r.WkSt = wd
		case "BYDAY":
			r.ByDay, err = parseByDay(value)
		case "BYMONTHDAY":
			r.ByMonthDay, err = parseIntList(value, -31, 31, false)
		case "BYMONTH":
			r.ByMonth, err = parseIntList(value, 1, 12, true)
		case "BYHOUR":
			r.ByHour, err = parseIntList(value, 0, 23, true)
		case "BYMINUTE":
			r.ByMinute, err = parseIntList(value, 0, 59, true)
		case "BYSETPOS":
			r.BySetPos, err = parseIntList(value, -366, 366, false)
		default:
			return Recurrence{}, fmt.Errorf("%w: unknown rule part %s", ErrMalformed, name)
		}
		if err != nil {
			return Recurrence{}, fmt.Errorf("%w: %s: %s", ErrMalformed, name, err)
		}
	}

	if !haveFreq {
		return Recurrence{}, fmt.Errorf("%w: FREQ is required", ErrMalformed)
	}
	if r.Count > 0 && untilRaw != "" {
		return Recurrence{}, fmt.Errorf("%w: COUNT and UNTIL are mutually exclusive", ErrMalformed)
	}
	if untilRaw != "" {
		until, isUTC, err := parseUntil(untilRaw)
		if err != nil {
			return Recurrence{}, err
		}
		r.Until, r.UntilIsUTC = until, isUTC
	}
	if err := r.validateCombinations(); err != nil {
		return Recurrence{}, err
	}
	return r, nil
}

// validateCombinations enforces the rules about how parts combine, which
// are separate from whether each part is individually well-formed.
//
// These are refusals rather than silent normalisations on purpose. An
// ordinal BYDAY under a WEEKLY rule ("the second Monday of the week") has
// no meaning; dateutil resolves it one way, other implementations another,
// and any choice this package made would be a divergence from AWX for some
// input. Refusing is the only answer that cannot silently disagree.
func (r Recurrence) validateCombinations() error {
	if len(r.BySetPos) > 0 && !r.hasNarrowingPart() {
		return fmt.Errorf("%w: BYSETPOS needs another BY part to select from", ErrMalformed)
	}
	for _, wd := range r.ByDay {
		if wd.Ordinal != 0 && r.Freq != Monthly && r.Freq != Yearly {
			return fmt.Errorf("%w: an ordinal BYDAY (%s) is only meaningful with FREQ=MONTHLY or FREQ=YEARLY, not %s",
				ErrMalformed, formatWeekdayNum(wd), r.Freq)
		}
	}
	return nil
}

// hasNarrowingPart reports whether any BY part other than BYSETPOS is set.
func (r Recurrence) hasNarrowingPart() bool {
	return len(r.ByDay) > 0 || len(r.ByMonthDay) > 0 || len(r.ByMonth) > 0 ||
		len(r.ByHour) > 0 || len(r.ByMinute) > 0
}

// parseBoundedInt parses a decimal integer and requires it to land within
// [min,max] inclusive.
func parseBoundedInt(s string, min, max int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not an integer", s)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%d is outside [%d,%d]", n, min, max)
	}
	return n, nil
}

// parseIntList parses a comma-separated integer list, bounding each entry
// to [min,max]. When allowZero is false, 0 is refused: RFC 5545 uses
// negative values to count from the end of a period, and zero names no
// position from either end.
//
// The list length is bounded by the value range itself, since a list may
// not repeat a value -- so no separate length cap is needed here.
func parseIntList(s string, min, max int, allowZero bool) ([]int, error) {
	if s == "" {
		return nil, errors.New("value is empty")
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, p := range parts {
		n, err := parseBoundedInt(strings.TrimSpace(p), min, max)
		if err != nil {
			return nil, err
		}
		if n == 0 && !allowZero {
			return nil, errors.New("0 names no position; use a negative value to count from the end")
		}
		if seen[n] {
			return nil, fmt.Errorf("%d appears more than once", n)
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// parseByDay parses a BYDAY list: comma-separated weekday codes, each
// optionally prefixed by a signed ordinal ("MO", "1MO", "-1FR").
func parseByDay(s string) ([]WeekdayNum, error) {
	if s == "" {
		return nil, errors.New("value is empty")
	}
	parts := strings.Split(s, ",")
	out := make([]WeekdayNum, 0, len(parts))
	seen := make(map[WeekdayNum]bool, len(parts))
	for _, p := range parts {
		p = strings.ToUpper(strings.TrimSpace(p))
		if len(p) < 2 {
			return nil, fmt.Errorf("%q is too short to be a weekday", p)
		}
		code := p[len(p)-2:]
		wd, ok := weekdayCodes[code]
		if !ok {
			return nil, fmt.Errorf("%q does not end in a weekday code", p)
		}
		entry := WeekdayNum{Weekday: wd}
		if ordinalText := p[:len(p)-2]; ordinalText != "" {
			// The ordinal range is RFC 5545's own: a weekday can occur at
			// most 53 times in a year, and at most 5 times in a month, so
			// 53 is the widest meaningful bound across the frequencies
			// that accept an ordinal at all.
			ord, err := parseBoundedInt(ordinalText, -53, 53)
			if err != nil {
				return nil, fmt.Errorf("ordinal %q: %s", ordinalText, err)
			}
			if ord == 0 {
				return nil, errors.New("0 names no occurrence; omit the ordinal for every weekday")
			}
			entry.Ordinal = ord
		}
		if seen[entry] {
			return nil, fmt.Errorf("%s appears more than once", formatWeekdayNum(entry))
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out, nil
}

// parseUntil parses an UNTIL value in RFC 5545's three permitted shapes:
// a UTC date-time ("20240301T090000Z"), a floating date-time
// ("20240301T090000"), and a bare date ("20240301").
//
// The returned bool records whether the value was UTC-qualified. A
// floating UNTIL is returned as a wall-clock time carrying time.UTC as a
// placeholder location, which Expand re-anchors into the expansion's own
// location -- it is not a claim that the value is UTC, which is exactly
// why the bool exists rather than callers inspecting Location().
func parseUntil(s string) (time.Time, bool, error) {
	for _, layout := range []struct {
		format string
		utc    bool
	}{
		{"20060102T150405Z", true},
		{"20060102T150405", false},
		{"20060102", false},
	} {
		t, err := time.ParseInLocation(layout.format, s, time.UTC)
		if err == nil {
			return t, layout.utc, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("%w: UNTIL=%s is not an RFC 5545 date or date-time", ErrMalformed, s)
}

// formatWeekdayNum renders one BYDAY entry back to its RFC 5545 spelling.
func formatWeekdayNum(w WeekdayNum) string {
	if w.Ordinal == 0 {
		return weekdayNames[w.Weekday]
	}
	return strconv.Itoa(w.Ordinal) + weekdayNames[w.Weekday]
}
