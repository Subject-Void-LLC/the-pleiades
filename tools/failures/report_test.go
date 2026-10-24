// Tests of the crossover report on a small synthetic ledger: which tests
// land in Actions only, pr-check only and both, which are recurring
// contention, how legs are grouped, and that the filters and both views
// read the same model.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// synthetic is a ledger of both sources: TestHole failed only in Actions,
// TestLocal only here, TestShared in both, TestRace was contention three
// times, and two legs failed for reasons of one class.
func synthetic() []Entry {
	at := func(h int) time.Time { return testAt.Add(time.Duration(h) * time.Hour) }
	test := func(source, leg, pkg, name, verdict string, h int) Entry {
		return Entry{Schema: EntrySchema, Source: source, SeenAt: at(h), Commit: testCommit, Kind: KindTest, Leg: leg, OS: "linux", Package: pkg, Test: name, Verdict: verdict, Error: "e" + name}
	}
	leg := func(source, name, verdict, reason string) Entry {
		return Entry{Schema: EntrySchema, Source: source, SeenAt: at(0), Commit: testCommit, Kind: KindLeg, Leg: name, Verdict: verdict, Reason: reason}
	}
	return []Entry{
		test(SourceActions, "containers (6)", "x/a", "TestHole", VerdictReal, 1),
		test(SourcePRCheck, "win", "x/a", "TestLocal", VerdictStrict, 2),
		test(SourcePRCheck, "fast", "x/b", "TestShared", VerdictReal, 3),
		test(SourceActions, "fast", "x/b", "TestShared", VerdictStrict, 4),
		test(SourcePRCheck, "containers", "x/c", "TestRace", VerdictContention, 5),
		test(SourcePRCheck, "containers", "x/c", "TestRace", VerdictContention, 6),
		test(SourceActions, "containers (2)", "x/c", "TestRace", VerdictContention, 7),
		leg(SourcePRCheck, "mac", VerdictNotRun, "stopped at reachable: <target> did not answer"),
		leg(SourcePRCheck, "mac", VerdictNotRun, "stopped at reachable: dial tcp <ip>:22: i/o timeout"),
		leg(SourceActions, "coverage", VerdictFail, "fast: failure; the floors are checked only from jobs that passed"),
	}
}

// TestBuildReport_Crossover: each test lands in exactly the section its
// sources put it in, and recurring contention counts both sources.
func TestBuildReport_Crossover(t *testing.T) {
	r, err := buildReport(synthetic(), nil, Filters{}, testAt)
	if err != nil {
		t.Fatal(err)
	}
	check := func(what string, got []string, want ...string) {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	check("actions only", r.ActionsOnly, "x/a.TestHole")
	check("pr-check only", r.PRCheckOnly, "x/a.TestLocal")
	check("both", r.Both, "x/b.TestShared", "x/c.TestRace")
	check("recurring contention", r.RecurringContention, "x/c.TestRace")
	if r.Entries[SourcePRCheck] != 6 || r.Entries[SourceActions] != 4 || len(r.Tests) != 4 {
		t.Errorf("entries %v, tests %d", r.Entries, len(r.Tests))
	}
	race := r.Tests[len(r.Tests)-1]
	if race.Contention != 3 || race.Counts[SourcePRCheck][VerdictContention] != 2 || !race.FirstSeen.Equal(testAt.Add(5*time.Hour)) ||
		!race.LastSeen.Equal(testAt.Add(7*time.Hour)) || race.LastError != "eTestRace" || strings.Join(race.Legs, ",") != "containers,containers (2)" {
		t.Errorf("TestRace: %+v", race)
	}
	if len(r.Legs) != 2 || r.Legs[0].Leg != "mac" || r.Legs[0].Count != 2 || r.Legs[0].Cause != "unreachable" || r.Legs[1].Cause != "coverage after a failed job" {
		t.Errorf("legs: %+v", r.Legs)
	}
}

// TestBuildReport_Filters: since, source and test each narrow what is read,
// and a bad expression is refused.
func TestBuildReport_Filters(t *testing.T) {
	since := testAt.Add(4 * time.Hour)
	r, _ := buildReport(synthetic(), nil, Filters{Since: &since}, testAt)
	if len(r.Tests) != 2 || len(r.Legs) != 0 {
		t.Errorf("since: %d tests, %d legs", len(r.Tests), len(r.Legs))
	}
	r, _ = buildReport(synthetic(), nil, Filters{Source: SourceActions}, testAt)
	if len(r.Both) != 0 || len(r.ActionsOnly) != 3 {
		t.Errorf("actions alone: both %v, actions only %v", r.Both, r.ActionsOnly)
	}
	r, _ = buildReport(synthetic(), nil, Filters{Test: `TestRace$`}, testAt)
	if len(r.Tests) != 1 || len(r.Legs) != 0 {
		t.Errorf("one test: %+v", r.Tests)
	}
	if _, err := buildReport(nil, nil, Filters{Test: "("}, testAt); err == nil {
		t.Error("a bad expression was read")
	}
}

// TestCauseOf: each class of reason the records hold has its cause, and
// anything else is its own.
func TestCauseOf(t *testing.T) {
	for reason, want := range map[string]string{
		"interrupted: pr-check stopped":                                   "interrupted",
		"no Mac configured (PLEIADES_MAC in tests/mac/.env)":              "not configured",
		"stopped at ready: <target> has 3 GB free where its tests unpack": "disk",
		"the working tree already had uncommitted changes":                "no receipt",
		"no record of it: pr-check said it would run it":                  "missing record",
		"no Docker daemon answered":                                       "docker",
		"exit 1":                                                          "exit 1",
	} {
		if got := causeOf(reason); got != want {
			t.Errorf("causeOf(%q) = %q, want %q", reason, got, want)
		}
	}
}

// TestReport_TextAndJSON: report prints every section from the ledgers on
// disk, and -json prints the same model, versioned.
func TestReport_TextAndJSON(t *testing.T) {
	dir := t.TempDir()
	var local, remote []Entry
	for _, e := range synthetic() {
		if e.Source == SourcePRCheck {
			local = append(local, e)
		} else {
			remote = append(remote, e)
		}
	}
	if err := writeLedger(ledgerFile(dir, SourcePRCheck), local); err != nil {
		t.Fatal(err)
	}
	if err := writeLedger(ledgerFile(dir, SourceActions), remote); err != nil {
		t.Fatal(err)
	}
	text := runTool(t, "report", "-dir", dir)
	for _, want := range []string{"Actions only", "pr-check only", "Both", "Recurring contention", "Legs and steps", "x/a.TestHole", "x/c.TestRace: 3 times", "unreachable"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text view lacks %q:\n%s", want, text)
		}
	}
	var r Report
	if err := json.Unmarshal([]byte(runTool(t, "report", "-dir", dir, "-json", "-since", "2026-10-24", "-source", "actions")), &r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != ReportSchema || r.Kind != ReportKind || r.Filters.Source != SourceActions || len(r.ActionsOnly) != 3 {
		t.Errorf("the JSON view: %+v", r)
	}
	if text := runTool(t, "report", "-dir", t.TempDir(), "-since", "2026-10-24T00:00:00Z"); !strings.Contains(text, "none") || !strings.Contains(text, "filtered") {
		t.Errorf("an empty report:\n%s", text)
	}
	for _, args := range [][]string{{"report", "-source", "jenkins"}, {"report", "-since", "Tuesday"}, {"report", "-test", "(", "-dir", dir}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
