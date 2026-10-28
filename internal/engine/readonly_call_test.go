// Tests that the engine holds a call a method's ReadOnlyCall declared
// read-only to that claim, as it holds a read-only method.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestCollectionActionExecutor_HoldsAReadOnlyCallToItsClaim: a method that
// is not read-only, whose ReadOnlyCall declares a call with read: true
// read-only, fails that call when it reports a change, and is allowed to
// report one for any other call.
func TestCollectionActionExecutor_HoldsAReadOnlyCallToItsClaim(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	const name = "enginetest.readonly_call"
	changes := func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name:         name,
		Manifest:     collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "some calls read"}},
		Invoke:       changes,
		ReadOnlyCall: func(params map[string]any) bool { return params["read"] == true },
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		read bool
		want string
	}{
		{true, "reported a change"},
		{false, ""},
	} {
		task := &engine.Task{FQCN: name, Params: map[string]any{"read": tc.read}}
		_, err := newBridge(&recordingFallback{}).Execute(context.Background(), task, &inventorytest.Stub{StubName: "sw1"})
		checkReadOnlyErr(t, "in process", err, tc.want)
	}
}
