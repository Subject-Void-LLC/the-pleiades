// Tests that a device reached through a console server is skipped at
// admission naming the server, since a Runner cannot resolve one (Phase 127d).
package dispatch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// consoleNode is a device reached through the console server cml, as a CML
// node imported with --via console is: no address of its own.
type consoleNode struct{ *inventorytest.Stub }

// ConsoleServer implements capability.ConsoleServedCapable.
func (consoleNode) ConsoleServer() string { return "cml" }

// ConsoleLine implements capability.ConsoleServedCapable.
func (consoleNode) ConsoleLine() string { return "00000000-0000-4000-8000-000000000001" }

// TestWorker_SkipsAConsoleServedDeviceNamingItsServer: a console-served node
// is skipped with a reason naming its console server and the CLI, not as a
// device with no host, and nothing is dispatched for it; an addressed device
// in the same job is dispatched.
func TestWorker_SkipsAConsoleServedDeviceNamingItsServer(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		consoleNode{&inventorytest.Stub{StubID: "dev-r1", StubName: "r1", StubState: pkginventory.StateActive,
			Caps: []capability.Name{capability.NameConsoleServed}}},
		&inventorytest.Stub{StubID: "dev-r2", StubName: "r2", StubState: pkginventory.StateActive,
			Caps: []capability.Name{capability.NameSSHTransport}, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.2"}},
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.yaml"), []byte("id: plain\ntasks:\n  - name: nothing\n    noop: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	evt := requestJob(t, ctx, store, "plain", "routers")
	if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	_, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		switch task.DeviceName {
		case "r1":
			if task.Outcome != dispatch.OutcomeSkipped || !strings.Contains(task.Reason, `console server "cml"`) || !strings.Contains(task.Reason, "pleiades CLI") {
				t.Errorf("r1: %q %q, want skipped naming the console server and the CLI", task.Outcome, task.Reason)
			}
		case "r2":
			if task.Outcome != dispatch.OutcomeDispatched {
				t.Errorf("r2: %q %q, want dispatched", task.Outcome, task.Reason)
			}
		}
	}
	if len(tasks) != 2 || bus.count() != 1 {
		t.Errorf("%d tasks, %d dispatches; want 2 and 1", len(tasks), bus.count())
	}
}
