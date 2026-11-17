// In-process tests that `pleiades rollback` leaves a change a block's always:
// task made in place without a --leave, says so once, and still refuses a
// change with no undo anywhere else.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// cleanupRunbook makes a directory in a block and removes a scratch file in
// the block's always:, the shape of a lab made and torn down.
const cleanupRunbook = `id: rb-cleanup
hosts: web1
tasks:
  - block:
      - name: make a directory
        file.directory:
          path: /tmp/cleanup-made
    always:
      - name: tear the scratch down
        exec.command:
          argv: [rm, -rf, /tmp/cleanup-scratch]
`

// cleanupProject is a project holding web1, cleanupRunbook under runbooks/,
// and a sealed journal of one run of it whose entries are written by
// entries, given the DAG version.
func cleanupProject(t *testing.T, entries func(version string, at time.Time) []engine.JournalEntry) string {
	t.Helper()
	dir := t.TempDir()
	inv := "hosts:\n  - id: " + cliDeviceID + "\n    name: web1\n    type: linux_server\n    properties:\n      host: 10.0.0.1\n"
	if err := os.WriteFile(filepath.Join(dir, "inventory.yaml"), []byte(inv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "runbooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runbooks", "rb-cleanup.yaml")
	if err := os.WriteFile(path, []byte(cleanupRunbook), 0o600); err != nil {
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
	store, err := journal.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), entries(dag.Version, at)); err != nil {
		t.Fatal(err)
	}
	if err := store.Seal(cliRunID, at); err != nil {
		t.Fatal(err)
	}
	return dir
}

// cliEntry is one entry of the run cleanupProject records.
func cliEntry(version string, at time.Time, seq int, node, fqcn string, outcome engine.Outcome) engine.JournalEntry {
	return engine.JournalEntry{RunID: cliRunID, Sequence: seq, NodeID: node, DeviceID: cliDeviceID, FQCN: fqcn,
		DAGID: "rb-cleanup", DAGVersion: version, Outcome: outcome, ActionChanged: outcome == engine.OutcomeChanged,
		TaskName: "task " + node, StartedAt: at, FinishedAt: at.Add(time.Second)}
}

// TestRollbackCommand_LeavesAnAlwaysCleanupInPlace: the always: task's
// change (exec.command, which records no undo) is left in place without a
// --leave and said once, as text and in the JSON report; with the block's
// change left too there is nothing to run. Before, it was refused until named
// with --leave tasks[0].always[0].
func TestRollbackCommand_LeavesAnAlwaysCleanupInPlace(t *testing.T) {
	dir := cleanupProject(t, func(version string, at time.Time) []engine.JournalEntry {
		made := cliEntry(version, at, 1, "tasks[0].block[0]", "file.directory", engine.OutcomeChanged)
		text := "/tmp/cleanup-made"
		made.InverseFQCN, made.InverseParamKeys, made.InverseComplete = "file.remove", []string{"path"}, true
		made.InverseParams = []engine.InverseParam{{Key: "path", Text: &text}}
		return []engine.JournalEntry{made, cliEntry(version, at, 2, "tasks[0].always[0]", "exec.command", engine.OutcomeChanged)}
	})
	args := []string{cliRunID, "--dir", dir, "--leave", "tasks[0].block[0]"}
	var err error
	out := captureStdout(t, func() { err = runRollback(args) })
	if err != nil || !strings.Contains(out, "cleanup left in place: tasks[0].always[0] on web1: it ran in a block's always:") || !strings.Contains(out, "nothing to run") {
		t.Fatalf("rollback = %v:\n%s", err, out)
	}
	if strings.Count(out, "tasks[0].always[0]") != 1 {
		t.Errorf("the cleanup is not said exactly once:\n%s", out)
	}
	var rep runReport
	out = captureStdout(t, func() { err = runRollback(append(args, "--json")) })
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || err != nil || rep.Rollback == nil || len(rep.Rollback.Cleanups) != 1 ||
		rep.Rollback.Cleanups[0].Node != "tasks[0].always[0]" || len(rep.Rollback.Problems) != 0 || rep.Outcome.Status != "complete" {
		t.Errorf("rollback --json = %+v (%v, %v)", rep.Rollback, err, jerr)
	}
}

// TestRollbackCommand_StillRefusesABlockChangeWithNoUndo: the same command
// outside always: is a change with no undo, refused until named with --leave,
// so leaving a cleanup in place widens nothing else.
func TestRollbackCommand_StillRefusesABlockChangeWithNoUndo(t *testing.T) {
	dir := cleanupProject(t, func(version string, at time.Time) []engine.JournalEntry {
		return []engine.JournalEntry{cliEntry(version, at, 1, "tasks[0].block[0]", "exec.command", engine.OutcomeChanged)}
	})
	var err error
	out := captureStdout(t, func() { err = runRollback([]string{cliRunID, "--dir", dir}) })
	if err == nil || !strings.Contains(out, "accept it with --leave tasks[0].block[0]") {
		t.Errorf("a block change with no undo: %v\n%s", err, out)
	}
}
