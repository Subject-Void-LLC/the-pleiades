// Package engine_test: tests that an external program's check never reaches a
// simulate-locked device.
package engine_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestCheckMode_AnExternalCheckSkipsASimulateLockedDevice proves a check
// provided by an external program is never run against a simulate-locked
// device and is reported unchecked there, naming the lock and the
// program, while the same method still checks an active device (the
// control), and the same method with no provider, a built-in, still
// checks the locked one.
func TestCheckMode_AnExternalCheckSkipsASimulateLockedDevice(t *testing.T) {
	locked := &inventorytest.Stub{StubID: "new-device", StubName: "new-device", StubState: inventory.StateSimulateLocked}
	active := &inventorytest.Stub{StubID: "old-device", StubName: "old-device", StubState: inventory.StateActive}
	resolver := mapResolver{"fleet": {locked, active}}

	for _, tc := range []struct {
		name     string
		provider *collection.Provider
		checks   int32
	}{
		{name: "external", provider: &collection.Provider{Program: "/opt/collections/thirdparty", Digest: "sha256:00"}, checks: 1},
		{name: "builtin", provider: nil, checks: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			var checks atomic.Int32
			check := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				checks.Add(1)
				return collection.Result{Changed: true}, nil
			}
			name := "enginelock." + tc.name
			if err := collection.Register(collection.Descriptor{
				Name: name,
				Manifest: collection.Manifest{
					Status:        collection.StatusImplemented,
					Reversibility: collection.Reversibility{Notes: "a test fixture"},
					SupportsCheck: true,
				},
				Invoke: check, Check: check, Provider: tc.provider,
			}); err != nil {
				t.Fatal(err)
			}
			dag := buildDAG(t, `{"id": "lock", "tasks": [{"name": "t", "fqcn": "`+name+`", "params": {"target": "fleet"}}]}`)
			// External checks allowed, as for a launcher who may run the
			// program for real: what is under test is the lock.
			result, err := newCheckExecutor(resolver, engine.WithMode(collection.ModeCheck), engine.WithExternalChecks(true)).Run(context.Background(), dag)
			if err != nil {
				t.Fatal(err)
			}
			if got := checks.Load(); got != tc.checks {
				t.Errorf("Check ran %d time(s), want %d", got, tc.checks)
			}
			for _, n := range result.Nodes {
				if n.Provider != tc.provider && (n.Provider == nil || tc.provider == nil || *n.Provider != *tc.provider) {
					t.Errorf("device %s: Provider = %+v, want %+v on every result of this method", n.Device, n.Provider, tc.provider)
				}
				lockedAndExternal := n.Device == "new-device" && tc.provider != nil
				if n.Unchecked != lockedAndExternal {
					t.Errorf("device %s: Unchecked = %v, want %v (%+v)", n.Device, n.Unchecked, lockedAndExternal, n)
				}
				if lockedAndExternal && (!strings.Contains(n.SkipReason, "simulate-locked") || !strings.Contains(n.SkipReason, tc.provider.Program)) {
					t.Errorf("the reason should name the lock and the program, got %q", n.SkipReason)
				}
			}
		})
	}
}

// TestJournal_AnExternalMethodsEntryNamesItsProgram proves the journal
// entry of a method an external program provides records that program and
// the digest it ran as, and a built-in method's entry records neither, so
// a reader of the journal alone can tell third-party work from Pleiades's
// own.
func TestJournal_AnExternalMethodsEntryNamesItsProgram(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}
	provider := &collection.Provider{Program: "/opt/collections/acme", Digest: "sha256:abc"}
	for name, p := range map[string]*collection.Provider{"enginejournal.external": provider, "enginejournal.builtin": nil} {
		if err := collection.Register(collection.Descriptor{
			Name:     name + ".run",
			Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}},
			Invoke:   run, Provider: p,
		}); err != nil {
			t.Fatal(err)
		}
	}
	dag := buildDAG(t, `{"id": "journal-provider", "tasks": [
		{"name": "ext", "fqcn": "enginejournal.external.run"},
		{"name": "own", "fqcn": "enginejournal.builtin.run"}]}`)
	journal := &entriesJournal{}
	if _, err := newCheckExecutor(mapResolver{}, engine.WithJournal(journal)).Run(context.Background(), dag); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range journal.entries {
		switch e.FQCN {
		case "enginejournal.external.run":
			seen++
			if e.ProviderProgram != provider.Program || e.ProviderDigest != provider.Digest {
				t.Errorf("the external method's entry names %q %q, want %q %q", e.ProviderProgram, e.ProviderDigest, provider.Program, provider.Digest)
			}
		case "enginejournal.builtin.run":
			seen++
			if e.ProviderProgram != "" || e.ProviderDigest != "" {
				t.Errorf("the built-in method's entry names a provider: %q %q", e.ProviderProgram, e.ProviderDigest)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d of the 2 entries: %+v", seen, journal.entries)
	}
}

// TestCheckMode_ExternalChecksAreOffByDefault covers WithExternalChecks:
// with it unset, the default, an external program's check is reported
// unchecked on an active device too, naming the program and the right it
// needs, and its Check never runs; a built-in's still checks. A Runner
// leaves it unset for a check launched without runbook:execute, and a
// path that forgets to set it fails closed.
func TestCheckMode_ExternalChecksAreOffByDefault(t *testing.T) {
	active := &inventorytest.Stub{StubID: "old-device", StubName: "old-device", StubState: inventory.StateActive}
	for _, tc := range []struct {
		name     string
		provider *collection.Provider
		allow    []engine.ExecutorOption
		checked  bool
	}{
		{"external, not allowed", &collection.Provider{Program: "/opt/collections/thirdparty", Digest: "sha256:00"}, nil, false},
		{"external, allowed", &collection.Provider{Program: "/opt/collections/thirdparty", Digest: "sha256:00"}, []engine.ExecutorOption{engine.WithExternalChecks(true)}, true},
		{"built in, not allowed", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			var checks atomic.Int32
			check := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
				checks.Add(1)
				return collection.Result{Changed: true}, nil
			}
			if err := collection.Register(collection.Descriptor{
				Name:     "enginedefault.method",
				Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, SupportsCheck: true},
				Invoke:   check, Check: check, Provider: tc.provider,
			}); err != nil {
				t.Fatal(err)
			}
			dag := buildDAG(t, `{"id": "default", "tasks": [{"name": "t", "fqcn": "enginedefault.method", "params": {"target": "fleet"}}]}`)
			opts := append([]engine.ExecutorOption{engine.WithMode(collection.ModeCheck)}, tc.allow...)
			result, err := newCheckExecutor(mapResolver{"fleet": {active}}, opts...).Run(context.Background(), dag)
			if err != nil {
				t.Fatal(err)
			}
			node := result.Nodes[0]
			if node.Unchecked == tc.checked || (checks.Load() == 1) != tc.checked {
				t.Fatalf("checked = %v (Check ran %d time(s)), want %v: %+v", !node.Unchecked, checks.Load(), tc.checked, node)
			}
			if !tc.checked && !strings.Contains(node.SkipReason, "runbook:execute") {
				t.Errorf("the reason does not name the right it needs: %q", node.SkipReason)
			}
		})
	}
}
