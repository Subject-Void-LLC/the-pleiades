// Tests of the per-device decision's own bookkeeping: every declared cause is
// listed and has a reason of its own kind, and a caller with no inventory is
// told what is left for the run to decide. That validation and a real run
// agree for each cause is internal/validate's
// TestLockAll_ValidateAndTheRunAgreeForEveryCause.
package engine_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestPerDeviceCauses_EveryDeclaredCauseIsListed reads per_device.go and
// holds PerDeviceCauses to the constants declared there with the type
// PerDeviceCause, NotPerDevice aside. A cause declared and left out of the
// list would escape the test that requires validation and the run to agree
// on each one.
func TestPerDeviceCauses_EveryDeclaredCauseIsListed(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "per_device.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if typ, ok := spec.Type.(*ast.Ident); ok && typ.Name == "PerDeviceCause" {
			for i, name := range spec.Names {
				if name.Name == "NotPerDevice" {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("%s is not declared with a literal", name.Name)
				}
				declared = append(declared, strings.Trim(lit.Value, `"`))
			}
		}
		return true
	})
	var listed []string
	for _, c := range engine.PerDeviceCauses() {
		listed = append(listed, string(c))
	}
	slices.Sort(declared)
	slices.Sort(listed)
	if len(declared) == 0 || !slices.Equal(declared, listed) {
		t.Errorf("per_device.go declares the causes %v, and PerDeviceCauses lists %v", declared, listed)
	}
}

// TestPerDeviceCauses_EachHasAReason: every cause says why the lock cannot
// be taken, in the words every such refusal ends with, and what is not a
// cause says nothing.
func TestPerDeviceCauses_EachHasAReason(t *testing.T) {
	for _, c := range engine.PerDeviceCauses() {
		if reason := c.LockAllReason(); !strings.HasSuffix(reason, "so it cannot take lock_acquisition: all_at_plan_time, which takes every device's lock together") {
			t.Errorf("cause %q has the reason %q", c, reason)
		}
	}
	for _, c := range []engine.PerDeviceCause{engine.NotPerDevice, "no such cause"} {
		if reason := c.LockAllReason(); reason != "" {
			t.Errorf("%q is not a cause and has the reason %q", c, reason)
		}
	}
	// A cause that may read no device does not borrow the device's words.
	for _, c := range []engine.PerDeviceCause{engine.PerDeviceVarsDiffer, engine.PerDeviceConditionReadsFacts, engine.PerDeviceIteration, engine.PerDeviceRescue, engine.PerDeviceHandler, engine.PerDeviceConditionUnreadable} {
		if c.LockAllReason() == engine.ErrDeviceRootLockAll {
			t.Errorf("cause %q gives the reason of a task that reads device", c)
		}
	}
}

// TestRunsPerDevice_WhatACallerWithNoInventoryIsTold: a caller that cannot
// say which devices a task targets gets a definite cause for everything the
// runbook alone decides, and for a task reading vars that no set_fact makes
// differ, the one open question: decided at run time. The same task asked
// with the devices is answered either way, and with no engine no parameter
// is read at all.
func TestRunsPerDevice_WhatACallerWithNoInventoryIsTold(t *testing.T) {
	eng := render.New()
	ask := func(runbook string, eng render.Engine, devices func() []inventory.InventoryItem) (engine.PerDeviceCause, bool) {
		t.Helper()
		dag := yamlRunbook(t, runbook)
		return engine.RunsPerDevice(dag, "tasks[0]", dag.Nodes["tasks[0]"], eng, devices)
	}
	const readsVars = "id: v\nhosts: web\ntasks:\n  - name: reads\n    noop: {note: \"{{ vars.site }}\"}\n"
	withVars := func() []inventory.InventoryItem {
		return []inventory.InventoryItem{&inventorytest.Stub{StubID: "web1", StubName: "web1",
			Props: map[string]inventory.PropertyValue{engine.DeviceVarsProperty: map[string]any{"site": "hq"}}}}
	}
	plain := func() []inventory.InventoryItem {
		return []inventory.InventoryItem{&inventorytest.Stub{StubID: "web2", StubName: "web2"}}
	}
	for _, tc := range []struct {
		name, runbook string
		eng           render.Engine
		devices       func() []inventory.InventoryItem
		cause         engine.PerDeviceCause
		atRunTime     bool
	}{
		{"vars, no inventory", readsVars, eng, nil, engine.NotPerDevice, true},
		{"vars, a device bringing some", readsVars, eng, withVars, engine.PerDeviceVarsDiffer, false},
		{"vars, a device bringing none", readsVars, eng, plain, engine.NotPerDevice, false},
		{"vars in a parameter, no engine to read it", readsVars, nil, nil, engine.NotPerDevice, false},
		{"a device read in a parameter, no engine to read it", "id: d\nhosts: web\ntasks:\n  - name: reads\n    noop: {note: \"{{ device.name }}\"}\n", nil, nil, engine.NotPerDevice, false},
		{"a loop, no inventory", "id: l\nhosts: web\ntasks:\n  - name: loops\n    noop: {}\n    loop: [a, b]\n", eng, nil, engine.PerDeviceIteration, false},
		{"nothing per device, no inventory", "id: n\nhosts: web\ntasks:\n  - name: plain\n    noop: {}\n", eng, nil, engine.NotPerDevice, false},
		{"vars in a rollback step, no inventory", "id: r\nhosts: web\ntasks:\n  - name: undoable\n    noop: {}\n    rollback:\n      - noop: {note: \"{{ vars.site }}\"}\n", eng, nil, engine.NotPerDevice, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if cause, atRunTime := ask(tc.runbook, tc.eng, tc.devices); cause != tc.cause || atRunTime != tc.atRunTime {
				t.Errorf("cause %q, at run time %v; want %q, %v", cause, atRunTime, tc.cause, tc.atRunTime)
			}
		})
	}
}
