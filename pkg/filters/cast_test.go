package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestSafeInt(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		fallback int
		want     int
	}{
		{"valid", "42", -1, 42},
		{"negative", "-7", 0, -7},
		{"leading_plus", "+3", 0, 3},
		{"whitespace_wrapped", "  42 \r\n", -1, 42},
		{"trailing_cr", "7\r", -1, 7},
		{"empty", "", 99, 99},
		{"whitespace_only", "   ", 99, 99},
		{"malformed", "abc", 99, 99},
		{"float_string", "3.14", 99, 99},
		{"hex_not_accepted", "0x1A", 99, 99},
		{"overflow", "99999999999999999999999999", 99, 99},
		{"embedded_space", "4 2", 99, 99},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), 99, 99},
		{"at_cap_valid", strings.Repeat("0", filters.MaxInputBytes-1) + "1", 99, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.SafeInt(tc.in, tc.fallback)
			if got != tc.want {
				t.Errorf("SafeInt(%q, %d) = %d, want %d", tc.in, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestSafeFloat(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		fallback float64
		want     float64
	}{
		{"valid", "3.14", -1, 3.14},
		{"integer_form", "42", -1, 42},
		{"negative", "-2.5", 0, -2.5},
		{"whitespace_wrapped", "  3.14 \r\n", -1, 3.14},
		{"scientific", "1.5e3", -1, 1500},
		{"empty", "", 9.9, 9.9},
		{"whitespace_only", "   ", 9.9, 9.9},
		{"malformed", "abc", 9.9, 9.9},
		{"nan_rejected", "NaN", 9.9, 9.9},
		{"pos_inf_rejected", "+Inf", 9.9, 9.9},
		{"neg_inf_rejected", "-Inf", 9.9, 9.9},
		{"infinity_word_rejected", "Infinity", 9.9, 9.9},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), 9.9, 9.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.SafeFloat(tc.in, tc.fallback)
			if got != tc.want {
				t.Errorf("SafeFloat(%q, %v) = %v, want %v", tc.in, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestSafeBool(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		fallback bool
		want     bool
	}{
		{"one", "1", false, true},
		{"t_lower", "t", false, true},
		{"t_upper", "T", false, true},
		{"true_lower", "true", false, true},
		{"true_title", "True", false, true},
		{"true_upper", "TRUE", false, true},
		{"yes_lower", "yes", false, true},
		{"yes_upper", "YES", false, true},
		{"on_lower", "on", false, true},
		{"on_title", "On", false, true},
		{"zero", "0", true, false},
		{"f_lower", "f", true, false},
		{"f_upper", "F", true, false},
		{"false_lower", "false", true, false},
		{"false_title", "False", true, false},
		{"false_upper", "FALSE", true, false},
		{"no_lower", "no", true, false},
		{"no_upper", "NO", true, false},
		{"off_lower", "off", true, false},
		{"off_title", "Off", true, false},
		{"whitespace_wrapped", "  yes \r\n", false, true},
		{"empty_uses_fallback_true", "", true, true},
		{"empty_uses_fallback_false", "", false, false},
		{"unrecognized_uses_fallback", "enabled", true, true},
		{"unrecognized_uses_fallback_false", "maybe", false, false},
		{"numeric_two_unrecognized", "2", true, true},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.SafeBool(tc.in, tc.fallback)
			if got != tc.want {
				t.Errorf("SafeBool(%q, %v) = %v, want %v", tc.in, tc.fallback, got, tc.want)
			}
		})
	}
}
