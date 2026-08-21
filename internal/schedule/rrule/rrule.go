// Package rrule is a deliberately bounded RFC 5545 recurrence engine: the
// subset of the specification an infrastructure scheduler actually needs,
// with everything else refused rather than silently mishandled.
//
// It is hand-rolled rather than delegated to a third-party library, which
// is the same call pkg/filters/cron.go's own 5-field parser made and for
// the same reasons: this repository ships a single static binary and keeps
// its supply chain small, and a permissive parser would have needed a
// fail-closed validator written on top of it anyway. What a dependency
// would genuinely have bought -- bug-for-bug agreement with the
// dateutil.rrule implementation AWX schedules on -- is bought instead by
// testing against golden vectors generated from dateutil itself
// (testdata/awx_parity.json, tools/genrrulefixtures).
//
// The constraint set is the point, not a gap. A recurrence rule is
// operator-supplied input reaching a component that expands it into work,
// so an unbounded or pathological rule is a resource-exhaustion vector.
// Parse refuses anything outside the accepted set at save time, so a rule
// that cannot be expanded safely can never reach storage, mirroring the
// save-time posture launch.Injectors.Validate already takes for templates.
//
// Accepted: FREQ (MINUTELY through YEARLY), INTERVAL, COUNT, UNTIL, WKST,
// BYDAY (with ordinals), BYMONTHDAY, BYMONTH, BYHOUR, BYMINUTE, BYSETPOS.
//
// Refused: SECONDLY, BYWEEKNO, BYYEARDAY, BYSECOND, RDATE, INTERVAL=0, a
// COUNT above MaxCount, and any rule bounded by neither COUNT nor UNTIL
// when the caller supplies no other bound.
//
// Time zones are the caller's, not this package's: every expansion runs
// in an explicit *time.Location and this package never reads the process
// zone. That is what makes the daylight-saving behaviour testable.
package rrule

import (
	"fmt"
	"time"
)

// Frequency is a recurrence's base period: the FREQ part, which every
// other part narrows rather than widens.
type Frequency int

// The frequencies this package accepts. SECONDLY is deliberately absent
// rather than defined-and-rejected: a value that cannot be constructed
// cannot be smuggled past a validator by a caller building a Recurrence
// literal instead of going through Parse.
const (
	Minutely Frequency = iota + 1
	Hourly
	Daily
	Weekly
	Monthly
	Yearly
)

// String renders a Frequency back into its RFC 5545 keyword, so a
// round-trip through Parse and String is stable and an error message can
// name the frequency the way the operator wrote it.
func (f Frequency) String() string {
	switch f {
	case Minutely:
		return "MINUTELY"
	case Hourly:
		return "HOURLY"
	case Daily:
		return "DAILY"
	case Weekly:
		return "WEEKLY"
	case Monthly:
		return "MONTHLY"
	case Yearly:
		return "YEARLY"
	default:
		return fmt.Sprintf("Frequency(%d)", int(f))
	}
}

// MaxCount bounds an accepted COUNT. It is not a statement about what a
// sane schedule looks like; it is the ceiling on how much work a single
// operator-supplied string may ask this package to materialise. A rule
// asking for more is refused at parse time rather than truncated at
// expansion time, because a truncated expansion is indistinguishable from
// a complete one at the call site.
const MaxCount = 10000

// WeekdayNum is one BYDAY entry: a weekday, optionally qualified by an
// ordinal ("-1FR" is the last Friday, "2MO" the second Monday).
//
// Ordinal 0 means unqualified ("FR", every Friday). The ordinal counts
// within the period the FREQ names -- a month for MONTHLY, a year for
// YEARLY -- and is meaningless for the shorter frequencies, which Parse
// refuses rather than ignores.
type WeekdayNum struct {
	Ordinal int
	Weekday time.Weekday
}

// Recurrence is a parsed, validated recurrence rule.
//
// A zero value is not a usable Recurrence: Freq has no valid zero and
// Interval must be at least 1. Construct one through Parse rather than as
// a literal, so the constraint set is enforced on every path.
type Recurrence struct {
	// Freq is the base period. Always set on a parsed Recurrence.
	Freq Frequency

	// Interval is how many Freq periods separate two candidate periods.
	// Always at least 1 on a parsed Recurrence; RFC 5545's default.
	Interval int

	// Count bounds the rule to a number of occurrences. Zero means unset,
	// which is why it is not a *int: RFC 5545 has no COUNT=0, so zero is
	// unambiguously "absent" rather than a meaningful value being shadowed.
	Count int

	// Until bounds the rule to an instant, inclusive. The zero Time means
	// unset. A parsed Recurrence never carries both Count and Until: RFC
	// 5545 forbids it and Parse enforces it.
	Until time.Time

	// UntilIsUTC records whether UNTIL carried a trailing Z. A floating
	// UNTIL is resolved against the expansion's location, so the same rule
	// means different instants in different zones -- which is the RFC's
	// behaviour, not an accident, and must survive parsing to be applied.
	UntilIsUTC bool

	// WkSt is the day a week starts on, which changes which occurrences a
	// WEEKLY rule with INTERVAL > 1 selects. RFC 5545's default is Monday,
	// and Parse applies that default rather than leaving the Go zero value
	// (Sunday) to mean something the operator did not write.
	WkSt time.Weekday

	// The BY* parts, each empty when absent. Order within a slice is not
	// significant to expansion, but is preserved as written so String can
	// round-trip.
	ByDay      []WeekdayNum
	ByMonthDay []int
	ByMonth    []int
	ByHour     []int
	ByMinute   []int

	// BySetPos selects from the occurrences one period produced, by
	// position, 1-based, negative counting from the end. RFC 5545 requires
	// it to accompany at least one other BY* part; Parse enforces that,
	// because BySetPos over an unnarrowed period is a no-op that reads
	// like a filter.
	BySetPos []int
}

// Bounded reports whether this rule terminates on its own, through COUNT
// or UNTIL, rather than running forever.
//
// An unbounded rule is legal RFC 5545 and genuinely wanted for a schedule
// that should simply keep running, so this is a question callers ask
// rather than a condition Parse refuses. internal/schedule pairs an
// unbounded rule with the schedule's own dtend, and every expansion entry
// point takes an explicit ceiling regardless, so no caller can ask this
// package for an infinite result.
func (r Recurrence) Bounded() bool {
	return r.Count > 0 || !r.Until.IsZero()
}
