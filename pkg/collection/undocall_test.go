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

// TestRegister_UndoCallDecidesPartialMustDecideSomething: the declaration
// that an UndoCall names every partial call is refused with no UndoCall,
// and on a method with no inverse that may be partial; with both it is
// accepted.
func TestRegister_UndoCallDecidesPartialMustDecideSomething(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	undo := func(map[string]any) string { return "" }
	partial := collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "undocall.target", MayBePartial: true}}}
	exact := collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{{FQCN: "undocall.target"}}}
	for _, c := range []struct {
		name    string
		r       collection.Reversibility
		undo    func(map[string]any) string
		refused string
	}{
		{"undocall.nocall", partial, nil, "no UndoCall"},
		{"undocall.nopartial", exact, undo, "no inverse that may be partial"},
		{"undocall.decides", partial, undo, ""},
	} {
		err := collection.Register(collection.Descriptor{Name: c.name, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: c.r,
		}, Invoke: run, UndoCall: c.undo, UndoCallDecidesPartial: true})
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s: Register = %v, want it accepted", c.name, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%s: Register = %v, want a refusal saying %q", c.name, err, c.refused)
		}
	}
}
