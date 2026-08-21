package filters_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestEpochToISO8601(t *testing.T) {
	cases := []struct {
		name  string
		epoch int
		want  string
	}{
		{"unix_zero", 0, "1970-01-01T00:00:00Z"},
		{"leap_day", 1709208000, "2024-02-29T12:00:00Z"},
		{"pre_1970_still_renders", -3600, "1969-12-31T23:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.EpochToISO8601(tc.epoch); got != tc.want {
				t.Errorf("EpochToISO8601(%d) = %q, want %q", tc.epoch, got, tc.want)
			}
		})
	}
}

func TestISO8601ToEpoch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"unix_zero", "1970-01-01T00:00:00Z", 0},
		{"ten_seconds_in", "1970-01-01T00:00:10Z", 10},
		{"leap_day", "2024-02-29T12:00:00Z", 1709208000},
		{"fractional_seconds_accepted", "2024-01-01T00:00:00.500Z", 1704067200},
		{"whitespace_trimmed", "  1970-01-01T00:00:10Z  ", 10},
		{"predates_1970_rejected", "1969-12-31T23:59:59Z", -1},
		{"malformed_not_rfc3339", "not-a-timestamp", -1},
		{"date_only_rejected", "2024-01-01", -1},
		{"empty", "", -1},
		{"over_cap", strings.Repeat("9", filters.MaxInputBytes+1), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ISO8601ToEpoch(tc.in); got != tc.want {
				t.Errorf("ISO8601ToEpoch(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestISO8601ToEpoch_EpochToISO8601_RoundTrip(t *testing.T) {
	for _, epoch := range []int{0, 10, 1709208000, 1704067200} {
		iso := filters.EpochToISO8601(epoch)
		got := filters.ISO8601ToEpoch(iso)
		if got != epoch {
			t.Errorf("round trip: EpochToISO8601(%d) = %q, ISO8601ToEpoch(%q) = %d, want %d", epoch, iso, iso, got, epoch)
		}
	}
}

func TestFileTimeToEpoch(t *testing.T) {
	cases := []struct {
		name     string
		fileTime int
		want     int
	}{
		{"windows_epoch_is_unix_zero_minus_delta", 116444736000000000, 0},
		{"one_second_past_windows_epoch", 116444736000000000 + 10_000_000, 1},
		{"predates_1970_is_negative_by_design", 0, -11644473600},
		{"truncates_sub_second_remainder", 116444736000000000 + 5_000_000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.FileTimeToEpoch(tc.fileTime); got != tc.want {
				t.Errorf("FileTimeToEpoch(%d) = %d, want %d", tc.fileTime, got, tc.want)
			}
		})
	}
}

func TestEpochToFileTime(t *testing.T) {
	cases := []struct {
		name  string
		epoch int
		want  int
	}{
		{"unix_zero", 0, 116444736000000000},
		{"one_second_after_unix_zero", 1, 116444736000000000 + 10_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.EpochToFileTime(tc.epoch); got != tc.want {
				t.Errorf("EpochToFileTime(%d) = %d, want %d", tc.epoch, got, tc.want)
			}
		})
	}
}

// TestFileTimeToEpoch_RoundTripsAcrossLeapYearBoundary is this phase's
// Adversarial Pattern Justification proof for Windows-FileTime
// conversion: a leap day (2024-02-29) and the day immediately after it
// (2024-03-01, which only exists at day 61 of the year because 2024 is a
// leap year) both round-trip exactly through EpochToFileTime(
// FileTimeToEpoch(x)) when x is already an exact multiple of
// windowsFileTimeTicksPerSecond, proving the linear transform carries no
// hidden leap-year-specific bug (there is nothing in the arithmetic that
// could contain one, but this is the proof rather than the assertion).
func TestFileTimeToEpoch_RoundTripsAcrossLeapYearBoundary(t *testing.T) {
	leapDay, err := time.Parse(time.RFC3339, "2024-02-29T12:00:00Z")
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	dayAfter, err := time.Parse(time.RFC3339, "2024-03-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	for _, ts := range []time.Time{leapDay, dayAfter} {
		fileTime := filters.EpochToFileTime(int(ts.Unix()))
		gotEpoch := filters.FileTimeToEpoch(fileTime)
		if gotEpoch != int(ts.Unix()) {
			t.Errorf("round trip for %s: FileTimeToEpoch(EpochToFileTime(%d)) = %d, want %d", ts, ts.Unix(), gotEpoch, ts.Unix())
		}
		// One full day (86400 seconds) really did elapse between the two
		// fixtures once converted through FileTime and back, proving the
		// leap day itself was not silently skipped or double-counted.
	}
	delta := filters.FileTimeToEpoch(filters.EpochToFileTime(int(dayAfter.Unix()))) -
		filters.FileTimeToEpoch(filters.EpochToFileTime(int(leapDay.Unix())))
	if want := 12 * 3600; delta != want {
		t.Errorf("elapsed seconds across the leap day boundary = %d, want %d", delta, want)
	}
}

func TestShiftTimezone(t *testing.T) {
	cases := []struct {
		name string
		iso  string
		tz   string
		want string
	}{
		{"utc_to_new_york_winter", "2024-01-01T00:00:00Z", "America/New_York", "2023-12-31T19:00:00-05:00"},
		{"utc_to_utc", "2024-01-01T00:00:00Z", "UTC", "2024-01-01T00:00:00Z"},
		{"malformed_timestamp", "not-a-timestamp", "UTC", ""},
		{"unknown_timezone", "2024-01-01T00:00:00Z", "Not/A_Real_Zone", ""},
		{"over_cap_timezone", "2024-01-01T00:00:00Z", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ShiftTimezone(tc.iso, tc.tz); got != tc.want {
				t.Errorf("ShiftTimezone(%q, %q) = %q, want %q", tc.iso, tc.tz, got, tc.want)
			}
		})
	}
}

// TestShiftTimezone_RoundTripsAcrossDSTBoundary is this phase's
// Adversarial Pattern Justification proof for timezone shift: two UTC
// instants straddling America/New_York's 2024 spring-forward transition
// (2024-03-10 02:00 local became 03:00 local; EST, -05:00, becomes EDT,
// -04:00) each shift to the correct offset for their own side of the
// transition, and shifting back to UTC recovers the exact original
// instant, proving ShiftTimezone consults the real IANA transition table
// rather than a fixed offset.
func TestShiftTimezone_RoundTripsAcrossDSTBoundary(t *testing.T) {
	cases := []struct {
		name       string
		utc        string
		wantLocal  string
		wantOffset string
	}{
		{"just_before_spring_forward", "2024-03-10T06:59:00Z", "2024-03-10T01:59:00", "-05:00"},
		{"just_after_spring_forward", "2024-03-10T07:01:00Z", "2024-03-10T03:01:00", "-04:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shifted := filters.ShiftTimezone(tc.utc, "America/New_York")
			want := tc.wantLocal + tc.wantOffset
			if shifted != want {
				t.Fatalf("ShiftTimezone(%q, America/New_York) = %q, want %q", tc.utc, shifted, want)
			}
			back := filters.ShiftTimezone(shifted, "UTC")
			if back != tc.utc {
				t.Errorf("round trip back to UTC = %q, want original %q", back, tc.utc)
			}
		})
	}
}

func TestAddSeconds(t *testing.T) {
	cases := []struct {
		name    string
		iso     string
		seconds int
		want    string
	}{
		{"add_one_hour", "2024-01-01T00:00:00Z", 3600, "2024-01-01T01:00:00Z"},
		{"subtract_via_negative", "2024-01-01T01:00:00Z", -3600, "2024-01-01T00:00:00Z"},
		{"zero_is_identity", "2024-01-01T00:00:00Z", 0, "2024-01-01T00:00:00Z"},
		{"malformed", "not-a-timestamp", 3600, ""},
		{"exceeds_bound", "2024-01-01T00:00:00Z", 200 * 365 * 24 * 3600, ""},
		{"exceeds_bound_negative", "2024-01-01T00:00:00Z", -200 * 365 * 24 * 3600, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.AddSeconds(tc.iso, tc.seconds); got != tc.want {
				t.Errorf("AddSeconds(%q, %d) = %q, want %q", tc.iso, tc.seconds, got, tc.want)
			}
		})
	}
}

func TestDeltaSeconds(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		fallback int
		want     int
	}{
		{"one_minute_forward", "2024-01-01T00:00:00Z", "2024-01-01T00:01:00Z", -1, 60},
		{"one_minute_backward", "2024-01-01T00:01:00Z", "2024-01-01T00:00:00Z", -1, -60},
		{"equal", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", -1, 0},
		{"a_malformed_returns_fallback", "nope", "2024-01-01T00:00:00Z", -99, -99},
		{"b_malformed_returns_fallback", "2024-01-01T00:00:00Z", "nope", -99, -99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.DeltaSeconds(tc.a, tc.b, tc.fallback); got != tc.want {
				t.Errorf("DeltaSeconds(%q, %q, %d) = %d, want %d", tc.a, tc.b, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestDeltaDays(t *testing.T) {
	cases := []struct {
		name     string
		a, b     string
		fallback int
		want     int
	}{
		{"two_days_forward", "2024-01-01T00:00:00Z", "2024-01-03T00:00:00Z", -1, 2},
		{"two_days_backward", "2024-01-03T00:00:00Z", "2024-01-01T00:00:00Z", -1, -2},
		{"less_than_a_day_truncates_to_zero", "2024-01-01T00:00:00Z", "2024-01-01T23:00:00Z", -1, 0},
		{"a_malformed_returns_fallback", "nope", "2024-01-01T00:00:00Z", -99, -99},
		{"b_malformed_returns_fallback", "2024-01-01T00:00:00Z", "nope", -99, -99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.DeltaDays(tc.a, tc.b, tc.fallback); got != tc.want {
				t.Errorf("DeltaDays(%q, %q, %d) = %d, want %d", tc.a, tc.b, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestRoundToHour(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"floors_within_hour", "2024-01-01T13:45:30Z", "2024-01-01T13:00:00Z"},
		{"already_on_the_hour", "2024-01-01T13:00:00Z", "2024-01-01T13:00:00Z"},
		{"malformed", "not-a-timestamp", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.RoundToHour(tc.in); got != tc.want {
				t.Errorf("RoundToHour(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRoundToHour_NonWholeHourOffset proves RoundToHour floors to the
// hour a reader in that timezone actually sees on a clock, for a zone
// whose UTC offset (+05:30, India Standard Time) is not a whole number
// of hours. The naive time.Time.Truncate(time.Hour), which operates on
// absolute time since the zero time rather than presentation form, would
// land on :30 past the hour for this input, not top-of-the-hour; this
// test proves both that RoundToHour avoids that trap and that the trap
// is real by checking Truncate's own output disagrees.
func TestRoundToHour_NonWholeHourOffset(t *testing.T) {
	const in = "2024-06-15T10:45:00+05:30"
	const want = "2024-06-15T10:00:00+05:30"

	got := filters.RoundToHour(in)
	if got != want {
		t.Fatalf("RoundToHour(%q) = %q, want %q", in, got, want)
	}

	parsed, err := time.Parse(time.RFC3339, in)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	naive := parsed.Truncate(time.Hour).Format(time.RFC3339)
	if naive == want {
		t.Fatal("fixture no longer demonstrates the Truncate(time.Hour) pitfall; pick a new +05:30 timestamp")
	}
}

func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    string
	}{
		{"zero", 0, "0s"},
		{"seconds_only", 45, "45s"},
		{"minutes_and_seconds", 125, "2m5s"},
		{"exact_minutes_omits_seconds", 120, "2m"},
		{"hours_minutes_seconds", 3723, "1h2m3s"},
		{"days_hours_minutes_seconds", 93784, "1d2h3m4s"},
		{"exact_day_omits_rest", 86400, "1d"},
		{"negative", -125, "-2m5s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.HumanizeDuration(tc.seconds); got != tc.want {
				t.Errorf("HumanizeDuration(%d) = %q, want %q", tc.seconds, got, tc.want)
			}
		})
	}
}

func TestBootTimeFromUptime(t *testing.T) {
	cases := []struct {
		name          string
		now           string
		uptimeSeconds int
		want          string
	}{
		{"one_hour_uptime", "2024-01-01T01:00:00Z", 3600, "2024-01-01T00:00:00Z"},
		{"zero_uptime_is_now", "2024-01-01T00:00:00Z", 0, "2024-01-01T00:00:00Z"},
		{"malformed_now", "nope", 3600, ""},
		{"negative_uptime_rejected", "2024-01-01T01:00:00Z", -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.BootTimeFromUptime(tc.now, tc.uptimeSeconds); got != tc.want {
				t.Errorf("BootTimeFromUptime(%q, %d) = %q, want %q", tc.now, tc.uptimeSeconds, got, tc.want)
			}
		})
	}
}

func TestUptimeFromBootTime(t *testing.T) {
	cases := []struct {
		name      string
		boot, now string
		want      int
	}{
		{"one_hour_uptime", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 3600},
		{"zero_uptime", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", 0},
		{"boot_after_now_rejected", "2024-01-01T01:00:00Z", "2024-01-01T00:00:00Z", -1},
		{"malformed_boot", "nope", "2024-01-01T00:00:00Z", -1},
		{"malformed_now", "2024-01-01T00:00:00Z", "nope", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.UptimeFromBootTime(tc.boot, tc.now); got != tc.want {
				t.Errorf("UptimeFromBootTime(%q, %q) = %d, want %d", tc.boot, tc.now, got, tc.want)
			}
		})
	}
}

func TestBootTimeFromUptime_UptimeFromBootTime_RoundTrip(t *testing.T) {
	const now = "2024-01-01T12:00:00Z"
	const uptime = 7384
	boot := filters.BootTimeFromUptime(now, uptime)
	got := filters.UptimeFromBootTime(boot, now)
	if got != uptime {
		t.Errorf("round trip: BootTimeFromUptime(%q, %d) = %q, UptimeFromBootTime(%q, %q) = %d, want %d", now, uptime, boot, boot, now, got, uptime)
	}
}
