// The crossover report: every test the ledgers name, counted by source and
// verdict, then which failed only in Actions (a hole in the pre-push gate),
// only in pr-check (this machine's environment), or in both; which keep
// losing races; and what legs and steps failed with no test to blame.
package main

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// ReportSchema is the -json report's major version, under the same rules
// as EntrySchema.
const ReportSchema = 1

// ReportKind is the kind every report carries.
const ReportKind = "failures-report"

// recurringAt is how many contention verdicts make a test recurring.
const recurringAt = 2

// Filters narrows the entries a report reads.
type Filters struct {
	// Since leaves out what was seen before it; nil leaves nothing out.
	Since *time.Time `json:"since,omitempty"`
	// Source keeps only pr-check or only actions.
	Source string `json:"source,omitempty"`
	// Test keeps only tests whose package.Test matches it.
	Test string `json:"test,omitempty"`
}

// TestSummary is one test (or package-level failure) across both ledgers.
type TestSummary struct {
	// Package is the test's import path.
	Package string `json:"package"`
	// Test is the test, absent for a package-level failure.
	Test string `json:"test,omitempty"`
	// Counts is how many entries name it, by source and then verdict.
	Counts map[string]map[string]int `json:"counts"`
	// Legs are the pr-check legs and Actions jobs it failed in.
	Legs []string `json:"legs"`
	// OSes are the operating systems it failed on.
	OSes []string `json:"oses"`
	// FirstSeen and LastSeen bound when it failed.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Commits are the commits it failed at, in full.
	Commits []string `json:"commits"`
	// Patterns are the FAILURE_PATTERNS numbers annotated on its entries.
	Patterns []int `json:"patterns,omitempty"`
	// Notes are the notes annotated on its entries.
	Notes []string `json:"notes,omitempty"`
	// LastError is the newest error line recorded for it.
	LastError string `json:"last_error,omitempty"`
	// Contention is how many entries call it contention, in either source.
	Contention int `json:"contention"`
}

// LegSummary is one kind of non-test outcome: a source's leg, verdict and
// cause, with how often it happened.
type LegSummary struct {
	Source  string `json:"source"`
	Leg     string `json:"leg"`
	Verdict string `json:"verdict"`
	// Cause is the reason's class (unreachable, disk, no receipt, ...), or
	// the reason itself when no class fits.
	Cause string `json:"cause"`
	// Count is how many entries it covers.
	Count int `json:"count"`
	// Example is one reason it covers, as recorded.
	Example string `json:"example"`
	// Commits are the commits it happened at.
	Commits []string `json:"commits"`
}

// Report is the crossover report. Each crossover list names tests by
// package.Test, as Tests does.
type Report struct {
	Schema    int       `json:"schema"`
	Kind      string    `json:"kind"`
	Generated time.Time `json:"generated"`
	Filters   Filters   `json:"filters"`
	// Entries is how many entries the report read, by source.
	Entries map[string]int `json:"entries"`
	// Tests are every test the entries name.
	Tests []TestSummary `json:"tests"`
	// ActionsOnly failed in Actions and never in pr-check: holes in the
	// pre-push gate's scope, tier or environment.
	ActionsOnly []string `json:"actions_only"`
	// PRCheckOnly failed in pr-check and never in Actions: this machine's
	// environment.
	PRCheckOnly []string `json:"pr_check_only"`
	// Both failed in both.
	Both []string `json:"both"`
	// RecurringContention are tests called contention recurringAt times or
	// more, in either source.
	RecurringContention []string `json:"recurring_contention"`
	// Legs are the non-test outcomes, by source, leg, verdict and cause.
	Legs []LegSummary `json:"legs"`
}

// causes classify a leg's reason, first match wins.
var causes = []struct {
	re    *regexp.Regexp
	cause string
}{
	{regexp.MustCompile(`(?i)interrupted`), "interrupted"},
	{regexp.MustCompile(`(?i)no \S+ configured`), "not configured"},
	{regexp.MustCompile(`(?i)did not answer|unreachable|i/o timeout|no route|connection refused`), "unreachable"},
	{regexp.MustCompile(`(?i)\bfree\b|disk|no space`), "disk"},
	{regexp.MustCompile(`(?i)uncommitted|no receipt`), "no receipt"},
	{regexp.MustCompile(`(?i)checked only from jobs that passed`), "coverage after a failed job"},
	{regexp.MustCompile(`(?i)no record of it`), "missing record"},
	{regexp.MustCompile(`(?i)docker`), "docker"},
}

// causeOf is reason's class.
func causeOf(reason string) string {
	for _, c := range causes {
		if c.re.MatchString(reason) {
			return c.cause
		}
	}
	return reason
}

// buildReport reads entries through f into the crossover report.
func buildReport(entries []Entry, f Filters, now time.Time) (Report, error) {
	var testRE *regexp.Regexp
	if f.Test != "" {
		re, err := regexp.Compile(f.Test)
		if err != nil {
			return Report{}, err
		}
		testRE = re
	}
	r := Report{Schema: ReportSchema, Kind: ReportKind, Generated: now.UTC(), Filters: f, Entries: map[string]int{},
		Tests: []TestSummary{}, ActionsOnly: []string{}, PRCheckOnly: []string{}, Both: []string{}, RecurringContention: []string{}, Legs: []LegSummary{}}
	tests := map[string]*TestSummary{}
	legs := map[string]*LegSummary{}
	for _, e := range entries {
		if (f.Since != nil && e.SeenAt.Before(*f.Since)) || (f.Source != "" && e.Source != f.Source) {
			continue
		}
		if testRE != nil && (e.Kind != KindTest || !testRE.MatchString(e.name())) {
			continue
		}
		r.Entries[e.Source]++
		if e.Kind == KindTest {
			addTest(tests, e)
			continue
		}
		cause := causeOf(e.Reason)
		k := strings.Join([]string{e.Source, e.Leg, e.Verdict, cause}, "\x00")
		l := legs[k]
		if l == nil {
			l = &LegSummary{Source: e.Source, Leg: e.Leg, Verdict: e.Verdict, Cause: cause, Example: e.Reason}
			legs[k] = l
		}
		l.Count++
		l.Commits = addString(l.Commits, e.Commit)
	}
	for name, t := range tests {
		r.Tests = append(r.Tests, *t)
		_, local := t.Counts[SourcePRCheck]
		_, remote := t.Counts[SourceActions]
		switch {
		case local && remote:
			r.Both = append(r.Both, name)
		case remote:
			r.ActionsOnly = append(r.ActionsOnly, name)
		default:
			r.PRCheckOnly = append(r.PRCheckOnly, name)
		}
		if t.Contention >= recurringAt {
			r.RecurringContention = append(r.RecurringContention, name)
		}
	}
	sort.Slice(r.Tests, func(i, j int) bool { return testName(r.Tests[i]) < testName(r.Tests[j]) })
	for _, list := range [][]string{r.ActionsOnly, r.PRCheckOnly, r.Both, r.RecurringContention} {
		sort.Strings(list)
	}
	for _, l := range legs {
		r.Legs = append(r.Legs, *l)
	}
	sort.Slice(r.Legs, func(i, j int) bool {
		a, b := r.Legs[i], r.Legs[j]
		if a.Source != b.Source {
			return a.Source > b.Source // pr-check first
		}
		if a.Leg != b.Leg {
			return a.Leg < b.Leg
		}
		return a.Cause < b.Cause
	})
	return r, nil
}

// addTest counts e into the summary of its test.
func addTest(tests map[string]*TestSummary, e Entry) {
	t := tests[e.name()]
	if t == nil {
		t = &TestSummary{Package: e.Package, Test: e.Test, Counts: map[string]map[string]int{}, FirstSeen: e.SeenAt, LastSeen: e.SeenAt}
		tests[e.name()] = t
	}
	if t.Counts[e.Source] == nil {
		t.Counts[e.Source] = map[string]int{}
	}
	t.Counts[e.Source][e.Verdict]++
	if e.Verdict == VerdictContention {
		t.Contention++
	}
	t.Legs = addString(t.Legs, e.Leg)
	t.OSes = addString(t.OSes, e.OS)
	t.Commits = addString(t.Commits, e.Commit)
	t.Notes = addString(t.Notes, e.Note)
	if e.Pattern != 0 && !slices.Contains(t.Patterns, e.Pattern) {
		t.Patterns = append(t.Patterns, e.Pattern)
		sort.Ints(t.Patterns)
	}
	if e.SeenAt.Before(t.FirstSeen) {
		t.FirstSeen = e.SeenAt
	}
	if !e.SeenAt.Before(t.LastSeen) {
		t.LastSeen = e.SeenAt
		if e.Error != "" {
			t.LastError = e.Error
		}
	}
	if t.LastError == "" {
		t.LastError = e.Error
	}
}

// testName is a summary's package.Test.
func testName(t TestSummary) string {
	return Entry{Package: t.Package, Test: t.Test}.name()
}

// addString adds s to list once, keeping it sorted; an empty s is not added.
func addString(list []string, s string) []string {
	if s == "" {
		return list
	}
	i := sort.SearchStrings(list, s)
	if i < len(list) && list[i] == s {
		return list
	}
	return append(list[:i], append([]string{s}, list[i:]...)...)
}
