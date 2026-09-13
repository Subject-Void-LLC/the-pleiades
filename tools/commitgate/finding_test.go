// Package main: tests for how findings are rendered and scored.
package main

import (
	"strings"
	"testing"
)

func TestSeverityString(t *testing.T) {
	if got := SeverityFail.String(); got != "FAIL" {
		t.Errorf("SeverityFail.String() = %q, want %q", got, "FAIL")
	}
	if got := SeverityWarn.String(); got != "warn" {
		t.Errorf("SeverityWarn.String() = %q, want %q", got, "warn")
	}
}

func TestFindingLocation(t *testing.T) {
	cases := map[string]struct {
		finding Finding
		want    string
	}{
		"file and line": {Finding{File: "internal/x/x.go", Line: 12}, "internal/x/x.go:12: "},
		"file only":     {Finding{File: "internal/x/x.go"}, "internal/x/x.go: "},
		"neither":       {Finding{}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.finding.location(); got != tc.want {
				t.Errorf("location() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReportBlocksOnlyOnAFailure(t *testing.T) {
	cases := map[string]struct {
		findings []Finding
		want     bool
	}{
		"nothing":       {nil, false},
		"warnings only": {[]Finding{{Severity: SeverityWarn, Rule: "soft cap"}}, false},
		"one failure":   {[]Finding{{Severity: SeverityFail, Rule: "em dash"}}, true},
		"mixed": {[]Finding{
			{Severity: SeverityWarn, Rule: "soft cap"},
			{Severity: SeverityFail, Rule: "em dash"},
		}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			if got := report(&out, tc.findings); got != tc.want {
				t.Errorf("report() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReportPrintsFailuresFirst(t *testing.T) {
	// A commit that is going to be refused should say so in the first
	// line a person reads, not after a screen of warnings.
	var out strings.Builder
	report(&out, []Finding{
		{Severity: SeverityWarn, Rule: "the warning", File: "a.go"},
		{Severity: SeverityFail, Rule: "the failure", File: "b.go"},
	})
	text := out.String()
	failAt := strings.Index(text, "the failure")
	warnAt := strings.Index(text, "the warning")
	if failAt < 0 || warnAt < 0 {
		t.Fatalf("report dropped a finding: %q", text)
	}
	if failAt > warnAt {
		t.Errorf("report printed the warning before the failure:\n%s", text)
	}
}

func TestReportKeepsTheOrderWithinASeverity(t *testing.T) {
	// Two runs over the same staged content must print the same thing,
	// or a reader cannot tell a new finding from a reshuffled one.
	var out strings.Builder
	report(&out, []Finding{
		{Severity: SeverityFail, Rule: "first", File: "a.go"},
		{Severity: SeverityFail, Rule: "second", File: "b.go"},
	})
	text := out.String()
	if strings.Index(text, "first") > strings.Index(text, "second") {
		t.Errorf("report reordered two findings of equal severity:\n%s", text)
	}
}

func TestReportWritesNothingWhenThereIsNothingToSay(t *testing.T) {
	var out strings.Builder
	report(&out, nil)
	if out.String() != "" {
		t.Errorf("report wrote %q for an empty finding list", out.String())
	}
}
