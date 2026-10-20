// Tests that the journal reads a method's parameters as validate and the
// rollback planner do: its Doc.Params and every param its named fragments
// add. Every container.* undo carries the engine param, which
// container.remove and its kin declare through the container_engine
// fragment alone; read from Doc.Params, each such undo was journaled
// incomplete and no rollback could replay it.
package engine

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerFragmentUndoFixtures registers an emitter whose undo calls a
// target declaring name in its Doc.Params and engine only through the
// container_engine fragment, as container.remove does, and returns the
// emitter's name.
func registerFragmentUndoFixtures(t *testing.T) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	target := collection.Descriptor{
		Name: "fragmenttest.remove",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{
				Params:    []collection.Param{{Name: "name", Type: "string"}},
				Fragments: []string{"container_engine"},
			}},
		Invoke: noop,
	}
	emitter := collection.Descriptor{
		Name: "fragmenttest.run",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{
				{FQCN: "fragmenttest.remove", Record: []string{"name", "engine"}},
			}}},
		Invoke: noop,
	}
	for _, d := range []collection.Descriptor{target, emitter} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
	return emitter.Name
}

func TestProjectInverse_AFragmentsParamIsDeclared(t *testing.T) {
	emitter := registerFragmentUndoFixtures(t)
	entry := projectUndo(t, emitter, "fragmenttest.remove", map[string]any{"name": "web", "engine": "docker"}, false)
	if entry.UndeclaredInverseParamCount != 0 {
		t.Errorf("%d undo param(s) counted undeclared, want none: engine is the container_engine fragment's", entry.UndeclaredInverseParamCount)
	}
	got := recorded(t, entry)
	if got["engine"] != `"docker"` || got["name"] != `"web"` {
		t.Errorf("recorded %v, want name and engine", got)
	}
	if !entry.InverseComplete {
		t.Error("the undo is not complete, so a rollback would refuse to replay it")
	}
}

func TestProjectInverse_AKeyNoParamOrFragmentDeclaresIsStillUndeclared(t *testing.T) {
	emitter := registerFragmentUndoFixtures(t)
	entry := projectUndo(t, emitter, "fragmenttest.remove", map[string]any{"name": "web", "engine": "docker", "volumes": true}, false)
	if entry.UndeclaredInverseParamCount != 1 || entry.InverseComplete {
		t.Errorf("undeclared %d complete %v, want the one key nothing declares counted and the undo incomplete",
			entry.UndeclaredInverseParamCount, entry.InverseComplete)
	}
}
