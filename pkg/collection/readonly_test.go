// Tests of Descriptor.ReadOnlyCall and Descriptor.ReadsOnly: a method only
// some of whose calls read says which, and a read-only method may not.
package collection_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestRegister_ReadOnlyCallIsRefusedOnAReadOnlyMethod: a method whose
// manifest declares every call read-only may not carry a ReadOnlyCall, and
// a method that is not read-only may.
func TestRegister_ReadOnlyCallIsRefusedOnAReadOnlyMethod(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	reads := func(map[string]any) bool { return true }
	whole := collection.Descriptor{Name: "readonlycall.whole", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "Reading is not changing."},
	}, Invoke: run, ReadOnlyCall: reads}
	if err := collection.Register(whole); err == nil || !strings.Contains(err.Error(), "ReadOnlyCall") {
		t.Errorf("Register = %v, want a ReadOnlyCall on a read-only method refused", err)
	}
	some := collection.Descriptor{Name: "readonlycall.some", Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture"},
	}, Invoke: run, ReadOnlyCall: reads}
	if err := collection.Register(some); err != nil {
		t.Errorf("Register = %v, want a ReadOnlyCall on a method that is not read-only accepted", err)
	}
}

// TestReadsOnly answers from the manifest first, then from ReadOnlyCall for
// the call's own parameters, and says no for a method with neither.
func TestReadsOnly(t *testing.T) {
	show := func(params map[string]any) bool { return params["command"] == "show version" }
	for _, tc := range []struct {
		name   string
		desc   collection.Descriptor
		params map[string]any
		want   bool
	}{
		{"a read-only manifest", collection.Descriptor{Manifest: collection.Manifest{Reversibility: collection.Reversibility{ReadOnly: true}}}, nil, true},
		{"a call ReadOnlyCall says reads", collection.Descriptor{ReadOnlyCall: show}, map[string]any{"command": "show version"}, true},
		{"a call ReadOnlyCall says changes", collection.Descriptor{ReadOnlyCall: show}, map[string]any{"command": "reload"}, false},
		{"neither", collection.Descriptor{}, map[string]any{"command": "show version"}, false},
	} {
		if got := tc.desc.ReadsOnly(tc.params); got != tc.want {
			t.Errorf("%s: ReadsOnly = %v, want %v", tc.name, got, tc.want)
		}
	}
}
