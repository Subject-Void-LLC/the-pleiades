// Package validate_test: UnmatchedTargetWarnings, the warning that a task's
// hosts or target names nothing in the inventory yet.
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestUnmatchedTargetWarnings: a target naming no device or tag is warned of
// once per name, never as a finding, and a call needing no device, a
// conditional task and a target that matches are not.
func TestUnmatchedTargetWarnings(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for name, device := range map[string]collection.DeviceUse{"ut.on_device": collection.DeviceRequired, "ut.no_device": collection.DeviceNone} {
		if err := collection.Register(collection.Descriptor{Name: name, Invoke: none, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture", ReadOnly: true},
			ExecutionContext: collection.ExecutionContext{Site: collection.SiteController, Device: device},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: u
hosts: nosuch
tasks:
  - ut.on_device: {}
  - ut.on_device: {}
  - ut.no_device: {}
  - ut.on_device: {target: r1}
  - ut.on_device: {target: ghost}
    when: ["true"]
`))
	if err != nil {
		t.Fatal(err)
	}
	report := validate.Validate(validate.WorldView{Items: []inventory.InventoryItem{&inventorytest.Stub{StubName: "r1", StubState: inventory.StateActive}}, DAG: dag})
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0].Message, `"nosuch" matches no device or tag in this inventory (2 task(s), first tasks[0])`) {
		t.Fatalf("warnings %+v", report.Warnings)
	}
	for _, f := range report.Findings {
		if strings.Contains(f.Message, "nosuch") {
			t.Errorf("an unmatched target became a finding: %+v", f)
		}
	}
}
