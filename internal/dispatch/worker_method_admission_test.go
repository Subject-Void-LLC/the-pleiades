// Tests that a job admits each device against the transports and
// capabilities of every method its runbook names (Phase 117b).
package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
