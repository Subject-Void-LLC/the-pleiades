// Tests of the scope record-prcheck keeps beside the ledgers, against a real
// pr-check report: what a run tested is recorded once per run, a run that
// says nothing reliable records nothing, and the report names a test that
// failed only in Actions, in a package pr-check did not test at a commit
// where it ran, as the FAILURE_PATTERNS 631 shape.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// otherCommit is a commit no pr-check run in the fixtures checked.
const otherCommit = "2fa9390d9e6736bec053983246a95a3b2b81ba39"

// TestRecordPRCheck_KeepsTheRunsScope: a recorded run's scope is the union
// of its passes' packages, written once however often the run is recorded.
func TestRecordPRCheck_KeepsTheRunsScope(t *testing.T) {
	report, dir := mergedReport(t), t.TempDir()
	if out := runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-branch", "b"); !strings.Contains(out, "scope (2 packages)") {
		t.Errorf("the first recording said %q", out)
	}
	scopes, err := readScopes(scopeFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{modulePrefix + "x/a", modulePrefix + "x/b"}
	if len(scopes) != 1 || !slices.Equal(scopes[0].Packages, want) || scopes[0].Commit != testCommit ||
		scopes[0].Run != "2026-10-24T12:00:00Z/"+testCommit || scopes[0].Schema != ScopeSchema || scopes[0].Kind != ScopeKind {
		t.Errorf("scopes %+v, want one run at %s testing %v", scopes, testCommit, want)
	}
	if out := runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-branch", "b"); strings.Contains(out, "scope") {
		t.Errorf("a second recording added a scope: %q", out)
	}
	if again, _ := readScopes(scopeFile(dir)); len(again) != 1 {
		t.Errorf("a run recorded twice holds %d scopes", len(again))
	}
}

// TestScopeOf_WhatSaysNothingReliable: an interrupted or running report, one
// with no commit and one whose passes tested nothing record no scope, since
// each would make a package the run meant to test look outside it.
func TestScopeOf_WhatSaysNothingReliable(t *testing.T) {
	r, err := prreport.ReadReport(mergedReport(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scopeOf(r, testAt); !ok {
		t.Fatal("a finished report recorded no scope")
	}
	for name, edit := range map[string]func(*prreport.Report){
		"interrupted": func(r *prreport.Report) { r.Verdict = prreport.VerdictInterrupted },
		"running":     func(r *prreport.Report) { r.Verdict = prreport.VerdictRunning },
		"no commit":   func(r *prreport.Report) { r.Commit = "" },
		"no package":  func(r *prreport.Report) { r.Passes = nil },
		"empty packages": func(r *prreport.Report) {
			for i := range r.Passes {
				r.Passes[i].Run.Packages = nil
			}
		},
	} {
		c := r
		c.Passes = slices.Clone(r.Passes)
		edit(&c)
		if s, ok := scopeOf(c, testAt); ok {
			t.Errorf("%s: recorded %+v", name, s)
		}
	}
}

// TestReport_NamesATestOutsidePRChecksScope: with a real run's scope
// recorded at testCommit, an Actions-only failure in a package that run did
// not test is outside its scope and named so in both views; one in a
// package it tested (named either way) is not; nor is one at a commit
// pr-check never ran at, which says nothing either way.
func TestReport_NamesATestOutsidePRChecksScope(t *testing.T) {
	report, dir := mergedReport(t), t.TempDir()
	runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-branch", "b")
	actions := func(pkg, test, commit string) Entry {
		return Entry{Schema: EntrySchema, Source: SourceActions, SeenAt: testAt, Commit: commit, Kind: KindTest, Leg: "fast", OS: "linux",
			Package: pkg, Test: test, Verdict: VerdictReal}
	}
	if err := writeLedger(ledgerFile(dir, SourceActions), []Entry{
		actions("x/consumer", "TestOldBehaviour", testCommit),
		actions("x/b", "TestInScope", testCommit),
		actions(modulePrefix+"x/a", "TestInScopeByFullPath", testCommit),
		actions("x/elsewhere", "TestNoRunThere", otherCommit),
	}); err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal([]byte(runTool(t, "report", "-dir", dir, "-json")), &r); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.OutsideScope, []string{"x/consumer.TestOldBehaviour"}) {
		t.Errorf("outside scope %v, want only the consumer's test", r.OutsideScope)
	}
	for _, ts := range r.Tests {
		want := 0
		if ts.Test == "TestOldBehaviour" {
			want = 1
		}
		if len(ts.OutsideScopeAt) != want {
			t.Errorf("%s: outside scope at %v", ts.Test, ts.OutsideScopeAt)
		}
	}
	text := runTool(t, "report", "-dir", dir)
	if !strings.Contains(text, "x/consumer.TestOldBehaviour") || !strings.Contains(text, "outside pr-check's scope at "+shortSHA(testCommit)+" (the FAILURE_PATTERNS 631 shape)") {
		t.Errorf("the text view does not name the test outside pr-check's scope:\n%s", text)
	}
	if strings.Count(text, "outside pr-check's scope") != 1 {
		t.Errorf("the text view names more than one test outside pr-check's scope:\n%s", text)
	}
}

// TestReadScopes_Refusals: a line that is not JSON, and one of another kind
// or schema, are refused with their line number; a file standing where the
// ledgers' directory belongs is not read as no scope.
func TestReadScopes_Refusals(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"not JSON":       "{\n",
		"another kind":   `{"schema":1,"kind":"test"}` + "\n",
		"a newer schema": "\n" + `{"schema":2,"kind":"pr-check-scope"}` + "\n",
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".jsonl")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readScopes(path); err == nil || !strings.Contains(err.Error(), path+":") {
			t.Errorf("%s: %v, want it refused with its line", name, err)
		}
		if _, err := appendScope(path, Scope{Run: "r"}); err == nil {
			t.Errorf("%s: a scope was appended to a file that cannot be read", name)
		}
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readScopes(scopeFile(file)); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a scope file under a file: %v", err)
	}
	broken := t.TempDir()
	if err := os.WriteFile(scopeFile(broken), []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if code := run([]string{"report", "-dir", broken}, &strings.Builder{}, &stderr, nil); code != 1 || !strings.Contains(stderr.String(), scopeName) {
		t.Errorf("a report over a scope file it cannot read: exit %d, %s", code, stderr.String())
	}
}
