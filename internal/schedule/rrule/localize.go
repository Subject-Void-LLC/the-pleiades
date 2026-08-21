package rrule

import "time"

// civil is a wall-clock reading with no zone attached, carried as a
// time.Time anchored in UTC.
//
// Representing it this way rather than as a struct of calendar fields is
// what lets the expansion reuse time.Date's calendar arithmetic -- month
// lengths, leap years, day overflow -- without any of it consulting a zone.
// UTC has no transitions, so wall-clock arithmetic in UTC is exact, which
// is precisely the property the recurrence walk needs.
type civil = time.Time

// toCivil strips the zone from a zoned time, keeping the wall clock.
func toCivil(t time.Time) civil {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, time.UTC)
}

// localize resolves a wall-clock reading into a real instant in loc,
// matching python-dateutil's behaviour exactly -- which is the whole reason
// this function exists rather than a bare time.Date call.
//
// Three cases, and the standard library only agrees with dateutil on two:
//
//   - The reading exists once. Both agree.
//
//   - The reading occurs twice, in the autumn fold. PEP 495 calls this
//     fold=0 and picks the FIRST, pre-transition instant; Go's time.Date
//     picks the same one. Both agree.
//
//   - The reading does not exist, in the spring gap. Here they diverge.
//     Go normalises using the post-transition offset, which moves the wall
//     clock backwards -- 02:30 in New York on 10 March 2024 comes back as
//     01:30. PEP 495's fold=0 instead keeps the wall clock and applies the
//     offset in effect BEFORE the transition, yielding 03:30 EDT. dateutil
//     does the latter, so AWX does the latter, so this does the latter.
//
// Getting the third case wrong is not a rounding error: an hourly rule
// walking through the gap collapses onto one repeated instant under Go's
// normalisation, because the normalised wall clock feeds back into the next
// step. That is why the recurrence walk keeps civil time and calls this
// only at the moment of emission.
func localize(c civil, loc *time.Location) time.Time {
	y, m, d := c.Date()
	hh, mm := c.Hour(), c.Minute()

	t := time.Date(y, m, d, hh, mm, 0, 0, loc)
	if ty, tm, td := t.Date(); ty == y && tm == m && td == d && t.Hour() == hh && t.Minute() == mm {
		// The wall clock survived, so the reading is real and Go's choice
		// already matches fold=0.
		return t
	}

	// The reading fell in a gap. Recover the offset in effect just before
	// the transition by asking what the same wall clock meant a day
	// earlier, then place the instant that many seconds off the reading.
	// A day is comfortably wider than any real transition and narrower
	// than any interval between two of them.
	_, offsetBefore := time.Date(y, m, d-1, hh, mm, 0, 0, loc).Zone()
	return time.Unix(c.Unix()-int64(offsetBefore), 0).In(loc)
}
