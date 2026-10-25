// Command gendocs's test that a real pr-check report fits the schema
// generated for it, strictly: every property the schema declares appears
// with a value, none it does not declare appears, and every value with a
// published known set is in it.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// fullPRReport merges a records directory written through each producer's
// own writer, holding a value for every field a report can carry: a pass
// with each kind of failure, a skip and its measurements; a leg that passed
// with its machine's runs and one that stopped; a step that failed and one
// that could not run; an expected part with no record; and a gate status.
func fullPRReport(t *testing.T) prreport.Report {
	t.Helper()
	dir := t.TempDir()
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	commit := strings.Repeat("abc123", 6) + "abcd"
	stamp := flakegate.Stamp{Commit: commit, Clean: true}
	if _, err := prreport.Begin(dir, commit, []string{"fast", "mac", "win", "coverage", "images", "durations"}, at); err != nil {
		t.Fatal(err)
	}
	fast := flakegate.Run{Label: "testgate -race -cover", OS: "linux", Arch: "amd64", Packages: []string{"x/a"}, Commit: commit, Clean: true, Started: at, Seconds: 12.5,
		Verdict: flakegate.VerdictFail, Error: "1 failure(s)",
		Failures: []flakegate.RunFailure{
			{Package: "x/a", Test: "TestBroken", Kind: "test", Outcome: flakegate.OutcomeFailed, Output: "want 1, got 2"},
			{Package: "x/a", Test: "TestRace", Kind: "test", Outcome: flakegate.OutcomeContention, Output: "lost a race"},
		},
		Skips:          []flakegate.Skip{{Package: "x/a", Test: "TestCloud", Reason: "needs localstack: no token"}},
		Coverage:       map[string]float64{"x/a": 91.5},
		Missing:        map[string][]string{"x/a": {"localstack"}},
		PackageSeconds: map[string]float64{"x/a": 3.25},
	}
	if err := flakegate.WriteRun(filepath.Join(dir, "fast.json"), fast); err != nil {
		t.Fatal(err)
	}
	// A leg's runs are the records its machine wrote, read back.
	written, err := os.ReadFile(filepath.Join(dir, "fast.json"))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := flakegate.ReadRun(written)
	if err != nil {
		t.Fatal(err)
	}
	legs := map[string]oscheck.LegRecord{
		"mac": {Tool: "maccheck", OS: "macOS", Target: "mac", Commit: commit, Packages: []string{"x/a"}, Started: at, Seconds: 80,
			Verdict: flakegate.VerdictPass, Error: "the box could not be put back", Runs: []flakegate.Run{machine}},
		"win": {Tool: "wincheck", OS: "Windows", Target: "win-check", Commit: commit, Packages: []string{"x/a"}, Started: at, Seconds: 9,
			Verdict: flakegate.VerdictNotRun, Step: oscheck.StepShip, Error: "401 - invalid content type"},
	}
	for name, leg := range legs {
		if err := oscheck.WriteLegRecord(filepath.Join(dir, name+".json"), leg); err != nil {
			t.Fatal(err)
		}
	}
	if err := prreport.WriteStep(dir, "coverage", 1, "", stamp, at); err != nil {
		t.Fatal(err)
	}
	if err := prreport.WriteStep(dir, "images", 0, "no Docker daemon answered", stamp, at); err != nil {
		t.Fatal(err)
	}
	one := 1
	r, err := prreport.Merge(dir, prreport.MergeOptions{Commit: commit, ExitCode: &one, Now: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestPRReportSchema_MatchesModel validates a real report against the
// generated schema, read as a consumer reads it, from its JSON.
func TestPRReportSchema_MatchesModel(t *testing.T) {
	built, err := prReportSchema(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(fullPRReport(t))
	if err != nil {
		t.Fatal(err)
	}
	var report any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	v := &schemaValidator{defs: schema["$defs"].(map[string]any), seen: map[string]bool{}, seenNull: map[string]bool{}, nullable: map[string]bool{}}
	for _, problem := range v.check("report", report, schema) {
		t.Error(problem)
	}
	for _, declared := range v.declared {
		if !v.seen[declared] {
			t.Errorf("the fixture never gives %s a value, so the schema's claim about it is untested", declared)
		}
	}
}

// TestPRReportSchema_IsTheContract: the published schema states the
// compatibility rules, leaves its objects open to fields a newer producer
// adds, publishes known values rather than closing them in an enum, and
// matches the file in docs/reference, so a reader of either reads one
// contract.
func TestPRReportSchema_IsTheContract(t *testing.T) {
	built, err := prReportSchema(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"schema is the major version", "a reader ignores a field it does not know", "as not a pass", "refuses a major version it does not know"} {
		if !strings.Contains(built["description"].(string), rule) {
			t.Errorf("the schema's description lacks %q", rule)
		}
	}
	encoded, _ := json.Marshal(built)
	for _, closed := range []string{`"additionalProperties":false`, `"enum"`} {
		if strings.Contains(string(encoded), closed) {
			t.Errorf("the schema closes a set the contract leaves open: %s", closed)
		}
	}
	published, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "schemas", "pr-check-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(published, &a); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(encoded, &b)
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)
	if string(pa) != string(pb) {
		t.Error("docs/reference/schemas/pr-check-report.json is not what gendocs builds now: run go run ./tools/gendocs")
	}
	if _, err := prReportSchema(t.TempDir()); err == nil {
		t.Error("a schema was built with none of its model files")
	}
}
