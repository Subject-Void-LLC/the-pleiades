// Tests that a stored fact names the Collection its method came from (Phase
// 136b), the identity the journal records for the same node.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registerIdentityGather registers name, a method that emits one declared
// fact, provided by provider (nil for a built-in). A program's fact goes
// under its own namespace, as collection.Register requires.
func registerIdentityGather(t *testing.T, name string, provider *collection.Provider) {
	t.Helper()
	key := "os.kernel.release"
	if provider != nil {
		key = strings.SplitN(name, ".", 2)[0] + ".os.kernel.release"
	}
	gather := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, rc.EmitFact(key, "6.1")
	}
	if err := collection.Register(collection.Descriptor{Name: name, Invoke: gather, Check: gather, Provider: provider,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture"},
			Facts: []collection.FactSpec{{Key: key, Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"}}}}); err != nil {
		t.Fatal(err)
	}
}

// gatherBy runs method once on the fact fleet and returns the provenance of
// what it stored, failing unless exactly one observation was.
func gatherBy(t *testing.T, method string) facts.Provenance {
	t.Helper()
	sink := &factSink{got: map[string][]facts.Entry{}}
	if _, err := newCheckExecutor(factFleet(), engine.WithFactRecorder(sink)).Run(context.Background(),
		buildDAG(t, `{"id": "facts", "tasks": [{"name": "g", "`+method+`": {"target": "fleet"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if len(sink.by) != 1 || len(sink.got["dev-1"]) != 1 {
		t.Fatalf("stored %+v by %+v, want one fact in one observation", sink.got, sink.by)
	}
	return sink.by[0]
}

// TestFacts_AStoredFactNamesItsBuiltinsCollection: a built-in's facts name
// its first-party group at this build's version and the group's digest, as
// its journal entry does.
func TestFacts_AStoredFactNamesItsBuiltinsCollection(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	registerIdentityGather(t, "gate.factidtest_gather", nil)
	want, err := collection.BuiltinIdentity("gate.factidtest_gather", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	if by := gatherBy(t, "gate.factidtest_gather"); by.Collection != want || want.Group != "gate" {
		t.Fatalf("stored collection %+v, want %+v", by.Collection, want)
	}
}

// TestFacts_AMethodInNoGroupStoresNoIdentity: a method in no first-party
// group, with no provider, records none, as its journal entry does.
func TestFacts_AMethodInNoGroupStoresNoIdentity(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	registerIdentityGather(t, "factidtest.nogroup", nil)
	if by := gatherBy(t, "factidtest.nogroup"); !by.Collection.IsZero() {
		t.Fatalf("a method in no first-party group stored collection %+v", by.Collection)
	}
}

// TestFacts_AProgramsFactsNameTheIdentityTheLoaderSet: a method an external
// program provides stores the identity on its descriptor's Provider, which
// the loader computes, never one the program sends.
func TestFacts_AProgramsFactsNameTheIdentityTheLoaderSet(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	digest := wire.ManifestDigestPrefix + strings.Repeat("0", 64)
	program := wire.CollectionIdentity{Group: "acme_tools", Program: true, Manifests: digest}
	registerIdentityGather(t, "acmefactidtest.gather", &collection.Provider{Program: "/opt/collections/acme_tools", Digest: digest, Identity: program})
	if by := gatherBy(t, "acmefactidtest.gather"); by.Collection != program {
		t.Fatalf("stored collection %+v, want the loader's %+v", by.Collection, program)
	}
}
