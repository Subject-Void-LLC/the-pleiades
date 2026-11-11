// A cross-side test of the pinned graph: the payload the real dispatch worker
// builds, over a real runbook directory and a real job store, handed to the
// real native adapter's Execute, which runs it over its own copy of the
// runbook. In process, no containers: the Controller's pin and the Runner's
// check meet through the dispatch payload exactly as they do over NATS.
package dispatch_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/native"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// crossRunbook runs one task of a method that counts its runs, with an
// authored rollback: list.
const crossRunbook = "id: cross\ntasks:\n  - name: count it\n    acme.test_cross: {}\n    rollback:\n      - name: undo it\n        acme.test_cross: {}\n"

// writeCross writes body as runbook cross in dir, a minute past the file it
// replaces, and returns the source over dir.
func writeCross(t *testing.T, dir, body string) runbook.Source {
	t.Helper()
	path := filepath.Join(dir, "cross.yaml")
	later := time.Now()
	if info, err := os.Stat(path); err == nil {
		later = info.ModTime().Add(time.Minute)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestPinnedGraph_TheWorkersPayloadRunsOnlyOnTheSameGraph: the payload the
// worker publishes for a job runs on a Runner whose copy of the runbook is
// the one the job pinned, and a Runner whose copy differs only in a
// rollback: list (so in no version) runs nothing and fails the device.
func TestPinnedGraph_TheWorkersPayloadRunsOnlyOnTheSameGraph(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	var ran atomic.Int32
	if err := collection.Register(collection.Descriptor{Name: "acme.test_cross",
		Invoke: func(context.Context, sdk.RunbookContext, pkginventory.InventoryItem, map[string]any) (collection.Result, error) {
			ran.Add(1)
			return collection.Result{}, nil
		},
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "reads"}},
		// A program's method, which the adapter runs in this process.
		Provider: &collection.Provider{Program: "/opt/collections/acme", Digest: "sha256:00"}}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	controllerSrc := writeCross(t, t.TempDir(), crossRunbook)
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{&inventorytest.Stub{StubID: "dev-1", StubName: "web1",
		StubState: pkginventory.StateActive, Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}}}
	worker := dispatch.NewWorker(store, repo, controllerSrc, bus, nil)
	evt := requestJob(t, ctx, store, "cross", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatal(err)
	}
	if bus.count() != 1 {
		t.Fatalf("the worker dispatched %d devices, want one", bus.count())
	}
	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.GraphScheme == "" || payload.GraphDigest == "" || payload.PayloadVersion != wire.PinnedGraphPayloadVersion {
		t.Fatalf("the worker's payload names graph %q@%q at version %d, want the job's pin", payload.GraphScheme, payload.GraphDigest, payload.PayloadVersion)
	}
	if err := payload.CheckVersion(); err != nil {
		t.Fatalf("this build's Runner refuses this build's payload: %v", err)
	}

	execute := func(src runbook.Source) error {
		t.Helper()
		adapter, err := native.NewAdapter(event.NewInProcessBus(), src, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.Execute(ctx, payload)
		return err
	}
	// The Runner's copy is the runbook the job pinned.
	runnerDir := t.TempDir()
	if err := execute(writeCross(t, runnerDir, crossRunbook)); err != nil || ran.Load() != 1 {
		t.Fatalf("the Runner holding the pinned graph: %v, ran %d times; want the task run once", err, ran.Load())
	}
	// Only the Runner's copy is edited, and only in a rollback: list.
	edited := strings.Replace(crossRunbook, "      - name: undo it\n", "      - name: undo it\n        acme.test_cross: {}\n      - name: and again\n", 1)
	err := execute(writeCross(t, runnerDir, edited))
	if err == nil || !strings.Contains(err.Error(), "is not the graph job") {
		t.Errorf("a Runner whose copy differs in a rollback: list: %v, want the refusal", err)
	}
	if ran.Load() != 1 {
		t.Errorf("the task ran %d times in all, want it not run on the edited copy", ran.Load())
	}
}
