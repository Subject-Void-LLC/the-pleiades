// Tests of the provenance the engine stamps on a task's facts
// (factProvenance), from inside the package.
package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestFactProvenance_DropsAnIdentityTheMethodDoesNotAgreeWith: an identity
// CheckCollection refuses for the method (here a program's, cached for a
// built-in of a first-party group, which neither BuiltinIdentity nor the
// loader writes) is left out and the rest of the provenance kept, so the
// store takes the facts rather than refusing them whole.
func TestFactProvenance_DropsAnIdentityTheMethodDoesNotAgreeWith(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	const method = "gate.factprovtest"
	if err := collection.Register(collection.Descriptor{Name: method, Invoke: noop,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	claimed := wire.CollectionIdentity{Group: "acme_tools", Program: true, Manifests: wire.ManifestDigestPrefix + strings.Repeat("0", 64)}
	r := &run{runID: "run-under-test", identities: map[string]wire.CollectionIdentity{method: claimed}}
	by := r.factProvenance(NodeResult{NodeID: "tasks[0]", Device: "dev-1", factMethod: method})
	if !by.Collection.IsZero() || by.Method != method || by.Run != "run-under-test" || by.Node != "tasks[0]" {
		t.Fatalf("provenance %+v, want the run, node and method with no identity", by)
	}

	// The same method with its own group's identity keeps it.
	own, err := collection.BuiltinIdentity(method, coreVersion())
	if err != nil {
		t.Fatal(err)
	}
	r.identities[method] = own
	if by := r.factProvenance(NodeResult{NodeID: "tasks[0]", Device: "dev-1", factMethod: method}); by.Collection != own {
		t.Fatalf("provenance collection %+v, want the group's own %+v", by.Collection, own)
	}
}
