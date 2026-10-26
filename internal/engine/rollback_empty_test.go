// Tests for a present, empty rollback: list, rollback: [], which says undoing
// a task means doing nothing: the parser keeps it apart from no list, a
// group may not carry one, the journal records it as authored, and a block's
// rollback_on_failure runs no undo for it and goes on to its rescue rather
// than refusing it as a change it cannot undo.
package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

func TestRollbackKey_AnEmptyListIsKeptApartFromNone(t *testing.T) {
	registerRollbackFixtures(t)
	dag, err := newTestBuilder(t).BuildFromYAML([]byte(`id: rb
hosts: web
tasks:
  - name: read only
    rbtest.make: {name: seen}
    rollback: []
  - name: no list
    rbtest.make: {name: made}
`))
	if err != nil {
		t.Fatalf("BuildFromYAML: %v", err)
	}
	if got := dag.Nodes["tasks[0]"].Rollback; got == nil || len(got) != 0 {
		t.Errorf("rollback: [] parsed as %#v, want an empty, non-nil list", got)
	}
	if got := dag.Nodes["tasks[1]"].Rollback; got != nil {
		t.Errorf("no rollback: parsed as %#v, want nil", got)
	}
}

func TestRollbackKey_AnEmptyListOnABlockIsRefused(t *testing.T) {
	registerRollbackFixtures(t)
	_, err := newTestBuilder(t).BuildFromYAML([]byte(`id: rb
hosts: web
tasks:
  - name: group
    rollback: []
    block:
      - name: inner
        rbtest.make: {name: one}
`))
	if err == nil || !strings.Contains(err.Error(), "on a block or parallel group") {
		t.Fatalf("err = %v, want rollback: on a block refused, empty or not", err)
	}
}

// TestRollbackOnFailure_AnEmptyListUndoesNothingAndTheRescueRuns: the
// block's second task changes the device and records no undo, and its
// rollback: list is empty. When b2 fails on web1, b0 is undone, nothing
// runs for the read, and the rescue answers the failure; the journal says
// the read had an authored undo, and journals no undo of it.
func TestRollbackOnFailure_AnEmptyListUndoesNothingAndTheRescueRuns(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"b2@web1": true}}
	journal := &entriesJournal{}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: bare read
        rbtest.make: {name: seen}
        rollback: []
      - name: b2
        rbtest.make: {name: two}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions, engine.WithJournal(journal))

	if got, want := actions.calls("web1"), []string{"b0", "bare read", "b2", "unmake:zero", "r0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v: %+v", result.HasErrors(), result.Rescued(), result.Nodes)
	}
	var authored, empty bool
	for _, e := range journal.entries {
		if e.NodeID == "tasks[0].block[1]" && e.DeviceID != "" {
			authored = authored || e.AuthoredRollback
			empty = empty || e.AuthoredRollbackEmpty
		}
		if e.NodeID == "tasks[0].block[0]" && e.AuthoredRollbackEmpty {
			t.Errorf("a task with no rollback: list journaled as an empty one: %+v", e)
		}
		if e.UndoesNode == "tasks[0].block[1]" {
			t.Errorf("journaled an undo of the read: %+v", e)
		}
	}
	if !authored {
		t.Error("the read's entry does not say it had an authored rollback: list, so a later rollback would look for a recorded undo")
	}
	if !empty {
		t.Error("the read's entry does not say its rollback: list is empty, so a rollback of an earlier run cannot tell it needs no undo")
	}
}
