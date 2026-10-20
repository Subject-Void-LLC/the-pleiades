// Package journal_test: a run's own undo steps, which a block's
// rollback_on_failure journals as a rollback of the run itself, read back.
package journal_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestReadRun_ReadsARunsOwnUndoSteps: a step undoing tasks[1].block[0],
// journaled as tasks[1].block[0].undo[0] with RollbackOf naming its own run,
// reads back as written; an id with anything after .undo[k], or an undo of
// an undo, is still refused as no graph id.
func TestReadRun_ReadsARunsOwnUndoSteps(t *testing.T) {
	write := func(t *testing.T, node, undoes string) (journal.RunJournal, error) {
		t.Helper()
		root := t.TempDir()
		store, err := journal.NewFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		entries := []engine.JournalEntry{
			{RunID: readerRun, Sequence: 1, NodeID: "tasks[1].block[0]", DAGVersion: digest, Outcome: engine.OutcomeChanged, StartedAt: now, FinishedAt: now},
			{RunID: readerRun, Sequence: 2, NodeID: node, DAGVersion: digest, Outcome: engine.OutcomeChanged, StartedAt: now, FinishedAt: now,
				RollbackOf: readerRun, UndoesNode: undoes},
		}
		if err := store.Record(context.Background(), entries); err != nil {
			t.Fatal(err)
		}
		if err := store.Seal(readerRun, now); err != nil {
			t.Fatal(err)
		}
		return journal.ReadRun(root, readerRun)
	}

	run, err := write(t, "tasks[1].block[0].undo[0]", "tasks[1].block[0]")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if got := run.Entries[1]; got.NodeID != "tasks[1].block[0].undo[0]" || got.UndoesNode != "tasks[1].block[0]" || got.RollbackOf != readerRun {
		t.Errorf("read back %+v", got)
	}
	for _, bad := range []string{"tasks[1].block[0].undo[0].undo[1]", "tasks[1].undo[0].block[0]", "tasks[1].undo[x]"} {
		if _, err := write(t, bad, "tasks[1].block[0]"); err == nil || !strings.Contains(err.Error(), "not a graph id") {
			t.Errorf("node %q = %v, want refused as no graph id", bad, err)
		}
	}
}
