// Package journal_test: a run with handlers reads back.
package journal_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestReadRunAcceptsTheHandlersEntriesTheEngineWrites proves a handler's
// node ("tasks.handlers[i]", with a loop's item suffix too) and a handler
// skipped as not notified read back, and that a handler holding a block,
// which the engine refuses to build, is still refused here. Before the fix
// the reader refused both handler entries, and with them every journal in
// the directory (FAILURE_PATTERNS 959).
func TestReadRunAcceptsTheHandlersEntriesTheEngineWrites(t *testing.T) {
	notified := entry("run-h", 1, "tasks.handlers[0]")
	looped := entry("run-h", 2, "posttasks.handlers[1].item[2]")
	skipped := entry("run-h", 3, "pretasks.handlers[1]")
	skipped.Outcome, skipped.SkipKind = engine.OutcomeSkipped, engine.SkipKindNotNotified
	store, root := newStore(t)
	if err := store.Record(context.Background(), []engine.JournalEntry{notified, looped, skipped}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	run, err := journal.ReadRun(root, "run-h")
	if err != nil {
		t.Fatalf("a run with handlers does not read back: %v", err)
	}
	if len(run.Entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(run.Entries))
	}

	block := entry("run-b", 1, "tasks.handlers[0].block[0]")
	store2, root2 := newStore(t)
	if err := store2.Record(context.Background(), []engine.JournalEntry{block}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := journal.ReadRun(root2, "run-b"); err == nil {
		t.Error("a handler holding a block, which the engine never builds, was read")
	}
}
