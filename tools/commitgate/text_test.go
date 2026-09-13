// Package main: tests for the rules that apply to any staged text.
//
// Every dash fixture below is built from its code point rather than
// pasted in. The rule under test forbids the literal character, so a
// test file carrying one would be refused by the gate it is testing.
package main

import (
	"strings"
	"testing"
)

// emDashText is one added line carrying the forbidden character.
var emDashText = "a sentence " + string(emDash) + " and its aside"

func TestCheckDashesRefusesAnEmDash(t *testing.T) {
	lines := []addedLine{{Number: 7, Text: emDashText}}
	findings := checkDashes("docs/10-running-in-production.md", lines)
	if !hasRule(findings, SeverityFail, "em dash") {
		t.Fatalf("checkDashes accepted %q", emDashText)
	}
	if findings[0].Line != 7 {
		t.Errorf("reported line %d, want 7: a finding has to open the right line", findings[0].Line)
	}
}

func TestCheckDashesReportsEachOffendingLineOnce(t *testing.T) {
	// Two em dashes on one line is one thing to fix, not two.
	lines := []addedLine{{Number: 1, Text: string(emDash) + "a" + string(emDash)}}
	findings := checkDashes("README.md", lines)
	fails := 0
	for _, f := range findings {
		if f.Severity == SeverityFail {
			fails++
		}
	}
	if fails != 1 {
		t.Errorf("reported %d failures for one line, want 1", fails)
	}
}

func TestCheckDashesWarnsOnALookalike(t *testing.T) {
	for name, r := range map[string]rune{"en dash": 0x2013, "horizontal bar": 0x2015} {
		t.Run(name, func(t *testing.T) {
			lines := []addedLine{{Number: 1, Text: "2020" + string(r) + "2024"}}
			findings := checkDashes("README.md", lines)
			if hasRule(findings, SeverityFail, "em dash") {
				t.Errorf("checkDashes refused a %s, which AGENTS.md does not forbid", name)
			}
			if !hasRule(findings, SeverityWarn, "reads as an em dash") {
				t.Errorf("checkDashes did not warn about a %s", name)
			}
		})
	}
}

func TestCheckDashesSkipsTestdata(t *testing.T) {
	lines := []addedLine{{Number: 1, Text: emDashText}}
	for _, path := range []string{
		"testdata/sample.md",
		"internal/redact/testdata/input.txt",
		"tools/gendocs/testdata/golden/page.md",
	} {
		t.Run(path, func(t *testing.T) {
			if findings := checkDashes(path, lines); len(findings) != 0 {
				t.Errorf("checkDashes judged a fixture under testdata: %d findings", len(findings))
			}
		})
	}
}

func TestCheckDashesIgnoresUntouchedText(t *testing.T) {
	// The rule reads added lines only. An empty added-line list is what a
	// commit that merely deletes from a dash-heavy archive looks like.
	if findings := checkDashes("HANDOFF_ARCHIVE.md", nil); len(findings) != 0 {
		t.Errorf("checkDashes reported %d findings for a commit that added nothing", len(findings))
	}
}

func TestIsBinaryDetectsANulByte(t *testing.T) {
	cases := map[string]struct {
		content []byte
		want    bool
	}{
		"text":            {[]byte("package main\n"), false},
		"empty":           {[]byte{}, false},
		"nul early":       {[]byte{0x7f, 'E', 'L', 'F', 0x00}, true},
		"nul past 8000":   {append(make([]byte, 0, 9000), append([]byte(strings.Repeat("a", 8500)), 0)...), false},
		"nul inside 8000": {append([]byte(strings.Repeat("a", 100)), 0), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isBinary(tc.content); got != tc.want {
				t.Errorf("isBinary() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExcerptKeepsWholeCharacters(t *testing.T) {
	// A multi-byte rune cut in half prints as a replacement character and
	// makes a reader doubt the tool reporting it.
	long := strings.Repeat("é", 200)
	got := excerpt(long)
	if strings.ContainsRune(got, 0xFFFD) {
		t.Error("excerpt split a multi-byte character")
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("excerpt did not mark the trim: %q", got)
	}
}

func TestExcerptLeavesAShortLineAlone(t *testing.T) {
	if got := excerpt("  short  "); got != "short" {
		t.Errorf("excerpt(%q) = %q, want %q", "  short  ", got, "short")
	}
}
