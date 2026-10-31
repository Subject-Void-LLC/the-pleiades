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

// TestUnmatchedTargetWarnings_AHostsListNamesTheMissingMember: of a hosts:
// list, the warning names the member that matches nothing and not the list, a
// list whose members all match is quiet, and the devices a check reaches are
// the union.
func TestUnmatchedTargetWarnings_AHostsListNamesTheMissingMember(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "ut.on_device", Invoke: none, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture", ReadOnly: true},
		ExecutionContext: collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
	}}); err != nil {
		t.Fatal(err)
	}
	items := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "r1", StubName: "r1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "r2", StubName: "r2", StubState: inventory.StateActive},
	}
	eval, _ := engine.NewCELEvaluator()
	build := func(hosts string) *engine.DAG {
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: u\n" + hosts + "\ntasks:\n  - ut.on_device: {}\n"))
		if err != nil {
			t.Fatal(err)
		}
		return dag
	}

	warnings := validate.UnmatchedTargetWarnings(validate.WorldView{Items: items, DAG: build("hosts: [r1, ghost, r2]")})
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, `"ghost" matches no device or tag`) || strings.Contains(warnings[0].Message, "r1") {
		t.Errorf("a list with one missing member: warnings %+v", warnings)
	}
	if got := validate.UnmatchedTargetWarnings(validate.WorldView{Items: items, DAG: build("hosts: [r1, r2]")}); len(got) != 0 {
		t.Errorf("a list of devices that exist was warned of: %+v", got)
	}
	devices, missing := validate.WorldView{Items: items}.ResolveAll("r2,r1,r2")
	if len(devices) != 2 || len(missing) != 0 {
		t.Errorf("ResolveAll(r2,r1,r2) = %d devices, missing %v, want 2 and none", len(devices), missing)
	}
}

// TestTemplateRule_RefusesDeviceWithAllAtPlanTime: a task reading device runs
// on each device on its own, so validate refuses it lock_acquisition:
// all_at_plan_time, in a parameter and in a condition alike.
func TestTemplateRule_RefusesDeviceWithAllAtPlanTime(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "ut.note", Invoke: none, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture", ReadOnly: true},
		Doc: collection.Doc{Params: []collection.Param{{Name: "note", Type: "string"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	eval, _ := engine.NewCELEvaluator()
	for name, task := range map[string]string{
		"a parameter": "  - ut.note: {note: \"{{ device.name }}\"}\n    lock_acquisition: all_at_plan_time\n",
		"a condition": "  - ut.note: {note: x}\n    when: [\"'hq' in device.tags\"]\n    lock_acquisition: all_at_plan_time\n",
	} {
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: l\nhosts: r1\ntasks:\n" + task))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		report := validate.Validate(validate.WorldView{Items: []inventory.InventoryItem{&inventorytest.Stub{StubName: "r1", StubState: inventory.StateActive}}, DAG: dag})
		found := false
		for _, f := range report.Findings {
			found = found || strings.Contains(f.Message, engine.ErrDeviceRootLockAll)
		}
		if !found {
			t.Errorf("%s: findings %+v", name, report.Findings)
		}
	}
}

// TestTemplateRule_RefusesATargetThatReadsDevice: the target is what chooses
// the device, so it may not read it.
func TestTemplateRule_RefusesATargetThatReadsDevice(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "ut.note", Invoke: none, Manifest: collection.Manifest{
		Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture", ReadOnly: true},
	}}); err != nil {
		t.Fatal(err)
	}
	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: t\ntasks:\n  - ut.note: {target: \"{{ device.name }}\"}\n    within: r1\n"))
	if err != nil {
		t.Fatal(err)
	}
	report := validate.Validate(validate.WorldView{Items: []inventory.InventoryItem{&inventorytest.Stub{StubName: "r1", StubState: inventory.StateActive}}, DAG: dag})
	found := false
	for _, f := range report.Findings {
		found = found || strings.Contains(f.Message, "a target cannot read device")
	}
	if !found {
		t.Errorf("findings %+v", report.Findings)
	}
}
