// Tests that a job admits each device against the transports and
// capabilities of every method its runbook names (Phase 117b).
package dispatch_test

import (
	"encoding/json"
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

// TestWorker_SkipsADeviceAMethodCannotRunOn: a runbook calls an SSH-only
// method that requires SystemdCapable. A Linux server with both is
// dispatched; a WinRM-only Windows server and an SSH switch without systemd
// are skipped at launch, each with the method's own refusal as the reason.
// Before Phase 117b admission asked only engine.ActionCapability, so both
// were dispatched and failed on the Runner.
func TestWorker_SkipsADeviceAMethodCannotRunOn(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	collection.MustRegister(collection.Descriptor{Name: "test.restart_unit", Manifest: collection.Manifest{
		Status:               collection.StatusDeclared,
		SupportedTransports:  []string{capability.TransportSSH},
		RequiredCapabilities: []capability.Name{capability.NameSystemd},
	}})

	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	stub := func(id, name string, caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubID: pkginventory.DeviceID(id), StubName: name, StubState: pkginventory.StateActive,
			Caps: caps, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		stub("dev-linux", "web1", capability.NameSSHTransport, capability.NameSystemd),
		stub("dev-win", "win1", capability.NameWinRM, capability.NameWindowsService),
		stub("dev-switch", "sw1", capability.NameSSHTransport, capability.NameCiscoIOS),
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "restart.yaml"), []byte("id: restart\ntasks:\n  - name: restart\n    test.restart_unit:\n      unit: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, nil)

	evt := requestJob(t, ctx, store, "restart", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		outcome dispatch.Outcome
		reason  string
	}{
		"web1": {dispatch.OutcomeDispatched, ""},
		"win1": {dispatch.OutcomeSkipped, `requires capability SystemdCapable, which device "win1" does not have`},
		"sw1":  {dispatch.OutcomeSkipped, `requires capability SystemdCapable, which device "sw1" does not have`},
	}
	if len(tasks) != len(want) {
		t.Fatalf("tasks = %+v, want one per device", tasks)
	}
	for _, task := range tasks {
		w := want[task.DeviceName]
		if task.Outcome != w.outcome || !strings.Contains(task.Reason, w.reason) {
			t.Errorf("%s: outcome %q, reason %q; want %q, a reason containing %q", task.DeviceName, task.Outcome, task.Reason, w.outcome, w.reason)
		}
	}
	if bus.count() != 1 {
		t.Errorf("%d dispatches published, want 1, for web1", bus.count())
	}
}

// TestWorker_SkipsADeviceAMethodCannotReach: a device carrying every
// capability a method requires but reaching none of its transports (a
// Windows server, WinRM only, under an SSH method that requires only
// ServiceManagerCapable) is skipped, naming the transports on both sides.
func TestWorker_SkipsADeviceAMethodCannotReach(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	collection.MustRegister(collection.Descriptor{Name: "test.restart_service", Manifest: collection.Manifest{
		Status:               collection.StatusDeclared,
		SupportedTransports:  []string{capability.TransportSSH},
		RequiredCapabilities: []capability.Name{capability.NameServiceManager},
	}})

	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	win := &inventorytest.Stub{StubID: "dev-win", StubName: "win1", StubState: pkginventory.StateActive,
		Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsService}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.2"}}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{win}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "restart.yaml"), []byte("id: restart\ntasks:\n  - name: restart\n    test.restart_service:\n      name: spooler\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, nil)

	evt := requestJob(t, ctx, store, "restart", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Outcome != dispatch.OutcomeSkipped || !strings.Contains(tasks[0].Reason, `reaches its device over ssh, and device "win1" reaches only winrm`) {
		t.Fatalf("tasks = %+v, want win1 skipped for the transport", tasks)
	}
	if bus.count() != 0 {
		t.Errorf("%d dispatches published, want none", bus.count())
	}
}

// TestWorker_AdmitsFromTheInstalledCatalogInABinaryWithNoRegistry: the
// shipped Controller registers no Collection, so its admission must read
// the module catalog it installs (FAILURE_PATTERNS 841). The method here is
// in the catalog only; a device lacking its capability is still skipped,
// which with the registry alone it was not.
func TestWorker_AdmitsFromTheInstalledCatalogInABinaryWithNoRegistry(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if _, ok := collection.Lookup("test.catalog_unit"); ok {
		t.Fatal("the method is registered, so this would not test the catalog")
	}
	catalog, err := json.Marshal(map[string]collection.Manifest{"test.catalog_unit": {
		Status:               collection.StatusImplemented,
		SupportedTransports:  []string{capability.TransportSSH},
		RequiredCapabilities: []capability.Name{capability.NameSystemd},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		&inventorytest.Stub{StubID: "dev-linux", StubName: "web1", StubState: pkginventory.StateActive,
			Caps: []capability.Name{capability.NameSSHTransport, capability.NameSystemd}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}},
		&inventorytest.Stub{StubID: "dev-switch", StubName: "sw1", StubState: pkginventory.StateActive,
			Caps: []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.2"}},
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "restart.yaml"), []byte("id: restart\ntasks:\n  - name: restart\n    test.catalog_unit:\n      unit: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	evt := requestJob(t, ctx, store, "restart", "routers")
	if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		switch task.DeviceName {
		case "web1":
			if task.Outcome != dispatch.OutcomeDispatched {
				t.Errorf("web1: %q %q, want dispatched", task.Outcome, task.Reason)
			}
		case "sw1":
			if task.Outcome != dispatch.OutcomeSkipped || !strings.Contains(task.Reason, "SystemdCapable") {
				t.Errorf("sw1: %q %q, want skipped for lacking SystemdCapable", task.Outcome, task.Reason)
			}
		}
	}
	if len(tasks) != 2 || bus.count() != 1 {
		t.Errorf("%d tasks, %d dispatches; want 2 and 1", len(tasks), bus.count())
	}
}

// TestWorker_RefusesARouterOfNeitherKindFromTheShippedCatalog: wait.routes
// reads an FRR router or a Cisco IOS one (collection.Manifest.Alternatives).
// A Controller admits from the module catalog it embeds, exactly as shipped
// (wellknown.ModuleCatalog), so this installs that one: the FRR router and
// the IOS router behind a console server are dispatched, and a Linux server
// that runs commands over SSH is skipped naming both kinds. Before the
// alternatives the method declared no capability, so the server was
// dispatched and failed on the Runner.
func TestWorker_RefusesARouterOfNeitherKindFromTheShippedCatalog(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	stub := func(id, name string, caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubID: pkginventory.DeviceID(id), StubName: name, StubState: pkginventory.StateActive,
			Caps: caps, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		stub("dev-frr", "r1", capability.NameFRR, capability.NameCommandExec, capability.NameSSHTransport),
		stub("dev-ios", "r2", capability.NameCiscoIOS, capability.NameConsoleServed),
		stub("dev-linux", "web1", capability.NameLinux, capability.NameCommandExec, capability.NameSSHTransport),
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settle.yaml"), []byte("id: settle\ntasks:\n  - name: routes settle\n    wait.routes:\n      prefixes: [10.1.0.0/24]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	evt := requestJob(t, ctx, store, "settle", "routers")
	if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		switch task.DeviceName {
		case "r1", "r2":
			if task.Outcome != dispatch.OutcomeDispatched {
				t.Errorf("%s: %q %q, want dispatched", task.DeviceName, task.Outcome, task.Reason)
			}
		case "web1":
			if task.Outcome != dispatch.OutcomeSkipped || !strings.Contains(task.Reason, `device "web1" is none of them`) ||
				!strings.Contains(task.Reason, "frr (FRRCapable and CommandExecCapable, over ssh); ios (CiscoIOSCapable") {
				t.Errorf("web1: %q %q, want skipped naming both kinds", task.Outcome, task.Reason)
			}
		}
	}
	if len(tasks) != 3 || bus.count() != 2 {
		t.Errorf("%d tasks, %d dispatches; want 3 and 2", len(tasks), bus.count())
	}
}
