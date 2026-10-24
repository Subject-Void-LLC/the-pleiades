// Tests of reading a saved pr-check log's summary, against the summaries
// of real runs (testdata/prcheck-summary-*.log, a machine's name and
// address replaced by documentation values).
package main

import (
	"strings"
	"testing"
	"time"
)

// brief is the part of an entry a summary test compares.
type brief struct{ kind, leg, pass, os, pkg, test, verdict, reason string }

// briefs is entries as briefs, packages without the module's prefix.
func briefs(entries []Entry) []brief {
	var out []brief
	for _, e := range entries {
		out = append(out, brief{e.Kind, e.Leg, e.Pass, e.OS, short(e.Package), e.Test, e.Verdict, e.Reason})
	}
	return out
}

// TestParsePRCheckLog_RealSummaries: each real run's summary gives one entry
// per failure, contention, and leg or step that did not pass with nothing
// else to blame, and none for what passed.
func TestParsePRCheckLog_RealSummaries(t *testing.T) {
	cases := map[string]struct {
		short   string
		verdict string
		want    []brief
	}{
		"prcheck-summary-9.log": {"5dfb5361be32", "fail", []brief{
			{KindTest, "win", "full", "Windows", "internal/journal", "TestActive_FailuresSayWhatFailed", VerdictReal, ""},
		}},
		"prcheck-summary-10.log": {"c2d7b8a66e01", "fail", []brief{
			{KindTest, "containers", "", "linux", "cmd/pleiades", "TestCLI_ImportGNS3ReachesANodesConsole", VerdictContention, ""},
			{KindTest, "containers", "", "linux", "cmd/pleiades", "TestCLI_NetworkChangeIsGatedAppliedAndUndone", VerdictContention, ""},
			{KindLeg, "mac", "", "macOS", "", "", VerdictNotRun, "stopped at reachable: <target> (<ip>:22) did not answer on its SSH port within 5s: dial tcp <ip>:22: i/o timeout"},
		}},
		"prcheck-summary-11.log": {"9fc2bd0ea76a", "pass", []brief{
			{KindTest, "win", "full", "Windows", "pkg/netcli", "TestTelnet_UserModeRunsCommandsAndRefusesConfiguration", VerdictContention, ""},
			{KindLeg, "receipt", "", "", "", "", VerdictFail, "the working tree already had uncommitted changes when the gate started, so the run examined code that is in no commit and no receipt was written."},
			{KindLeg, "mac", "", "macOS", "", "", VerdictNotRun, "no Mac configured (PLEIADES_MAC in tests/mac/.env)"},
		}},
		"prcheck-summary-13.log": {"9fc2bd0ea76a", "fail", []brief{
			{KindTest, "containers", "", "linux", "cmd/pleiades", "", VerdictReal, "the package's test binary failed or timed out"},
			{KindTest, "containers", "", "linux", "cmd/pleiades", "TestCLI_ImportGNS3ReachesANodesConsole", VerdictContention, ""},
			{KindLeg, "mac", "", "macOS", "", "", VerdictNotRun, "no Mac configured (PLEIADES_MAC in tests/mac/.env)"},
		}},
		"prcheck-summary-15.log": {"d1e221fa8162", "fail", []brief{
			{KindTest, "win", "repeat", "Windows", "pkg/telnetexec", "TestSession_ADeviceThatNeverGoesQuietIsCutOff", VerdictStrict, ""},
			{KindTest, "containers", "", "linux", "internal/catalog/wait", "TestRoutes_WaitsForARouteOSPFLearns", VerdictContention, ""},
		}},
	}
	for file, c := range cases {
		got, err := parsePRCheckLog(readFixture(t, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got.short != c.short || got.verdict != c.verdict || got.written.IsZero() {
			t.Errorf("%s: summary at %q %q %v, want %q %q", file, got.short, got.verdict, got.written, c.short, c.verdict)
		}
		if b := briefs(got.entries); !equalBriefs(b, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", file, b, c.want)
		}
	}
}

// TestParsePRCheckLog_KeepsTheFailingLineAndNoMachine: a failure's own
// output gives its error line, and the disk refusal keeps no machine name.
func TestParsePRCheckLog_KeepsTheFailingLineAndNoMachine(t *testing.T) {
	got, err := parsePRCheckLog(readFixture(t, "prcheck-summary-15.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got.entries[0].Error != "session_test.go:356: a dribble at login = <nil>" {
		t.Errorf("the Windows failure's error line is %q", got.entries[0].Error)
	}
	if !got.written.Equal(time.Date(2026, 10, 24, 5, 55, 0, 0, time.UTC)) {
		t.Errorf("written %v", got.written)
	}
	disk, err := parsePRCheckLog(readFixture(t, "prcheck-summary-12.log"))
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range disk.entries {
		reasons = append(reasons, e.Leg+": "+e.Reason)
	}
	joined := strings.Join(reasons, "\n")
	if !strings.Contains(joined, "bsd: stopped at ready: <target> has 3 GB free") || strings.Contains(joined, "bsd-check") {
		t.Errorf("the disk refusal reads:\n%s", joined)
	}
}

// TestParsePRCheckLog_ProblemsAndNoSummary: a problem line is a pr-check
// entry, a missing part is one, and a log with no summary is an error.
func TestParsePRCheckLog_ProblemsAndNoSummary(t *testing.T) {
	log := "pr-check at abc123, 2026-10-24 09:00 UTC: FAIL\n\n" +
		"  fast        error    3 package(s), 9s, 0 failed, 0 contention, 0 skipped: go test would not start\n" +
		"  win         missing  no record of it\n" +
		"  coverage    fail     exit 1\n" +
		"\nProblem (interrupted) pr-check stopped before it finished: what ran is recorded\n" +
		"\nProblem (unreadable) mac.json: not a record\n"
	got, err := parsePRCheckLog(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []brief{
		{KindLeg, "pr-check", "", "", "", "", VerdictNotRun, "interrupted: pr-check stopped before it finished: what ran is recorded"},
		{KindLeg, "pr-check", "", "", "", "", VerdictFail, "unreadable: not a record"},
		{KindLeg, "fast", "", "linux", "", "", VerdictFail, "3 package(s), 9s, 0 failed, 0 contention, 0 skipped: go test would not start"},
		{KindLeg, "win", "", "Windows", "", "", VerdictNotRun, "no record of it: pr-check said it would run it"},
		{KindLeg, "coverage", "", "", "", "", VerdictFail, "exit 1"},
	}
	if b := briefs(got.entries); !equalBriefs(b, want) {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
	// An interrupted run's missing parts are the interruption's, not their own.
	stopped, err := parsePRCheckLog(strings.Replace(log, ": FAIL", ": INTERRUPTED", 1))
	if err != nil || len(stopped.entries) != len(want)-1 || stopped.verdict != "interrupted" {
		t.Errorf("interrupted: %v, %+v", err, briefs(stopped.entries))
	}
	if _, err := parsePRCheckLog("pr-check: 3 container-free packages\nmake: *** Error 2\n"); err == nil {
		t.Error("a log with no summary was read")
	}
}

// equalBriefs reports whether a and b hold the same briefs in the same order.
func equalBriefs(a, b []brief) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParsePRCheckLog_ContentionTakesItsErrorFromTestgate: a contention the
// summary only names gets its error line from testgate's own account of the
// pass, earlier in the same log.
func TestParsePRCheckLog_ContentionTakesItsErrorFromTestgate(t *testing.T) {
	got, err := parsePRCheckLog(readFixture(t, "testgate-contention.log") + readFixture(t, "prcheck-summary-15.log"))
	if err != nil {
		t.Fatal(err)
	}
	race := got.entries[1]
	if race.Verdict != VerdictContention || !strings.HasPrefix(race.Error, "routes_live_test.go:27: creating network") || len(race.Error) > maxText {
		t.Errorf("the contention: %+v", race)
	}
}
