// Package engine_test: tests of a check's "cannot check this call" answer.
package engine_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerPartlyCheckable registers a method shaped like exec.command with
// creates: its Check can answer only when the creates parameter is set,
// and says it cannot check the call otherwise, wrapped as a method might
// wrap it. Its Invoke returns the same answer when told to, which in a
// real run is an ordinary failure.
func registerPartlyCheckable(t *testing.T) (name string, checks *atomic.Int32) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	checks = new(atomic.Int32)
	name = "enginecheck.partly"
	err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture"},
			SupportsCheck: true,
		},
		Invoke: func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			if params["refuse"] == true {
				return collection.Result{}, collection.CannotCheck("an Invoke has no business saying this")
			}
			return collection.Result{Changed: true}, nil
		},
		Check: func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			if params["creates"] == nil {
				return collection.Result{}, fmt.Errorf("partly: %w", collection.CannotCheck("without creates, whether the command runs cannot be known without running it"))
			}
			checks.Add(1)
			return collection.Result{Changed: true}, rc.SetStat("would_run", true)
		},
	})
	if err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name, checks
}

// TestCannotCheck_IsUncheckedForThatCallOnly covers the "cannot check this
// call" answer: one method, checked for the parameters it can answer,
// reported unchecked (not failed) with its own reason for the ones it
// cannot, and a later task reading the unchecked one's register is left
// undecided rather than failed. The same answer from Invoke in a real run
// is a failure, never a skip.
func TestCannotCheck_IsUncheckedForThatCallOnly(t *testing.T) {
	name, checks := registerPartlyCheckable(t)
	dag := buildDAG(t, `{"id": "cannot-check", "tasks": [
		{"name": "with-creates", "fqcn": "`+name+`", "register": "a", "params": {"creates": "/tmp/x"}},
		{"name": "without", "fqcn": "`+name+`", "register": "b"},
		{"name": "reads-it", "fqcn": "`+name+`", "params": {"creates": "/tmp/y"}, "when_cel": "stat.b[\"\"].would_run"}
	]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("a call the method cannot check failed the check: %+v", result.Nodes)
	}
	if n := nodeByID(t, result, "tasks[0]"); n.Unchecked || n.Skipped || !n.Changed {
		t.Errorf("the call the method can check = %+v, want it checked and predicting a change", n)
	}
	without := nodeByID(t, result, "tasks[1]")
	if !without.Unchecked || !strings.Contains(without.SkipReason, "without creates") || !strings.Contains(without.SkipReason, name) {
		t.Errorf("the call the method cannot check = %+v, want it unchecked with the method's reason", without)
	}
	if n := nodeByID(t, result, "tasks[2]"); !n.Unchecked {
		t.Errorf("a task reading the unchecked call's result = %+v, want it undecided", n)
	}
	if checks.Load() != 1 {
		t.Errorf("Check answered %d call(s), want 1", checks.Load())
	}

	refusing := buildDAG(t, `{"id": "cannot-check-real", "tasks": [{"name": "t", "fqcn": "`+name+`", "params": {"refuse": true}}]}`)
	result, err = newCheckExecutor(mapResolver{}).Run(context.Background(), refusing)
	if err != nil {
		t.Fatalf("execute Run: %v", err)
	}
	if n := nodeByID(t, result, "tasks[0]"); n.Err == nil || n.Unchecked {
		t.Errorf("a real run whose method answered cannot-check = %+v, want it failed", n)
	}
}
