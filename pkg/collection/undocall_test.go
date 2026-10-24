// Tests of Descriptor.UndoCall's registration: a method that is not
// reversible may not answer whether a call records an undo.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestRegister_UndoCallNeedsAReversibleMethod(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	undo := func(map[string]any) string { return "" }
	never := collection.Descriptor{Name: "undocall.never", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"},
	}, Invoke: run, UndoCall: undo}
	if err := collection.Register(never); err == nil || !strings.Contains(err.Error(), "UndoCall") {
		t.Errorf("Register = %v, want an UndoCall on an irreversible method refused", err)
	}
	target := sdk.InverseSpec{FQCN: "undocall.target"}
	whole := collection.Descriptor{Name: "undocall.whole", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{target}},
	}, Invoke: run, UndoCall: undo}
	if err := collection.Register(whole); err != nil {
		t.Errorf("Register = %v, want a reversible method's UndoCall accepted", err)
	}
}
