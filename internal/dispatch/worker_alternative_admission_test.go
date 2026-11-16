// Tests that a Controller's admission holds a task to the alternative it
// names (collection.Manifest.AlternativeParam), from the module catalog it
// ships, as validate does: net.yang.schema with via: restconf skips a device
// served only by NETCONF at launch.
package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestWorker_HoldsATaskToTheAlternativeItNames launches one runbook per via
// against a device served by NETCONF alone, one by RESTCONF alone and one by
// both. A task naming RESTCONF skips the NETCONF-only device naming the one
// kind, and one naming NETCONF skips the RESTCONF-only device; auto, an absent
// via and a templated one admit every device either kind fits, a templated
// value being the Runner's to hold once rendered.
func TestWorker_HoldsATaskToTheAlternativeItNames(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
		t.Fatal(err)
	}
	stub := func(id, name string, caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubID: pkginventory.DeviceID(id), StubName: name, StubState: pkginventory.StateActive,
			Caps: caps, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		stub("dev-nc", "nc", capability.NameNetconf, capability.NameSSHTransport),
		stub("dev-rc", "rc", capability.NameRestconf),
		stub("dev-both", "both", capability.NameNetconf, capability.NameSSHTransport, capability.NameRestconf),
	}}
	for via, skipped := range map[string]map[string]string{
		"restconf":         {"nc": `reaches its device over restconf, and device "nc" reaches only netconf and ssh`},
		"netconf":          {"rc": `reaches its device over netconf, and device "rc" reaches only restconf`},
		"auto":             {},
		"":                 {},
		"{{ vars.proto }}": {},
	} {
		dir := t.TempDir()
		task := "id: schema\ntasks:\n  - name: read the schema\n    net.yang.schema:\n      modules: [ietf-interfaces]\n"
		if via != "" {
			task += "      via: '" + via + "'\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), []byte(task), 0o600); err != nil {
			t.Fatal(err)
		}
		runbooks, err := runbook.NewDirSource(dir)
		if err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		evt := requestJob(t, ctx, store, "schema", "routers")
		if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
			t.Fatal(err)
		}
		_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			reason, wantSkip := skipped[task.DeviceName]
			switch {
			case wantSkip && (task.Outcome != dispatch.OutcomeSkipped || !strings.Contains(task.Reason, reason)):
				t.Errorf("via %q, %s: %q %q, want skipped naming %s", via, task.DeviceName, task.Outcome, task.Reason, reason)
			case !wantSkip && task.Outcome != dispatch.OutcomeDispatched:
				t.Errorf("via %q, %s: %q %q, want dispatched", via, task.DeviceName, task.Outcome, task.Reason)
			}
		}
		if len(tasks) != 3 || bus.count() != 3-len(skipped) {
			t.Errorf("via %q: %d tasks, %d dispatches; want 3 and %d", via, len(tasks), bus.count(), 3-len(skipped))
		}
	}
}
