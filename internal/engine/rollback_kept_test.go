// Tests for an undo kept through a failure (sdk.Inverse.KeepOnFailure): a
// method that confirmed its change and then failed (a read-back that lost
// its connection) keeps its undo, the journal holds the failed task as a
// change with that undo, and a block's rollback_on_failure undoes it first.
// A method whose manifest does not allow it keeps nothing.
package engine_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerKeptFixture registers rbtest.keep beside the rollback fixtures:
// rbtest.make's twin whose manifest allows its undo through rbtest.unmake
// to be kept through a failure.
func registerKeptFixture(t *testing.T) {
	t.Helper()
	registerRollbackFixtures(t)
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "rbtest.keep", Invoke: none, Manifest: collection.Manifest{
		Status: collection.StatusImplemented,
		Doc:    collection.Doc{Params: []collection.Param{{Name: "name", Type: "any"}}},
		Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{
			{FQCN: "rbtest.unmake", Record: []string{"name"}, MayKeepOnFailure: true},
		}},
	}}); err != nil {
		t.Fatalf("registering rbtest.keep: %v", err)
	}
}

// TestKeptUndo_TheFailedTaskIsUndoneFirst: b1 changes web1, keeps its undo
// and fails; the block undoes b1's change and then b0's, newest first,
// before its rescue runs.
func TestKeptUndo_TheFailedTaskIsUndoneFirst(t *testing.T) {
	registerKeptFixture(t)
	actions := &undoable{keepAndFail: map[string]bool{"b1@web1": true}}
	result := runUndoable(t, `id: kept
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
      - name: b1
        rbtest.keep: {name: one}
    rescue:
      - name: r0
        noop: {}
`, twoDevices(), actions)

	if got, want := actions.calls("web1"), []string{"b0", "b1", "unmake:one", "unmake:zero", "r0"}; !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if !result.Rescued() || result.HasErrors() {
		t.Errorf("Rescued %v, HasErrors %v: %+v", result.Rescued(), result.HasErrors(), result.Nodes)
	}
}

// TestKeptUndo_TheJournalHoldsTheFailedChange: with no block to undo it,
// the failed task is journaled as having changed the device, with its undo
// held exactly, so a later rollback plans it rather than refusing an
// unknown effect.
func TestKeptUndo_TheJournalHoldsTheFailedChange(t *testing.T) {
	registerKeptFixture(t)
	actions := &undoable{keepAndFail: map[string]bool{"k@web1": true}}
	journal := &entriesJournal{}
	runUndoable(t, `id: kept
hosts: web
tasks:
  - name: k
    rbtest.keep: {name: one}
`, twoDevices(), actions, engine.WithJournal(journal))

	var found bool
	for _, e := range journal.entries {
		if e.NodeID != "tasks[0]" || e.Outcome != engine.OutcomeFailed {
			continue
		}
		found = true
		if !e.ActionChanged || e.InverseFQCN != "rbtest.unmake" || !e.InverseComplete {
			t.Errorf("failed entry: changed %v, undo %q, complete %v; want a change with its undo held exactly", e.ActionChanged, e.InverseFQCN, e.InverseComplete)
		}
	}
	if !found {
		t.Fatalf("no failed entry for tasks[0] in %+v", journal.entries)
	}
}

// TestKeptUndo_AMethodNotAllowedKeepsNothing: rbtest.make's manifest does
// not allow a kept undo, so the flag it sets is ignored: the failed task
// is journaled with no undo and no change, as any failed task is.
func TestKeptUndo_AMethodNotAllowedKeepsNothing(t *testing.T) {
	registerKeptFixture(t)
	actions := &undoable{keepAndFail: map[string]bool{"m@web1": true}}
	journal := &entriesJournal{}
	runUndoable(t, `id: kept
hosts: web
tasks:
  - name: m
    rbtest.make: {name: one}
`, twoDevices(), actions, engine.WithJournal(journal))

	for _, e := range journal.entries {
		if e.NodeID == "tasks[0]" && e.Outcome == engine.OutcomeFailed && (e.ActionChanged || e.InverseFQCN != "") {
			t.Errorf("a failed task of a method not allowed to keep its undo journaled changed %v, undo %q", e.ActionChanged, e.InverseFQCN)
		}
	}
}
