// Package native: that a check runs an external program's Check only when
// the payload says its launcher may run the job for real.
package native

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestAdapter_Execute_ExternalChecksFollowThePayload runs a check through
// the adapter's real Execute (a compiled runbook, the engine, the route an
// external method takes) with an external method that supports check: the
// payload's ExternalChecks decides whether its Check runs, and without it
// the task is counted unchecked in the Outcome the Controller records.
func TestAdapter_Execute_ExternalChecksFollowThePayload(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	var checks atomic.Int32
	check := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		checks.Add(1)
		return collection.Result{Changed: true}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name:     "nativeexternal.check.run",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, SupportsCheck: true},
		Invoke:   check, Check: check,
		Provider: &collection.Provider{Program: "/opt/collections/checker", Digest: "sha256:00"},
	}); err != nil {
		t.Fatal(err)
	}
	runbooks := writeRunbook(t, "ext", "id: ext\ntasks:\n  - name: step\n    fqcn: nativeexternal.check.run\n")
	adapter, err := NewAdapter(&mockBus{}, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, allowed := range []bool{false, true} {
		before := checks.Load()
		outcome, err := adapter.Execute(context.Background(), wire.DispatchPayload{
			JobID: "job-1", RunbookID: "ext", Mode: "check", ExternalChecks: allowed,
			DeviceName: "router1", DeviceHost: "10.0.0.1",
		})
		if err != nil {
			t.Fatalf("ExternalChecks %v: Execute: %v", allowed, err)
		}
		ran := checks.Load() - before
		switch {
		case allowed && (ran != 1 || outcome.Unchecked != 0):
			t.Errorf("allowed: Check ran %d time(s), %d unchecked; want it run and nothing unchecked", ran, outcome.Unchecked)
		case !allowed && (ran != 0 || outcome.Unchecked != 1):
			t.Errorf("not allowed: Check ran %d time(s), %d unchecked; want it not run and counted unchecked", ran, outcome.Unchecked)
		}
	}
}
