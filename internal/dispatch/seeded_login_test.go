// Tests that fan-out refuses a runbook that would seed a login before it
// publishes a single dispatch (decided 2026-11-11).
package dispatch_test

import (
	"errors"
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

// TestWorker_RefusesASeededLoginBeforeAnyDispatch: the shipped Controller
// knows a seeding method only from the module catalog it installs. A job
// whose runbook seeds a device's login into the machine it creates is failed
// at fan-out, its reason naming the task, the method and the device, and no
// dispatch is published for either device. The control, the same fleet under
// a runbook that seeds nothing, publishes one dispatch per device.
func TestWorker_RefusesASeededLoginBeforeAnyDispatch(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte(`{
"seedtest.clone":{"status":"implemented","seedsLogin":"login","doc":{"params":[{"name":"login","type":"string","required":true}]}},
"seedtest.list":{"status":"implemented"}}`)); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	stub := func(id, name string) pkginventory.InventoryItem {
		return &inventorytest.Stub{StubID: pkginventory.DeviceID(id), StubName: name, StubState: pkginventory.StateActive,
			Caps: []capability.Name{capability.NameSSHTransport}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{stub("dev-1", "host1"), stub("dev-2", "host2")}}
	dir := t.TempDir()
	files := map[string]string{
		"seeds.yaml": "id: seeds\ntasks:\n  - name: list\n    seedtest.list: {}\n  - name: clone\n    seedtest.clone:\n      login: lab-web\n",
		"plain.yaml": "id: plain\ntasks:\n  - name: list\n    seedtest.list: {}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := dispatch.NewWorker(store, repo, runbooks, bus, nil)

	evt := requestJob(t, ctx, store, "seeds", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	job, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "failed" || len(tasks) != 0 {
		t.Fatalf("job %q with %d devices, want failed with none", job.State, len(tasks))
	}
	for _, want := range []string{`task "tasks[1]" runs seedtest.clone`, `device "lab-web"`, "pleiades CLI"} {
		if !strings.Contains(job.FailureReason, want) {
			t.Errorf("failure reason %q, want it to contain %q", job.FailureReason, want)
		}
	}
	if bus.count() != 0 {
		t.Errorf("%d dispatches published for a seeding runbook, want none", bus.count())
	}

	control := requestJob(t, ctx, store, "plain", "routers")
	if err := worker.HandleJobRequested(control); err != nil {
		t.Fatal(err)
	}
	if bus.count() != 2 {
		t.Errorf("control: %d dispatches published, want 2", bus.count())
	}
}

// TestRefuseSeededLogins_WrapsTheSentinel: the refusal is ErrSeededLogin,
// so the API maps it to 422 rather than an internal error, and a runbook
// that seeds nothing, or none at all, is not refused.
func TestRefuseSeededLogins_WrapsTheSentinel(t *testing.T) {
	err := dispatch.RefuseSeededLogins(&runbook.Runbook{ID: "x", SeededLogins: []string{"task a", "task b"}})
	if !errors.Is(err, dispatch.ErrSeededLogin) || !strings.Contains(err.Error(), "task a; task b") {
		t.Errorf("RefuseSeededLogins = %v, want ErrSeededLogin naming both tasks", err)
	}
	if err := dispatch.RefuseSeededLogins(&runbook.Runbook{ID: "x"}); err != nil {
		t.Errorf("a runbook seeding nothing was refused: %v", err)
	}
	if err := dispatch.RefuseSeededLogins(nil); err != nil {
		t.Errorf("a nil runbook was refused: %v", err)
	}
}
