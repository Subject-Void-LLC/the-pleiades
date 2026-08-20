package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestCamelToSnake(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"simple", "camelCase", "camel_case"},
		{"pascal", "PascalCase", "pascal_case"},
		{"acronym_middle", "classifyIP", "classify_ip"},
		{"acronym_leading", "MACOUI", "macoui"},
		{"single_word", "word", "word"},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.CamelToSnake(tc.in); got != tc.want {
				t.Errorf("CamelToSnake(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSnakeToCamel(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"simple", "snake_case", "snakeCase"},
		{"single_word", "word", "word"},
		{"three_words", "one_two_three", "oneTwoThree"},
		{"empty", "", ""},
		{"leading_underscore", "_foo_bar", "FooBar"},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.SnakeToCamel(tc.in); got != tc.want {
				t.Errorf("SnakeToCamel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		keepLast int
		want     string
	}{
		{"keep_two", "hunter2", 2, "*****r2"},
		{"keep_zero", "hunter2", 0, "*******"},
		{"keep_negative", "hunter2", -1, "*******"},
		{"keep_more_than_length", "hunter2", 100, "hunter2"},
		{"keep_exact_length", "hunter2", 7, "hunter2"},
		{"empty", "", 2, ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MaskSecret(tc.in, tc.keepLast); got != tc.want {
				t.Errorf("MaskSecret(%q, %d) = %q, want %q", tc.in, tc.keepLast, got, tc.want)
			}
		})
	}
}

func TestRegexExtract(t *testing.T) {
	cases := []struct {
		name, s, pattern, groupName, want string
	}{
		{"match", "host1.example.com", `^(?P<host>[^.]+)\.`, "host", "host1"},
		{"no_match", "nope", `^(?P<host>[^.]+)\.`, "host", ""},
		{"unknown_group", "host1.example.com", `^(?P<host>[^.]+)\.`, "missing", ""},
		{"malformed_pattern", "x", `(`, "host", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.RegexExtract(tc.s, tc.pattern, tc.groupName); got != tc.want {
				t.Errorf("RegexExtract(%q, %q, %q) = %q, want %q", tc.s, tc.pattern, tc.groupName, got, tc.want)
			}
		})
	}
	t.Run("over_cap_s", func(t *testing.T) {
		if got := filters.RegexExtract(strings.Repeat("a", filters.MaxInputBytes+1), `(?P<x>a)`, "x"); got != "" {
			t.Errorf("RegexExtract(over cap s) = %q, want \"\"", got)
		}
	})
	t.Run("over_cap_pattern", func(t *testing.T) {
		if got := filters.RegexExtract("a", strings.Repeat("a", filters.MaxInputBytes+1), "x"); got != "" {
			t.Errorf("RegexExtract(over cap pattern) = %q, want \"\"", got)
		}
	})
}

func TestIsEmptyOrWhitespace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"spaces", "   ", true},
		{"tabs_and_newlines", "\t\n\r ", true},
		{"non_empty", "x", false},
		{"whitespace_around_content", "  x  ", false},
		{"over_cap", strings.Repeat(" ", filters.MaxInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsEmptyOrWhitespace(tc.in); got != tc.want {
				t.Errorf("IsEmptyOrWhitespace(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
