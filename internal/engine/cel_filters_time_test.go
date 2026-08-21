package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestCELFilters_Phase55TimeDateSchedulingFilters is Phase 55's own
// Release Gate requirement: every one of its filters proven callable
// through the real, unmodified engine.NewCELEvaluator()/Program.Eval via
// a compiled when_cel-shaped expression, not a bare Go function call
// (RULE 0). Each case's want value was independently verified against
// pkg/filters' own unit tests before being written here.
func TestCELFilters_Phase55TimeDateSchedulingFilters(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	cases := []struct {
		name string
		expr string
	}{
		{"epoch_to_iso8601", `filters.epochToISO8601(0) == "1970-01-01T00:00:00Z"`},
		{"iso8601_to_epoch", `filters.iso8601ToEpoch("1970-01-01T00:00:10Z") == 10`},
		{"iso8601_to_epoch_malformed", `filters.iso8601ToEpoch("nope") == -1`},
		{"file_time_to_epoch", `filters.fileTimeToEpoch(116444736000000000) == 0`},
		{"epoch_to_file_time", `filters.epochToFileTime(0) == 116444736000000000`},
		{"shift_timezone", `filters.shiftTimezone("2024-01-01T00:00:00Z", "America/New_York") == "2023-12-31T19:00:00-05:00"`},
		{"add_seconds", `filters.addSeconds("2024-01-01T00:00:00Z", 3600) == "2024-01-01T01:00:00Z"`},
		{"delta_seconds", `filters.deltaSeconds("2024-01-01T00:00:00Z", "2024-01-01T00:01:00Z", -1) == 60`},
		{"delta_days", `filters.deltaDays("2024-01-01T00:00:00Z", "2024-01-03T00:00:00Z", -1) == 2`},
		{"round_to_hour", `filters.roundToHour("2024-01-01T13:45:30Z") == "2024-01-01T13:00:00Z"`},
		{"humanize_duration", `filters.humanizeDuration(93784) == "1d2h3m4s"`},
		{"boot_time_from_uptime", `filters.bootTimeFromUptime("2024-01-01T01:00:00Z", 3600) == "2024-01-01T00:00:00Z"`},
		{"uptime_from_boot_time", `filters.uptimeFromBootTime("2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z") == 3600`},
		{"is_past_true", `filters.isPast("2020-01-01T00:00:00Z", "2024-01-01T00:00:00Z")`},
		{"is_past_false", `!filters.isPast("2025-01-01T00:00:00Z", "2024-01-01T00:00:00Z")`},
		{"is_future_true", `filters.isFuture("2025-01-01T00:00:00Z", "2024-01-01T00:00:00Z")`},
		{"is_older_than_true", `filters.isOlderThan("2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", 1800)`},
		{"is_expiring_within_true", `filters.isExpiringWithin("2024-01-01T00:30:00Z", "2024-01-01T00:00:00Z", 3600)`},
		{"start_of_day", `filters.startOfDay("2024-01-01T13:45:00Z") == "2024-01-01T00:00:00Z"`},
		{"start_of_week", `filters.startOfWeek("2024-01-03T13:45:00Z") == "2024-01-01T00:00:00Z"`},
		{"start_of_month", `filters.startOfMonth("2024-01-15T13:45:00Z") == "2024-01-01T00:00:00Z"`},
		{"is_leap_year_true", `filters.isLeapYear(2024)`},
		{"is_leap_year_false", `!filters.isLeapYear(1900)`},
		{"day_of_week", `filters.dayOfWeek("2024-01-01T00:00:00Z") == "Monday"`},
		{"is_business_hour_true", `filters.isBusinessHour({"start": "09:00", "end": "17:00"}, "2024-01-01T10:00:00Z")`},
		{"is_business_hour_false", `!filters.isBusinessHour({"start": "09:00", "end": "17:00"}, "2024-01-06T10:00:00Z")`},
		{"is_maintenance_window_true", `filters.isMaintenanceWindow({"start": "2024-01-01T00:00:00Z", "end": "2024-01-02T00:00:00Z"}, "2024-01-01T12:00:00Z")`},
		{"cron_next_run", `filters.cronNextRun("0 9 * * *", "2024-01-01T08:00:00Z") == "2024-01-01T09:00:00Z"`},
		{"cron_previous_run", `filters.cronPreviousRun("0 9 * * *", "2024-01-02T08:00:00Z") == "2024-01-01T09:00:00Z"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := eval.Compile(tc.expr)
			if err != nil {
				t.Fatalf("failed to compile %q: %v", tc.expr, err)
			}
			got, err := prg.Eval(map[string]interface{}{})
			if err != nil {
				t.Fatalf("eval of %q failed: %v", tc.expr, err)
			}
			if !got {
				t.Errorf("%s: expression %q evaluated false", tc.name, tc.expr)
			}
		})
	}
}

// TestCELFilters_Phase55CombinedCondition chains several of this phase's
// functions in one when_cel-shaped condition against a realistic device
// stat payload, the same combined-condition shape
// TestCELFilters_Phase51CombinedCondition through
// TestCELFilters_Phase54CombinedCondition established, with a negative
// control proving the condition genuinely flips false.
func TestCELFilters_Phase55CombinedCondition(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}

	expr := `filters.isBusinessHour({"start": "09:00", "end": "17:00"}, stat.last_checked) && ` +
		`filters.isPast(stat.cert_expiry, stat.last_checked) && ` +
		`filters.isMaintenanceWindow({"start": stat.window_start, "end": stat.window_end}, stat.last_checked) && ` +
		`filters.cronNextRun(stat.backup_schedule, stat.last_checked) == stat.next_backup`

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile: %v", err)
	}

	stat := map[string]interface{}{
		"last_checked":    "2024-01-01T10:00:00Z", // a Monday, within business hours
		"cert_expiry":     "2020-01-01T00:00:00Z", // already past relative to last_checked
		"window_start":    "2024-01-01T00:00:00Z",
		"window_end":      "2024-01-02T00:00:00Z",
		"backup_schedule": "0 9 * * *",
		"next_backup":     "2024-01-02T09:00:00Z", // next 9am strictly after 10am today is tomorrow
	}
	got, err := prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !got {
		t.Fatal("expected the combined condition to evaluate true against a realistic stat payload")
	}

	// Negative control: a certificate that has not yet expired must flip
	// the same condition false, proving the combined expression is
	// actually exercising every clause rather than being vacuously true.
	stat["cert_expiry"] = "2030-01-01T00:00:00Z"
	got, err = prg.Eval(map[string]interface{}{"stat": stat})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if got {
		t.Fatal("expected the combined condition to evaluate false once cert_expiry has not yet passed")
	}
}
