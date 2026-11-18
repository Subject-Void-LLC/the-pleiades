// Tests that a runbook is said to only read exactly when every task it can
// run is a literal call declaring that it only reads.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestOnlyReads_IsTrueOnlyForLiteralReads registers a method only some of
// whose calls read (command beginning "show"), as net.ios.command is, and
// holds OnlyReads to each shape a runbook can take.
func TestOnlyReads_IsTrueOnlyForLiteralReads(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	const name = "enginetest.exec_line"
	nothing := func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name:     name,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a show reads"}},
		Invoke:   nothing,
		ReadOnlyCall: func(params map[string]any) bool {
			command, _ := params["command"].(string)
			return len(command) >= 4 && command[:4] == "show"
		},
	}); err != nil {
		t.Fatal(err)
	}
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)
	for _, tc := range []struct {
		name, tasks string
		want        bool
	}{
		{"one show", "  - " + name + ":\n      command: show version\n", true},
		{"two shows in a block", "  - block:\n      - " + name + ":\n          command: show version\n    rescue:\n      - " + name + ":\n          command: show clock\n", true},
		{"a change", "  - " + name + ":\n      command: clear counters\n", false},
		{"a show and a change", "  - " + name + ":\n      command: show version\n  - " + name + ":\n      command: reload\n", false},
		{"a templated show", "  - " + name + ":\n      command: \"show {{ vars.what }}\"\n", false},
		{"a template deeper down", "  - " + name + ":\n      command: show version\n      extra:\n        - \"{{ vars.what }}\"\n", false},
		{"a loop", "  - " + name + ":\n      command: show version\n    loop: [1, 2]\n", false},
		{"an engine action", "  - noop:\n      note: x\n", false},
		{"an unknown method", "  - enginetest.not_registered:\n      command: show version\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag, err := builder.BuildFromYAML([]byte("id: reads\nhosts: sw1\ntasks:\n" + tc.tasks))
			if err != nil {
				t.Fatal(err)
			}
			if got := engine.OnlyReads(dag); got != tc.want {
				t.Errorf("OnlyReads = %v, want %v", got, tc.want)
			}
		})
	}
	if engine.OnlyReads(nil) {
		t.Error("OnlyReads(nil) is true")
	}
}
