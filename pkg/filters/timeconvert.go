package filters

import (
	"strings"
	"time"
)

// windowsFileTimeEpochDeltaSeconds is the number of seconds between the
// Windows FileTime epoch (1601-01-01T00:00:00Z) and the Unix epoch
// (1970-01-01T00:00:00Z).
const windowsFileTimeEpochDeltaSeconds = 11644473600

// windowsFileTimeTicksPerSecond is the number of 100 nanosecond
// intervals ("ticks") in one second, the unit a Windows FileTime counts
// in.
const windowsFileTimeTicksPerSecond = 10_000_000

// maxDeltaSeconds bounds how far AddSeconds will shift a timestamp:
// 100 years, in seconds. Without a bound, time.Duration(seconds) *
// time.Second overflows int64 nanoseconds for a seconds argument beyond
// roughly 292 years, silently wrapping into a nonsensical result rather
// than an obviously wrong one; refusing well inside that ceiling turns a
// silent wraparound into an honest "".
const maxDeltaSeconds = 100 * 365 * 24 * 3600

// parseISO8601 parses s as RFC 3339 (this package's own working
// definition of "ISO8601": the profile of ISO 8601 the standard library
// and this codebase's own timestamps already use, not the full ISO 8601
// grammar's week-dates or date-only forms), after bounding its length
// and trimming surrounding whitespace the same way every other flat-
// string filter in this package does. Go's time.Parse accepts a
// fractional-second suffix on this layout even though the layout string
// itself does not spell one out, so "2024-01-01T00:00:00.123456Z" parses
// too.
func parseISO8601(s string) (time.Time, bool) {
	if len(s) > MaxInputBytes {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// EpochToISO8601 converts a Unix epoch in seconds to an RFC 3339
// timestamp string in UTC. Every int is a well-defined input (there is
// no malformed epoch), so this always returns a real timestamp string;
// an epoch far outside any realistic device-reported range is not
// rejected, the same way EpochToFileTime and FileTimeToEpoch below treat
// their own inputs as a total linear transform rather than a validated
// one.
func EpochToISO8601(epoch int) string {
	return time.Unix(int64(epoch), 0).UTC().Format(time.RFC3339)
}

// ISO8601ToEpoch parses iso into a Unix epoch in seconds, returning -1
// if iso is malformed or predates 1970-01-01T00:00:00Z. -1 is never a
// real result under that restriction: every device and event timestamp
// this platform manages is realistically after 1970, the same
// documented-domain-restriction judgment call IPToInt/IntToIP already
// make for their own sentinel (pkg/filters/network.go). A genuine
// pre-1970 timestamp is exactly the kind of input DeltaSeconds/DeltaDays
// below take a fallback argument for instead, because a delta between
// two timestamps is legitimately signed and small in magnitude, with no
// realistic-domain restriction available to carve a sentinel out of.
func ISO8601ToEpoch(iso string) int {
	t, ok := parseISO8601(iso)
	if !ok {
		return -1
	}
	epoch := t.Unix()
	if epoch < 0 {
		return -1
	}
	return int(epoch)
}

// FileTimeToEpoch converts a Windows FileTime (the count of 100
// nanosecond intervals since 1601-01-01T00:00:00Z, the value
// win.feature.* and similar facts report registry/WMI timestamps as) to
// a Unix epoch in whole seconds, truncating any sub-second remainder
// toward zero the way Go's own integer division does.
//
// Unlike ISO8601ToEpoch, this is a total function with no invalid input
// and no sentinel: fileTime is a plain integer, not a string with its
// own parseable grammar, so there is no "malformed" state to detect
// short of a realistic-domain restriction, and here that restriction
// would not even be safe, because a legitimate FileTime predating 1970
// (any date from 1601 through 1969) converts to a genuinely negative
// epoch. A negative epoch is FileTimeToEpoch's correct answer for such
// an input, not a signal that something went wrong.
func FileTimeToEpoch(fileTime int) int {
	return fileTime/windowsFileTimeTicksPerSecond - windowsFileTimeEpochDeltaSeconds
}

// EpochToFileTime converts a Unix epoch in seconds to a Windows
// FileTime, the inverse of FileTimeToEpoch. Also total, for the same
// reason: every int is a well-defined FileTime under this linear
// transform.
//
// The round trip EpochToFileTime(FileTimeToEpoch(fileTime)) equals
// fileTime only when fileTime is an exact multiple of
// windowsFileTimeTicksPerSecond; FileTimeToEpoch's own truncation of the
// sub-second remainder is real and asymmetric, the same category of
// documented one-way lossiness CompareSemVer's own doc comment states
// for its prefix-cut truncation.
func EpochToFileTime(epoch int) int {
	return (epoch + windowsFileTimeEpochDeltaSeconds) * windowsFileTimeTicksPerSecond
}

// ShiftTimezone reformats iso in the named IANA timezone (e.g.
// "America/New_York", "UTC"), returning "" if iso is malformed or tz
// does not name a real timezone.
func ShiftTimezone(iso, tz string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	if len(tz) > MaxInputBytes {
		return ""
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return ""
	}
	return t.In(loc).Format(time.RFC3339)
}

// AddSeconds adds seconds (negative to subtract) to iso, returning "" if
// iso is malformed or seconds' magnitude exceeds maxDeltaSeconds.
func AddSeconds(iso string, seconds int) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	if seconds > maxDeltaSeconds || seconds < -maxDeltaSeconds {
		return ""
	}
	return t.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339)
}

// DeltaSeconds returns the whole seconds from a to b (negative if b
// precedes a), or fallback if either is malformed. A delta is
// legitimately signed and can legitimately equal any int, so there is no
// safe sentinel the way ISO8601ToEpoch's own pre-1970 restriction
// provides; fallback is a required argument the caller writes, the same
// present-but-malformed contract Phase 50's SafeInt/SafeFloat/SafeBool
// established.
func DeltaSeconds(a, b string, fallback int) int {
	ta, ok := parseISO8601(a)
	if !ok {
		return fallback
	}
	tb, ok := parseISO8601(b)
	if !ok {
		return fallback
	}
	return int(tb.Sub(ta).Seconds())
}

// DeltaDays returns the whole days from a to b (negative if b precedes
// a), truncated toward zero, or fallback if either is malformed. Same
// fallback rationale as DeltaSeconds.
func DeltaDays(a, b string, fallback int) int {
	ta, ok := parseISO8601(a)
	if !ok {
		return fallback
	}
	tb, ok := parseISO8601(b)
	if !ok {
		return fallback
	}
	return int(tb.Sub(ta).Hours() / 24)
}

// RoundToHour floors iso to the start of its own current hour, in iso's
// own timezone, returning "" if iso is malformed.
//
// This deliberately does not use time.Time.Truncate(time.Hour):
// Truncate operates on the time as an absolute duration since the zero
// time, not on its presentation form, so for a timezone whose UTC offset
// is not a whole number of hours (India, UTC+5:30; Nepal, UTC+5:45;
// several historical zones), Truncate(time.Hour) produces boundaries at
// :30 or :45 past the hour in local wall-clock terms, not top-of-the-
// hour at all. Rebuilding the timestamp from its own Year/Month/Day/Hour
// fields, in its own Location, floors to the hour a reader of that
// timezone actually sees on a clock, which is what "round to hour" means
// here. TestRoundToHour_NonWholeHourOffset proves this against exactly
// that class of zone.
func RoundToHour(iso string) string {
	t, ok := parseISO8601(iso)
	if !ok {
		return ""
	}
	rounded := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	return rounded.Format(time.RFC3339)
}

// HumanizeDuration renders seconds as a compact, day-aware human-
// readable duration such as "1d2h3m4s", omitting any zero-valued
// component except when seconds is exactly zero ("0s"). A negative
// seconds is rendered with a leading "-" over the same magnitude
// breakdown.
//
// This is deliberately not time.Duration.String(): that format has no
// concept of a day, so a week-long duration renders as "168h0m0s"
// instead of the "7d" a human reads at a glance, which is the entire
// point of a "human-readable" duration filter distinct from bare
// stringification.
func HumanizeDuration(seconds int) string {
	sign := ""
	n := seconds
	if n < 0 {
		sign = "-"
		n = -n
	}
	days := n / 86400
	hours := (n % 86400) / 3600
	minutes := (n % 3600) / 60
	secs := n % 60

	var b strings.Builder
	b.WriteString(sign)
	wrote := false
	if days > 0 {
		writeUnit(&b, days, "d")
		wrote = true
	}
	if hours > 0 {
		writeUnit(&b, hours, "h")
		wrote = true
	}
	if minutes > 0 {
		writeUnit(&b, minutes, "m")
		wrote = true
	}
	if secs > 0 || !wrote {
		writeUnit(&b, secs, "s")
	}
	return b.String()
}

func writeUnit(b *strings.Builder, n int, unit string) {
	b.WriteString(itoa(n))
	b.WriteString(unit)
}

// itoa avoids pulling in strconv solely for this one non-error-
// returning conversion; n is always non-negative here (HumanizeDuration
// splits the sign off before calling it).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

// BootTimeFromUptime subtracts uptimeSeconds from now, returning "" if
// now is malformed or uptimeSeconds is negative (a device's uptime is
// never negative, so a negative input is treated as malformed rather
// than as "the device booted in the future").
func BootTimeFromUptime(now string, uptimeSeconds int) string {
	t, ok := parseISO8601(now)
	if !ok {
		return ""
	}
	if uptimeSeconds < 0 {
		return ""
	}
	return t.Add(-time.Duration(uptimeSeconds) * time.Second).Format(time.RFC3339)
}

// UptimeFromBootTime returns the whole seconds from boot to now, or -1
// if either timestamp is malformed or boot is after now. A device's
// uptime is never negative in reality, so boot-after-now (whether from a
// bad reading or clock skew) is treated the same as a malformed
// timestamp: not a real uptime, sharing the one sentinel.
func UptimeFromBootTime(boot, now string) int {
	tb, ok := parseISO8601(boot)
	if !ok {
		return -1
	}
	tn, ok := parseISO8601(now)
	if !ok {
		return -1
	}
	delta := tn.Sub(tb)
	if delta < 0 {
		return -1
	}
	return int(delta.Seconds())
}
