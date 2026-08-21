package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestIsValidCronExpr(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"every_minute", "* * * * *", true},
		{"specific_time", "30 4 * * *", true},
		{"step", "*/15 * * * *", true},
		{"range", "0 9-17 * * *", true},
		{"range_with_step", "0 9-17/2 * * *", true},
		{"list", "0,15,30,45 * * * *", true},
		{"weekday_range", "0 0 * * 1-5", true},
		{"too_few_fields", "* * * *", false},
		{"too_many_fields", "* * * * * *", false},
		{"minute_out_of_range", "60 * * * *", false},
		{"hour_out_of_range", "* 24 * * *", false},
		{"day_of_month_zero_rejected", "* * 0 * *", false},
		{"month_out_of_range", "* * * 13 *", false},
		{"day_of_week_out_of_range", "* * * * 7", false},
		{"non_numeric_field", "* * * jan *", false},
		{"empty", "", false},
		{"named_shorthand_rejected", "@daily", false},
		{"backwards_range_rejected", "17-9 * * * *", false},
		{"zero_step_rejected", "*/0 * * * *", false},
		{"invalid_step_non_numeric", "*/x * * * *", false},
		{"invalid_range_start_non_numeric", "x-5 * * * *", false},
		{"invalid_range_end_non_numeric", "5-x * * * *", false},
		{"empty_list_item_trailing_comma", "1,2, * * * *", false},
		{"empty_list_item_double_comma", "1,,3 * * * *", false},
		{"over_cap", strings.Repeat("* ", filters.MaxInputBytes), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsValidCronExpr(tc.in); got != tc.want {
				t.Errorf("IsValidCronExpr(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCronNextRun(t *testing.T) {
	cases := []struct {
		name     string
		cronExpr string
		fromISO  string
		want     string
	}{
		{"later_today", "0 9 * * *", "2024-01-01T08:00:00Z", "2024-01-01T09:00:00Z"},
		{"strictly_after_an_exact_match", "0 9 * * *", "2024-01-01T09:00:00Z", "2024-01-02T09:00:00Z"},
		{"weekly_monday", "0 9 * * 1", "2024-01-01T10:00:00Z", "2024-01-08T09:00:00Z"},
		{"step_minutes", "*/15 * * * *", "2024-01-01T00:07:00Z", "2024-01-01T00:15:00Z"},
		{"malformed_expr", "not a cron", "2024-01-01T00:00:00Z", ""},
		{"malformed_timestamp", "0 9 * * *", "nope", ""},
		{"unsatisfiable_expression_terminates_empty", "0 0 31 2 *", "2024-01-01T00:00:00Z", ""},
		{"over_cap_expr", strings.Repeat("* ", filters.MaxInputBytes), "2024-01-01T00:00:00Z", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.CronNextRun(tc.cronExpr, tc.fromISO); got != tc.want {
				t.Errorf("CronNextRun(%q, %q) = %q, want %q", tc.cronExpr, tc.fromISO, got, tc.want)
			}
		})
	}
}

func TestCronPreviousRun(t *testing.T) {
	cases := []struct {
		name     string
		cronExpr string
		fromISO  string
		want     string
	}{
		{"earlier_today", "0 9 * * *", "2024-01-01T10:00:00Z", "2024-01-01T09:00:00Z"},
		{"strictly_before_an_exact_match", "0 9 * * *", "2024-01-01T09:00:00Z", "2023-12-31T09:00:00Z"},
		{"weekly_monday", "0 9 * * 1", "2024-01-01T08:00:00Z", "2023-12-25T09:00:00Z"},
		{"malformed_expr", "not a cron", "2024-01-01T00:00:00Z", ""},
		{"malformed_timestamp", "0 9 * * *", "nope", ""},
		{"unsatisfiable_expression_terminates_empty", "0 0 31 2 *", "2024-01-01T00:00:00Z", ""},
		{"over_cap_expr", strings.Repeat("* ", filters.MaxInputBytes), "2024-01-01T00:00:00Z", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.CronPreviousRun(tc.cronExpr, tc.fromISO); got != tc.want {
				t.Errorf("CronPreviousRun(%q, %q) = %q, want %q", tc.cronExpr, tc.fromISO, got, tc.want)
			}
		})
	}
}

func TestCronNextRun_PreviousRun_RoundTrip(t *testing.T) {
	const expr = "0 9 * * *"
	const t0 = "2024-01-05T09:00:00Z"

	prev := filters.CronPreviousRun(expr, t0)
	if prev != "2024-01-04T09:00:00Z" {
		t.Fatalf("CronPreviousRun(%q, %q) = %q, want 2024-01-04T09:00:00Z", expr, t0, prev)
	}
	if got := filters.CronNextRun(expr, prev); got != t0 {
		t.Errorf("CronNextRun(%q, %q) = %q, want %q", expr, prev, got, t0)
	}

	next := filters.CronNextRun(expr, t0)
	if next != "2024-01-06T09:00:00Z" {
		t.Fatalf("CronNextRun(%q, %q) = %q, want 2024-01-06T09:00:00Z", expr, t0, next)
	}
	if got := filters.CronPreviousRun(expr, next); got != t0 {
		t.Errorf("CronPreviousRun(%q, %q) = %q, want %q", expr, next, got, t0)
	}
}

// TestCronNextRun_DayFieldsUseCronsRealORSemantics proves cronSchedule's
// domWildcard/dowWildcard bookkeeping is real and load-bearing, not
// decoration: "0 0 1 * 1" means midnight on the 1st of the month OR any
// Monday (standard cron(8) day-field behavior when both fields are
// restricted), not AND. 2024-01-01 is itself a Monday that is also the
// 1st, so searching strictly after it, an AND reading would have to wait
// for a future 1st-of-month that also falls on a Monday (2024-04-01, the
// next one) while the correct OR reading finds the very next Monday,
// 2024-01-08, a full three months sooner. The two readings disagreeing
// this far apart is what makes this a real adversarial proof rather than
// a coincidental match.
func TestCronNextRun_DayFieldsUseCronsRealORSemantics(t *testing.T) {
	const expr = "0 0 1 * 1"
	const from = "2024-01-01T00:00:01Z"

	got := filters.CronNextRun(expr, from)
	const wantOR = "2024-01-08T00:00:00Z"
	const wantIfItWereAND = "2024-04-01T00:00:00Z"

	if got != wantOR {
		t.Fatalf("CronNextRun(%q, %q) = %q, want %q (cron's real OR semantics)", expr, from, got, wantOR)
	}
	if got == wantIfItWereAND {
		t.Fatal("result matches the AND reading; the OR/AND distinction this test exists to prove did not actually run")
	}
}
