// Tests that a dispatch names the Collections its methods belong to as this
// Controller's installed catalog identifies them (Phase 136b,
// wire.DispatchPayload.Collections).
package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// installGroupCatalog installs a module catalog holding a method of the file
// group and one of the net group, as a Controller that registers no
// Collection holds them, and returns the two manifests by name.
func installGroupCatalog(t *testing.T) map[string]collection.Manifest {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	manifests := map[string]collection.Manifest{
		"file.test_remove": {Status: collection.StatusImplemented, SupportedTransports: []string{capability.TransportSSH}},
		"net.test_ping":    {Status: collection.StatusImplemented, SupportedTransports: []string{capability.TransportSSH}},
	}
	for name := range manifests {
		if _, ok := collection.Lookup(name); ok {
			t.Fatalf("%s is registered, so this would not test the catalog", name)
		}
	}
	catalog, err := json.Marshal(manifests)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	return manifests
}

// publishedPayloads decodes every dispatch bus carried.
func publishedPayloads(t *testing.T, bus *capturingBus) []wire.DispatchPayload {
	t.Helper()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	out := make([]wire.DispatchPayload, 0, len(bus.published))
	for _, evt := range bus.published {
		var p wire.DispatchPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// TestWorker_NamesEachGroupItsRunbookRunsFromTheCatalog: a runbook calling a
// file method, a net method only under a condition, an engine action and a
// method no catalog holds (an external program's) is dispatched with exactly
// the file and net groups' identities, keyed by group, each the group's name,
// this build's version and the digest of the catalog's manifests for it. The
// conditional task counts, since a condition that holds on the Runner runs
// it.
func TestWorker_NamesEachGroupItsRunbookRunsFromTheCatalog(t *testing.T) {
	manifests := installGroupCatalog(t)
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{&inventorytest.Stub{StubID: "dev-1", StubName: "web1", StubState: pkginventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}}}
	dir := t.TempDir()
	content := `id: mixed
tasks:
  - name: remove
    file.test_remove:
      path: /tmp/x
  - name: ping when asked
    net.test_ping: {}
    when: vars.ping == true
  - name: a command
    ssh_exec:
      command: "true"
  - name: a program's method
    acme.widget: {}
`
	if err := os.WriteFile(filepath.Join(dir, "mixed.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	evt := requestJob(t, t.Context(), store, "mixed", "web")
	if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	payloads := publishedPayloads(t, bus)
	if len(payloads) != 1 {
		t.Fatalf("%d dispatches, want 1", len(payloads))
	}
	want := map[string]wire.CollectionIdentity{
		"file": {Group: "file", Version: buildinfo.Version(), Manifests: collection.ManifestSetDigest(map[string]collection.Manifest{"file.test_remove": manifests["file.test_remove"]})},
		"net":  {Group: "net", Version: buildinfo.Version(), Manifests: collection.ManifestSetDigest(map[string]collection.Manifest{"net.test_ping": manifests["net.test_ping"]})},
	}
	if got := payloads[0].Collections; !reflect.DeepEqual(got, want) {
		t.Errorf("the dispatch names %+v, want %+v", got, want)
	}
}

// TestWorker_ARollbackNamesTheGroupsOfItsSteps: a rollback dispatch runs its
// undo steps in place of the runbook's tasks, so it names the groups of the
// steps' methods, from the catalog.
func TestWorker_ARollbackNamesTheGroupsOfItsSteps(t *testing.T) {
	manifests := installGroupCatalog(t)
	plan := rollbackPlan()
	for d := range plan.Devices {
		for s := range plan.Devices[d].Steps {
			plan.Devices[d].Steps[s].Method = "file.test_remove"
		}
	}
	f, _ := requestRollbackOf(t, nil, plan)
	payloads := publishedPayloads(t, f.bus)
	if len(payloads) == 0 {
		t.Fatal("no rollback was dispatched")
	}
	want := map[string]wire.CollectionIdentity{"file": {Group: "file", Version: buildinfo.Version(),
		Manifests: collection.ManifestSetDigest(map[string]collection.Manifest{"file.test_remove": manifests["file.test_remove"]})}}
	for _, p := range payloads {
		if p.Rollback == nil || !reflect.DeepEqual(p.Collections, want) {
			t.Errorf("device %s: rollback %v, collections %+v; want its steps' group %+v", p.DeviceID, p.Rollback != nil, p.Collections, want)
		}
	}
}
