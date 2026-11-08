// Tests for the Collection identity a journal entry records
// (journal_identity.go): a built-in's group identity and its undo's digest,
// an external program's identity as the loader set it, and none for what no
// Collection provides.
package engine

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registerIdentityFixtures registers, in namespace ns, an emitter whose undo
// calls a target, the emitter provided by provider (nil for a built-in, in a
// first-party group's namespace; a program's in its own), and returns both
// descriptors.
func registerIdentityFixtures(t *testing.T, ns string, provider *collection.Provider) (emitter, target collection.Descriptor) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	target = collection.Descriptor{
		Name: ns + ".journalidtest_target",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "name", Type: "string", Required: true}}}},
		Invoke: noop,
	}
	emitter = collection.Descriptor{
		Name: ns + ".journalidtest_emitter",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{
				{FQCN: target.Name, Record: []string{"name"}},
			}}},
		Invoke:   noop,
		Provider: provider,
	}
	for _, d := range []collection.Descriptor{target, emitter} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
	return emitter, target
}

// TestJournalEntry_RecordsABuiltinsCollectionIdentity: a built-in's entry
// names its group at this build's version and the group's digest, and its
// undo the digest of the emitter's declaration over the target's params.
func TestJournalEntry_RecordsABuiltinsCollectionIdentity(t *testing.T) {
	emitter, target := registerIdentityFixtures(t, "gate", nil)
	entry := projectUndo(t, emitter.Name, target.Name, map[string]any{"name": "web"}, false)

	want, err := collection.BuiltinIdentity(emitter.Name, coreVersion())
	if err != nil {
		t.Fatal(err)
	}
	if entry.Collection != want || want.Group != "gate" || want.Version == "" {
		t.Errorf("Collection = %+v, want %+v (group gate at this build's version)", entry.Collection, want)
	}
	undo := collection.UndoDigest(emitter.Manifest.Reversibility.Inverses[0], fragment.ByName(target))
	if entry.InverseSpecDigest != undo || !entry.InverseComplete {
		t.Errorf("InverseSpecDigest = %q (complete %v), want %q", entry.InverseSpecDigest, entry.InverseComplete, undo)
	}
}

// TestJournalEntry_RecordsAProgramsIdentityAsTheLoaderSetIt: a method an
// external program provides records the identity on its Provider, and its
// undo, which the journal never honors for a program, no digest.
func TestJournalEntry_RecordsAProgramsIdentityAsTheLoaderSetIt(t *testing.T) {
	id := wire.CollectionIdentity{Group: "acme_tools", Program: true, Manifests: wire.ManifestDigestPrefix + "00"}
	provider := &collection.Provider{Program: "/opt/collections/acme_tools", Digest: "sha256:00", Identity: id}
	emitter, target := registerIdentityFixtures(t, "acmeidtest", provider)
	r := &run{
		dag:   buildInternalDAG(t, `{"id": "identity", "tasks": [{"name": "a", "fqcn": "`+emitter.Name+`"}]}`),
		runID: "run-under-test",
	}
	// As the executor sets it, from the registered descriptor (externalProvider).
	n := NodeResult{NodeID: "tasks[0]", Device: "dev-1", Changed: true, Provider: externalProvider(emitter.Name)}
	n.journalStats = map[string]any{sdk.StatInverse: map[string]any{
		sdk.InverseFQCNKey: target.Name, sdk.InverseParamsKey: map[string]any{"name": "web"},
	}}
	entry, err := r.projectResult(n)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Collection != id {
		t.Errorf("Collection = %+v, want the provider's %+v", entry.Collection, id)
	}
	if entry.InverseSpecDigest != "" {
		t.Errorf("a program's undo recorded InverseSpecDigest %q", entry.InverseSpecDigest)
	}
}

// TestJournalEntry_RecordsNoIdentityWhereNoCollectionRan: an engine action,
// a name nothing registers, and a built-in in no first-party group record
// none.
func TestJournalEntry_RecordsNoIdentityWhereNoCollectionRan(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "journalidtest.nogroup", Invoke: noop,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	for _, fqcn := range []string{"noop", "nosuch.method", "journalidtest.nogroup"} {
		r := &run{
			dag:   buildInternalDAG(t, `{"id": "identity", "tasks": [{"name": "a", "fqcn": "`+fqcn+`"}]}`),
			runID: "run-under-test",
		}
		entry, err := r.projectResult(NodeResult{NodeID: "tasks[0]", Device: "dev-1"})
		if err != nil {
			t.Fatal(err)
		}
		if !entry.Collection.IsZero() {
			t.Errorf("%s recorded Collection %+v, want none", fqcn, entry.Collection)
		}
	}
}
