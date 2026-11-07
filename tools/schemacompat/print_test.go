// Package main: tests of the printed verdict and the refusals before any
// comparison.
package main

import (
	"strings"
	"testing"
)

func TestPrintVerdictNamesEachStateAndWhatToDo(t *testing.T) {
	v := Verdict{Base: "v0.3.0", Line: "0.4.0",
		Findings: []Judged{
			{Finding: Finding{Rule: "property-removed", Path: "run-report.json#/properties/a"}, AcceptedBy: "a.breaking.md"},
			{Finding: Finding{Rule: "type-narrowed", Path: "runbook.schema.json#/type"}},
		},
		Stale:    []string{"enum-value-added run-report.json#/enum"},
		Problems: []string{"x.json is not classified"},
	}
	var b strings.Builder
	printVerdict(&b, v)
	for _, want := range []string{
		"accepted  property-removed run-report.json#/properties/a  (a.breaking.md)",
		"BREAKING  type-narrowed runbook.schema.json#/type",
		"STALE     enum-value-added run-report.json#/enum",
		"PROBLEM   x.json is not classified",
		"changelog/<slug>.breaking.md",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, b.String())
		}
	}
	v.Patch = true
	b.Reset()
	printVerdict(&b, v)
	if !strings.Contains(b.String(), "a patch line") || !strings.Contains(b.String(), "make it on main") || strings.Contains(b.String(), "breaking.md fragment") {
		t.Errorf("a patch line's output should send the change to main, not to a fragment:\n%s", b.String())
	}
	b.Reset()
	printVerdict(&b, Verdict{Base: "v0.3.0", Line: "0.4.0", Pass: true})
	if !strings.Contains(b.String(), "no unaccepted breaking change") {
		t.Errorf("a pass says %q", b.String())
	}
}

// fakeTree is a tree with fixed answers, for refusals that never reach git.
type fakeTree struct{ tagList []string }

// tags implements tree.
func (f fakeTree) tags() ([]string, error) { return f.tagList, nil }

// list implements tree.
func (fakeTree) list(string, string) ([]string, error) { return nil, nil }

// read implements tree.
func (fakeTree) read(string, string) ([]byte, error) { return nil, nil }

func TestRunRefusesWhatIsNotAReleaseBeforeComparing(t *testing.T) {
	for _, tc := range []struct{ base, line, want string }{
		{line: "next", want: "not a release version"},
		{line: "0.4.0-rc.1", want: "not a release version"},
		{base: "main", line: "0.4.0", want: "not a release tag"},
		{line: "0.4.0", want: "no release tag at or below 0.4.0"},
	} {
		_, err := run(fakeTree{tagList: []string{"v0.5.0"}}, tc.base, tc.line, t.TempDir(), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("base %q line %q: error %v, want %q", tc.base, tc.line, err, tc.want)
		}
	}
}

func TestCompareFileRefusesWhatIsNotAJSONObject(t *testing.T) {
	for _, tc := range []struct{ old, cur, want string }{
		{old: "{", cur: "{}", want: "at the base is not JSON"},
		{old: "{}", cur: "nope", want: "is not JSON"},
		{old: "[]", cur: "{}", want: "not a JSON object"},
	} {
		if _, err := compareFile("x.json", outputSchema, []byte(tc.old), []byte(tc.cur)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s -> %s: error %v, want %q", tc.old, tc.cur, err, tc.want)
		}
	}
}
