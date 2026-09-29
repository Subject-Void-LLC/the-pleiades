// In-process tests of `pleiades journal` and of `pleiades rollback`'s
// planning, refusals and runs of nothing. What a rollback does to a device
// is proved against a real one by rollback_release_gate_test.go; these
// cover the paths that decide before any device is contacted.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

const (
	cliRunID    = "3f2a9c1e-7b4d-4e8a-9c6f-2d1b8e5a7c30"
	cliUndoneBy = "4a3b2c1d-7b4d-4e8a-9c6f-2d1b8e5a7c31"
	cliDeviceID = "11111111-2222-4333-8444-555555555555"
)

// cliRunbook is the runbook the recorded run ran.
const cliRunbook = `id: rb-cli
hosts: web1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/cli-made
  - name: edit a file
    file.line.set:
      path: /tmp/cli.conf
      line: "a = b"
    rollback:
      - name: put it back
        file.line.set:
          path: /tmp/cli.conf
          line: "a = c"
`

// rollbackProject is a project holding web1, the runbook, and a sealed
// journal of one run of it: a directory made, with its undo recorded, and
// a file edited, whose undo is withheld. authored gives the edit a
// rollback: list, in the runbook and in its entry.
func rollbackProject(t *testing.T, authored bool, extra ...engine.JournalEntry) string {
	t.Helper()
	dir := t.TempDir()
	inv := "hosts:\n  - id: " + cliDeviceID + "\n    name: web1\n    type: linux_server\n    properties:\n      host: 10.0.0.1\n"
	if err := os.WriteFile(filepath.Join(dir, "inventory.yaml"), []byte(inv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "runbooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The rollback: list is in the runbook only when the edit had one: a
	// list written into the runbook later would answer for it too.
	source := cliRunbook
	if !authored {
		source = cliRunbook[:strings.Index(cliRunbook, "    rollback:")]
	}
	path := filepath.Join(dir, "runbooks", "rb-cli.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	entry := func(seq int, node, fqcn string) engine.JournalEntry {
		return engine.JournalEntry{RunID: cliRunID, Sequence: seq, NodeID: node, DeviceID: cliDeviceID, FQCN: fqcn,
			DAGID: "rb-cli", DAGVersion: dag.Version, Outcome: engine.OutcomeChanged, ActionChanged: true,
			TaskName: "task " + node, StartedAt: at, FinishedAt: at.Add(time.Second)}
	}
	made := entry(1, "tasks[0]", "file.directory")
	text := "/tmp/cli-made"
	made.InverseFQCN, made.InverseParamKeys, made.InverseComplete = "file.remove", []string{"path"}, true
	made.InverseParams = []engine.InverseParam{{Key: "path", Text: &text}}
	edited := entry(2, "tasks[1]", "file.line.set")
	edited.InverseFQCN, edited.InverseParamKeys = "file.copy", []string{"content", "dest"}
	edited.AuthoredRollback = authored
	store, err := journal.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), append([]engine.JournalEntry{made, edited}, extra...)); err != nil {
		t.Fatal(err)
	}
	if err := store.Seal(cliRunID, at); err != nil {
		t.Fatal(err)
	}
	return dir
}

// exitCodeOf is the exit status err asks main for.
func exitCodeOf(err error) int {
	var coded exitCoder
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestJournalCommand_ListsAndShowsRuns(t *testing.T) {
	dir := rollbackProject(t, false)
	// A rollback of it, left unsealed.
	store, err := journal.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	undo := engine.JournalEntry{RunID: cliUndoneBy, Sequence: 1, NodeID: "tasks[0]", DeviceID: cliDeviceID, FQCN: "file.remove",
		Outcome: engine.OutcomeChanged, RollbackOf: cliRunID, UndoesNode: "tasks[0]", StartedAt: time.Now().UTC().Add(time.Minute)}
	if err := store.Record(context.Background(), []engine.JournalEntry{undo}); err != nil {
		t.Fatal(err)
	}

	list := captureStdout(t, func() {
		if err := runJournal([]string{"list", "--dir", dir}); err != nil {
			t.Errorf("journal list: %v", err)
		}
	})
	for _, want := range []string{cliRunID + "  ", "rb-cli  2 task(s), 2 changed, 0 failed; rolled back by " + cliUndoneBy, "unsealed; rollback of " + cliRunID} {
		if !strings.Contains(list, want) {
			t.Errorf("journal list does not say %q:\n%s", want, list)
		}
	}
	var runs []runSummary
	if err := json.Unmarshal([]byte(captureStdout(t, func() { _ = runJournal([]string{"list", "--dir", dir, "--json"}) })), &runs); err != nil || len(runs) != 2 || runs[1].RollbackOf != cliRunID {
		t.Errorf("journal list --json = %+v, %v", runs, err)
	}

	show := captureStdout(t, func() {
		if err := runJournal([]string{"show", cliRunID, "--dir", dir}); err != nil {
			t.Errorf("journal show: %v", err)
		}
	})
	for _, want := range []string{"run " + cliRunID, "web1", "undo: file.remove path=/tmp/cli-made", "undo: file.copy (not recorded in full"} {
		if !strings.Contains(show, want) {
			t.Errorf("journal show does not say %q:\n%s", want, show)
		}
	}
	if out := captureStdout(t, func() { _ = runJournal([]string{"show", cliUndoneBy, "--dir", dir}) }); !strings.Contains(out, "unsealed") {
		t.Errorf("an unsealed run is not marked:\n%s", out)
	}
	var shown runShow
	if err := json.Unmarshal([]byte(captureStdout(t, func() { _ = runJournal([]string{"show", cliRunID, "--dir", dir, "--json"}) })), &shown); err != nil || !shown.Sealed || len(shown.Entries) != 2 {
		t.Errorf("journal show --json = %+v, %v", shown, err)
	}

	for name, args := range map[string][]string{
		"no subcommand":      nil,
		"an unknown one":     {"frobnicate"},
		"show with no run":   {"show", "--dir", dir},
		"show a run not had": {"show", "0000aaaa-0000-4000-8000-000000000000", "--dir", dir},
		"an unknown flag":    {"list", "--colour"},
	} {
		if err := runJournal(args); err == nil {
			t.Errorf("%s: journal %v succeeded", name, args)
		}
	}
	if out := captureStdout(t, func() { _ = runJournal([]string{"list", "--dir", t.TempDir()}) }); !strings.Contains(out, "no runs recorded") {
		t.Errorf("an empty project: %q", out)
	}
}

func TestRollbackCommand_RefusesWithEveryProblemAndTheFlagForIt(t *testing.T) {
	dir := rollbackProject(t, false)
	var err error
	out := captureStdout(t, func() { err = runRollback([]string{cliRunID, "--dir", dir}) })
	if err == nil || !strings.Contains(out, "refused, before any device was contacted") || !strings.Contains(out, "accept it with --leave tasks[1]") {
		t.Fatalf("rollback = %v:\n%s", err, out)
	}
	var rep runReport
	out = captureStdout(t, func() { err = runRollback([]string{cliRunID, "--dir", dir, "--json"}) })
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || err == nil || rep.Rollback == nil || len(rep.Rollback.Problems) != 1 || rep.Rollback.Problems[0].Accept != "--leave tasks[1]" || rep.Outcome.Status != "invalid" {
		t.Errorf("rollback --json = %+v (%v, %v)", rep.Rollback, err, jerr)
	}

	// A run holding the lock: the rollback waits for nothing and says so.
	release, lerr := journal.LockRuns(dir, false)
	if lerr != nil {
		t.Fatal(lerr)
	}
	out = captureStdout(t, func() { err = runRollback([]string{cliRunID, "--dir", dir, "--json"}) })
	_ = release()
	if !errors.Is(err, journal.ErrRunsBusy) || !strings.Contains(out, "a run is in progress") {
		t.Errorf("a rollback while a run holds the lock: %v\n%s", err, out)
	}

	for name, args := range map[string][]string{
		"no run id":          {"--dir", dir},
		"an empty leave":     {cliRunID, "--dir", dir, "--leave", ""},
		"a run not recorded": {"0000aaaa-0000-4000-8000-000000000000", "--dir", dir},
		"no inventory":       {cliRunID, "--dir", t.TempDir()},
	} {
		if err := runRollback(args); err == nil {
			t.Errorf("%s: rollback %v succeeded", name, args)
		}
	}
}

// TestRollbackCommand_RunsNothingWhenNothingIsLeft covers a plan with no
// steps: every change named to leave, a check of that, and an unknown
// effect nobody accepted, which ends incomplete.
func TestRollbackCommand_RunsNothingWhenNothingIsLeft(t *testing.T) {
	dir := rollbackProject(t, false)
	leaveAll := []string{cliRunID, "--dir", dir, "--leave", "tasks[0]", "--leave", "tasks[1]"}
	var err error
	out := captureStdout(t, func() { err = runRollback(leaveAll) })
	if err != nil || !strings.Contains(out, "left in place: tasks[0] on web1: named to leave in place") || !strings.Contains(out, "nothing to run") {
		t.Errorf("everything left: %v\n%s", err, out)
	}
	out = captureStdout(t, func() { err = runRollback(append(leaveAll, "--mode", "check", "--json")) })
	var rep runReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || err != nil || rep.Outcome.Status != "complete" || len(rep.Rollback.Left) != 2 {
		t.Errorf("a check of it as JSON: %v %v\n%s", err, jerr, out)
	}

	failed := engine.JournalEntry{RunID: cliRunID, Sequence: 3, NodeID: "tasks[2]", DeviceID: cliDeviceID, FQCN: "exec.command",
		Outcome: engine.OutcomeFailed, FailureStage: engine.FailureStageAction, StartedAt: time.Now().UTC()}
	unknown := rollbackProject(t, false, failed)
	args := []string{cliRunID, "--dir", unknown, "--leave", "tasks[0]", "--leave", "tasks[1]"}
	out = captureStdout(t, func() { err = runRollback(args) })
	if exitCodeOf(err) != exitIncomplete || !strings.Contains(out, "unknown: tasks[2] on web1") || !strings.Contains(out, "--allow-unknown") {
		t.Errorf("an unaccepted unknown: exit %d (%v)\n%s", exitCodeOf(err), err, out)
	}
	if err := runRollback(append(args, "--allow-unknown", "tasks[2]")); err != nil {
		t.Errorf("an accepted unknown: %v", err)
	}
}

// TestRollbackCommand_FindsTheRunbookThatRan covers where an authored
// rollback: list is read from: the runbook named, or the one under
// runbooks/ at the journal's version, and never one that has changed.
func TestRollbackCommand_FindsTheRunbookThatRan(t *testing.T) {
	dir := rollbackProject(t, true)
	run, err := journal.ReadRun(dir, cliRunID)
	if err != nil {
		t.Fatal(err)
	}
	authored := authoredFrom(dir, "", run.Entries)
	steps, err := authored("tasks[1]")
	if err != nil || len(steps) != 1 || steps[0].Params["line"] != "a = c" {
		t.Fatalf("the list under runbooks/: %+v, %v", steps, err)
	}
	if _, err := authored("tasks[7]"); err == nil {
		t.Error("a node the runbook lacks was found")
	}
	explicit := filepath.Join(dir, "runbooks", "rb-cli.yaml")
	if steps, err := authoredFrom(dir, explicit, run.Entries)("tasks[1]"); err != nil || len(steps) != 1 {
		t.Errorf("--runbook naming it: %v, %v", steps, err)
	}

	// Two copies: which one ran is ambiguous.
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "copy.yaml"), []byte(cliRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authoredFrom(dir, "", run.Entries)("tasks[1]"); err == nil || !strings.Contains(err.Error(), "2 runbooks") {
		t.Errorf("two copies: %v", err)
	}
	// Changed since: neither the named one nor any found is the one that ran.
	changed := strings.Replace(cliRunbook, "make a directory", "make the directory", 1)
	for _, name := range []string{"rb-cli.yaml", "copy.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, "runbooks", name), []byte(changed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := authoredFrom(dir, explicit, run.Entries)("tasks[1]"); err == nil || !strings.Contains(err.Error(), "has changed since") {
		t.Errorf("--runbook changed since: %v", err)
	}
	if _, err := authoredFrom(dir, "", run.Entries)("tasks[1]"); !errors.Is(err, rollback.ErrNoRunbook) {
		t.Errorf("none at its version: %v", err)
	}
	if _, err := authoredFrom(dir, filepath.Join(dir, "missing.yaml"), run.Entries)("tasks[1]"); err == nil {
		t.Error("a --runbook that does not exist was read")
	}

	// And the command says so: the authored undo cannot be found.
	var rerr error
	out := captureStdout(t, func() { rerr = runRollback([]string{cliRunID, "--dir", dir}) })
	if rerr == nil || !strings.Contains(out, "--runbook") {
		t.Errorf("a rollback whose list cannot be found: %v\n%s", rerr, out)
	}
}

// TestRollbackOutcome_AnUnknownMakesACompleteRollbackIncomplete covers the
// one outcome rollback changes.
func TestRollbackOutcome_AnUnknownMakesACompleteRollbackIncomplete(t *testing.T) {
	done := runOutcome{Status: "complete", ExitCode: 0}
	if got := rollbackOutcome(done, &rollbackReport{}); got.Status != "complete" || got.ExitCode != 0 {
		t.Errorf("nothing unknown: %+v", got)
	}
	if got := rollbackOutcome(done, &rollbackReport{UnacceptedUnknown: 2}); got.ExitCode != exitIncomplete || !strings.Contains(got.Message, "2 effect(s)") {
		t.Errorf("two unknown: %+v", got)
	}
	failed := runOutcome{Status: "failed", ExitCode: 1}
	if got := rollbackOutcome(failed, &rollbackReport{UnacceptedUnknown: 1}); got.Status != "failed" || got.ExitCode != 1 {
		t.Errorf("a failed rollback stays failed: %+v", got)
	}
}
