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
