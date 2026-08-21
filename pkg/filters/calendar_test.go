package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestIsPast(t *testing.T) {
	cases := []struct {
		name      string
		iso, asOf string
		want      bool
	}{
		{"strictly_before", "2020-01-01T00:00:00Z", "2024-01-01T00:00:00Z", true},
		{"strictly_after", "2025-01-01T00:00:00Z", "2024-01-01T00:00:00Z", false},
		{"equal_is_not_past", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", false},
		{"malformed_iso", "nope", "2024-01-01T00:00:00Z", false},
		{"malformed_asof", "2024-01-01T00:00:00Z", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsPast(tc.iso, tc.asOf); got != tc.want {
				t.Errorf("IsPast(%q, %q) = %v, want %v", tc.iso, tc.asOf, got, tc.want)
			}
		})
	}
}

func TestIsFuture(t *testing.T) {
	cases := []struct {
		name      string
		iso, asOf string
		want      bool
	}{
		{"strictly_after", "2025-01-01T00:00:00Z", "2024-01-01T00:00:00Z", true},
		{"strictly_before", "2020-01-01T00:00:00Z", "2024-01-01T00:00:00Z", false},
		{"equal_is_not_future", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", false},
		{"malformed_iso", "nope", "2024-01-01T00:00:00Z", false},
		{"malformed_asof", "2024-01-01T00:00:00Z", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsFuture(tc.iso, tc.asOf); got != tc.want {
				t.Errorf("IsFuture(%q, %q) = %v, want %v", tc.iso, tc.asOf, got, tc.want)
			}
		})
	}
}

func TestIsOlderThan(t *testing.T) {
	cases := []struct {
		name             string
		iso, asOf        string
		thresholdSeconds int
		want             bool
	}{
		{"exactly_at_threshold", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 3600, true},
		{"older_than_threshold", "2024-01-01T00:00:00Z", "2024-01-01T02:00:00Z", 3600, true},
		{"newer_than_threshold", "2024-01-01T00:30:00Z", "2024-01-01T01:00:00Z", 3600, false},
		{"iso_after_asof", "2024-01-01T02:00:00Z", "2024-01-01T01:00:00Z", 0, false},
		{"malformed_iso", "nope", "2024-01-01T01:00:00Z", 3600, false},
		{"malformed_asof", "2024-01-01T01:00:00Z", "nope", 3600, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsOlderThan(tc.iso, tc.asOf, tc.thresholdSeconds); got != tc.want {
				t.Errorf("IsOlderThan(%q, %q, %d) = %v, want %v", tc.iso, tc.asOf, tc.thresholdSeconds, got, tc.want)
			}
		})
	}
}

func TestIsExpiringWithin(t *testing.T) {
	cases := []struct {
		name          string
		iso, asOf     string
		windowSeconds int
		want          bool
	}{
		{"within_window", "2024-01-01T00:30:00Z", "2024-01-01T00:00:00Z", 3600, true},
		{"exactly_at_window_edge", "2024-01-01T01:00:00Z", "2024-01-01T00:00:00Z", 3600, true},
		{"beyond_window", "2024-01-01T02:00:00Z", "2024-01-01T00:00:00Z", 3600, false},
		{"already_past_does_not_count", "2023-12-31T23:00:00Z", "2024-01-01T00:00:00Z", 3600, false},
		{"malformed_iso", "nope", "2024-01-01T00:00:00Z", 3600, false},
		{"malformed_asof", "2024-01-01T00:00:00Z", "nope", 3600, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsExpiringWithin(tc.iso, tc.asOf, tc.windowSeconds); got != tc.want {
				t.Errorf("IsExpiringWithin(%q, %q, %d) = %v, want %v", tc.iso, tc.asOf, tc.windowSeconds, got, tc.want)
			}
		})
	}
}

func TestStartOfDay(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"floors_within_day", "2024-01-01T13:45:30Z", "2024-01-01T00:00:00Z"},
		{"already_midnight", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z"},
		{"malformed", "nope", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.StartOfDay(tc.in); got != tc.want {
				t.Errorf("StartOfDay(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStartOfWeek(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// 2024-01-01 is a Monday; 2024-01-03 is the Wednesday of that
		// same week; 2024-01-07 is the following Sunday, still the same
		// ISO week as the Monday it belongs to.
		{"wednesday_floors_to_monday", "2024-01-03T13:45:00Z", "2024-01-01T00:00:00Z"},
		{"sunday_floors_to_preceding_monday", "2024-01-07T23:59:00Z", "2024-01-01T00:00:00Z"},
		{"already_monday_midnight", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z"},
		{"malformed", "nope", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.StartOfWeek(tc.in); got != tc.want {
				t.Errorf("StartOfWeek(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStartOfMonth(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"floors_within_month", "2024-01-15T13:45:00Z", "2024-01-01T00:00:00Z"},
		{"already_first", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z"},
		{"malformed", "nope", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.StartOfMonth(tc.in); got != tc.want {
				t.Errorf("StartOfMonth(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsLeapYear(t *testing.T) {
	cases := []struct {
		name string
		year int
		want bool
	}{
		{"divisible_by_4_not_100", 2024, true},
		{"divisible_by_100_not_400", 1900, false},
		{"divisible_by_400", 2000, true},
		{"not_divisible_by_4", 2023, false},
		{"divisible_by_400_recent", 2400, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsLeapYear(tc.year); got != tc.want {
				t.Errorf("IsLeapYear(%d) = %v, want %v", tc.year, got, tc.want)
			}
		})
	}
}

func TestDayOfWeek(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"monday", "2024-01-01T00:00:00Z", "Monday"},
		{"sunday", "2024-01-07T00:00:00Z", "Sunday"},
		{"malformed", "nope", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.DayOfWeek(tc.in); got != tc.want {
				t.Errorf("DayOfWeek(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsBusinessHour(t *testing.T) {
	schedule := map[string]any{"start": "09:00", "end": "17:00"}
	scheduleWithDays := map[string]any{"start": "09:00", "end": "17:00", "days": []any{"Mon", "Wed", "Fri"}}

	cases := []struct {
		name     string
		schedule map[string]any
		iso      string
		want     bool
	}{
		{"monday_within_default_hours", schedule, "2024-01-01T10:00:00Z", true},
		{"monday_before_hours", schedule, "2024-01-01T08:59:00Z", false},
		{"monday_at_start_inclusive", schedule, "2024-01-01T09:00:00Z", true},
		{"monday_at_end_inclusive", schedule, "2024-01-01T17:00:00Z", true},
		{"monday_after_hours", schedule, "2024-01-01T17:01:00Z", false},
		// 2024-01-06 is a Saturday, outside the Mon-Fri default.
		{"saturday_defaults_excluded", schedule, "2024-01-06T10:00:00Z", false},
		{"explicit_days_matches", scheduleWithDays, "2024-01-03T10:00:00Z", true},   // Wednesday
		{"explicit_days_excludes", scheduleWithDays, "2024-01-02T10:00:00Z", false}, // Tuesday
		{"malformed_timestamp", schedule, "nope", false},
		{"missing_start", map[string]any{"end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"missing_end", map[string]any{"start": "09:00"}, "2024-01-01T10:00:00Z", false},
		{"start_wrong_type", map[string]any{"start": 9, "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"malformed_hhmm_start", map[string]any{"start": "9am", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"malformed_hhmm_end", map[string]any{"start": "09:00", "end": "5pm"}, "2024-01-01T10:00:00Z", false},
		{"hhmm_no_colon", map[string]any{"start": "0900", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"hhmm_hour_non_numeric", map[string]any{"start": "ab:00", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"hhmm_hour_out_of_range", map[string]any{"start": "25:00", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"hhmm_minute_non_numeric", map[string]any{"start": "09:ab", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"hhmm_minute_out_of_range", map[string]any{"start": "09:99", "end": "17:00"}, "2024-01-01T10:00:00Z", false},
		{"overnight_window_not_supported", map[string]any{"start": "22:00", "end": "06:00"}, "2024-01-01T23:00:00Z", false},
		{"days_wrong_type_is_malformed", map[string]any{"start": "09:00", "end": "17:00", "days": "Mon"}, "2024-01-01T10:00:00Z", false},
		{"days_entries_case_insensitive_full_name", map[string]any{"start": "09:00", "end": "17:00", "days": []any{"monday"}}, "2024-01-01T10:00:00Z", true},
		{"days_entry_too_short_to_match", map[string]any{"start": "09:00", "end": "17:00", "days": []any{"Mo"}}, "2024-01-01T10:00:00Z", false},
		{"days_entry_wrong_element_type_skipped", map[string]any{"start": "09:00", "end": "17:00", "days": []any{5, "Mon"}}, "2024-01-01T10:00:00Z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsBusinessHour(tc.schedule, tc.iso); got != tc.want {
				t.Errorf("IsBusinessHour(%v, %q) = %v, want %v", tc.schedule, tc.iso, got, tc.want)
			}
		})
	}
}

func TestIsMaintenanceWindow(t *testing.T) {
	window := map[string]any{"start": "2024-01-01T00:00:00Z", "end": "2024-01-02T00:00:00Z"}

	cases := []struct {
		name   string
		window map[string]any
		iso    string
		want   bool
	}{
		{"within_window", window, "2024-01-01T12:00:00Z", true},
		{"at_start_inclusive", window, "2024-01-01T00:00:00Z", true},
		{"at_end_inclusive", window, "2024-01-02T00:00:00Z", true},
		{"before_window", window, "2023-12-31T23:59:59Z", false},
		{"after_window", window, "2024-01-02T00:00:01Z", false},
		{"malformed_timestamp", window, "nope", false},
		{"missing_start", map[string]any{"end": "2024-01-02T00:00:00Z"}, "2024-01-01T12:00:00Z", false},
		{"missing_end", map[string]any{"start": "2024-01-01T00:00:00Z"}, "2024-01-01T12:00:00Z", false},
		{"start_malformed_value", map[string]any{"start": "not-a-timestamp", "end": "2024-01-02T00:00:00Z"}, "2024-01-01T12:00:00Z", false},
		{"end_malformed_value", map[string]any{"start": "2024-01-01T00:00:00Z", "end": "not-a-timestamp"}, "2024-01-01T12:00:00Z", false},
		{"start_after_end", map[string]any{"start": "2024-01-02T00:00:00Z", "end": "2024-01-01T00:00:00Z"}, "2024-01-01T12:00:00Z", false},
		{"start_wrong_type", map[string]any{"start": 1, "end": "2024-01-02T00:00:00Z"}, "2024-01-01T12:00:00Z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsMaintenanceWindow(tc.window, tc.iso); got != tc.want {
				t.Errorf("IsMaintenanceWindow(%v, %q) = %v, want %v", tc.window, tc.iso, got, tc.want)
			}
		})
	}
}
