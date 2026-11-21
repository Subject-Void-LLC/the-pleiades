// Internal tests of the overlay's bound: a run keeps, of what its tasks
// emitted, only the keys its conditions read.
package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestOverlay_KeepsOnlyTheKeysTheRunsConditionsRead: a gather emitting a
// thousand packages and a kernel, in a run whose one condition reads the
// kernel, overlays the kernel alone; a run reading no fact overlays nothing.
func TestOverlay_KeepsOnlyTheKeysTheRunsConditionsRead(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "tfz.gather",
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a fixture"},
			Facts: []collection.FactSpec{
				{Key: "tfz.kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"},
				{Key: "tfz.pkg.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a package"},
			}}}); err != nil {
		t.Fatal(err)
	}
	stats := map[string]any{"tfz.kernel": "6.1"}
	keys := []string{"tfz.kernel"}
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("tfz.pkg.p%d", i)
		stats[key] = map[string]any{"version": "1"}
		keys = append(keys, key)
	}
	result := NodeResult{NodeID: "tasks[0]", Device: "dev-1", Stats: stats, factKeys: keys, factMethod: "tfz.gather"}
	for name, tc := range map[string]struct {
		runbook string
		want    []string
	}{
		"a run reading the kernel": {`{"id": "o", "tasks": [{"name": "g", "tfz.gather": {}}, {"name": "r", "noop": {}, "when": "facts[\"tfz.kernel\"] == \"6.1\""}]}`, []string{"tfz.kernel"}},
		"a run reading no fact":    {`{"id": "o", "tasks": [{"name": "g", "tfz.gather": {}}]}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			dag := buildInternalDAG(t, tc.runbook)
			r := &run{x: NewExecutor(nil, nil, nil, nil, nil, 0), dag: dag, secrets: newStringSet(), knowledge: newFactKnowledge(dag)}
			r.knowledge.observe(r, []NodeResult{result})
			var kept []string
			if d := r.knowledge.devices["dev-1"]; d != nil {
				for k := range d.real.values {
					kept = append(kept, k)
				}
			}
			if fmt.Sprint(kept) != fmt.Sprint(tc.want) {
				t.Errorf("kept %d keys %v, want %v", len(kept), kept, tc.want)
			}
		})
	}
}
