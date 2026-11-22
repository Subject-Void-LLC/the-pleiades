// Tests of the per-device decision's own bookkeeping: every cause is declared
// in per_device.go and listed, each has a reason of its own kind, a task goes
// down the per-device path only with the cause the run's own question gave,
// and the one cause that reads the inventory reads what its caller hands it.
// That validation and a real run agree for each cause is internal/validate's
// TestLockAll_ValidateAndTheRunAgreeForEveryCause.
package engine_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// causeType is the type whose constants are the causes.
const causeType = "PerDeviceCause"

// engineSources parses every file of this package that is not a test, by
// file name.
func engineSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ast.File{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = parsed
	}
	if len(out) == 0 {
		t.Fatal("no source files found")
	}
	return out
}

// isCauseValue reports whether expr makes a cause by its syntax: a
// conversion, PerDeviceCause("x"), or the name of a constant already known
// to be one.
func isCauseValue(expr ast.Expr, known map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return isCauseValue(e.X, known)
	case *ast.CallExpr:
		fun, ok := e.Fun.(*ast.Ident)
		return ok && fun.Name == causeType
	case *ast.Ident:
		return known[e.Name]
	}
	return false
}

// causeConstants returns, by file, every constant of the package that is a
// PerDeviceCause, however it is spelled: declared with the type, converted
// to it, given another cause's value, or left to repeat the line above it in
// a constant block. It reads the files until no new one is found, so a
// constant named after one declared later in another file is still seen.
func causeConstants(files map[string]*ast.File) map[string][]string {
	known := map[string]bool{}
	where := map[string]string{}
	for found := true; found; {
		found = false
		for name, file := range files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				// A line with no type and no value repeats the last line that
				// had either.
				repeats := false
				for _, s := range gen.Specs {
					spec := s.(*ast.ValueSpec)
					is := repeats
					if spec.Type != nil || len(spec.Values) > 0 {
						typ, typed := spec.Type.(*ast.Ident)
						is = typed && typ.Name == causeType
						for _, v := range spec.Values {
							is = is || isCauseValue(v, known)
						}
						repeats = is
					}
					if !is {
						continue
					}
					for _, n := range spec.Names {
						if !known[n.Name] {
							known[n.Name], where[n.Name], found = true, name, true
						}
					}
				}
			}
		}
	}
	out := map[string][]string{}
	for constant, file := range where {
		out[file] = append(out[file], constant)
	}
	for _, list := range out {
		slices.Sort(list)
	}
	return out
}

// TestPerDeviceCause_EveryConstantIsDeclaredInPerDeviceAndListed: every
// constant of type PerDeviceCause in the package is declared in
// per_device.go, whatever its spelling, and PerDeviceCauses lists each of
// them but NotPerDevice. A cause declared in another file, or left out of
// the list, would escape the test that requires validation and the run to
// agree on each one.
func TestPerDeviceCause_EveryConstantIsDeclaredInPerDeviceAndListed(t *testing.T) {
	byFile := causeConstants(engineSources(t))
	for file, constants := range byFile {
		if file != "per_device.go" {
			t.Errorf("%s declares the per-device cause(s) %v: a cause is declared in per_device.go, where RunsPerDevice answers with it and PerDeviceCauses lists it", file, constants)
		}
	}
	declared := byFile["per_device.go"]
	if !slices.Contains(declared, "NotPerDevice") || len(declared) < 2 {
		t.Fatalf("per_device.go declares %v: the reader of the constants is broken", declared)
	}
	// The listed values, by the name each is declared under.
	values := map[string]string{}
	ast.Inspect(engineSources(t)["per_device.go"], func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if i < len(spec.Values) {
				if lit, ok := spec.Values[i].(*ast.BasicLit); ok {
					values[name.Name] = strings.Trim(lit.Value, `"`)
				}
			}
		}
		return true
	})
	var want []string
	for _, name := range declared {
		if name == "NotPerDevice" {
			continue
		}
		value, ok := values[name]
		if !ok {
			t.Fatalf("%s is not declared with a text of its own", name)
		}
		want = append(want, value)
	}
	var listed []string
	for _, c := range engine.PerDeviceCauses() {
		listed = append(listed, string(c))
	}
	slices.Sort(want)
	slices.Sort(listed)
	if !slices.Equal(want, listed) {
		t.Errorf("per_device.go declares the causes %v, and PerDeviceCauses lists %v", want, listed)
	}
}

// TestPerDeviceCause_OnlyPerDeviceSendsATaskDownThePath: runPerDevice and
// rescueOn, the two ways a task runs on each device on its own, are called
// only with a variable named cause, and in a function that calls either,
// cause is only ever given what r.perDevice returned. So a new reason to run
// a task per device cannot be wired into the run beside RunsPerDevice, with
// a constant written at the call or a decision of the caller's own, where
// validation would never hear of it.
func TestPerDeviceCause_OnlyPerDeviceSendsATaskDownThePath(t *testing.T) {
	paths := map[string]bool{"runPerDevice": true, "rescueOn": true}
	calls := 0
	for name, file := range engineSources(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			sends := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !paths[sel.Sel.Name] {
					return true
				}
				sends = true
				calls++
				last, ok := call.Args[len(call.Args)-1].(*ast.Ident)
				if !ok || last.Name != "cause" {
					t.Errorf("%s: %s calls %s with a cause that is not the variable cause: pass what r.perDevice returned", name, fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
			if !sends {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); !ok || id.Name != "cause" {
						continue
					}
					fromPerDevice := false
					if len(assign.Rhs) == len(assign.Lhs) {
						if call, ok := assign.Rhs[i].(*ast.CallExpr); ok {
							if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "perDevice" {
								fromPerDevice = true
							}
						}
					}
					if !fromPerDevice {
						t.Errorf("%s: %s gives cause a value that is not r.perDevice's answer", name, fn.Name.Name)
					}
				}
				return true
			})
			// A cause declared with var, or as a parameter, has no perDevice
			// behind it either.
			ast.Inspect(fn, func(n ast.Node) bool {
				switch d := n.(type) {
				case *ast.ValueSpec:
					for _, id := range d.Names {
						if id.Name == "cause" {
							t.Errorf("%s: %s declares cause with var: take it from r.perDevice", name, fn.Name.Name)
						}
					}
				case *ast.Field:
					for _, id := range d.Names {
						if id.Name == "cause" {
							t.Errorf("%s: %s takes cause as a parameter and sends a task down the per-device path with it: ask r.perDevice there", name, fn.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	if calls < 4 {
		t.Fatalf("found %d calls of runPerDevice and rescueOn, and the run makes at least 4: the reader of the calls is broken", calls)
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

// TestRunsPerDevice_WhatTheCallerHandsItDecidesVars: a task reading vars
// runs per device when a device its caller names brings variables of its
// own, and not otherwise; the devices are asked for only then; and with no
// engine no parameter is read at all, as a run with no renderer reads none.
func TestRunsPerDevice_WhatTheCallerHandsItDecidesVars(t *testing.T) {
	eng := render.New()
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
		// asked says the devices are needed for the answer.
		asked bool
		cause engine.PerDeviceCause
	}{
		{"vars, a device bringing some", readsVars, eng, withVars, true, engine.PerDeviceVarsDiffer},
		{"vars, a device bringing none", readsVars, eng, plain, true, engine.NotPerDevice},
		{"vars in a parameter, no engine to read it", readsVars, nil, withVars, false, engine.NotPerDevice},
		{"a device read in a parameter, no engine to read it", "id: d\nhosts: web\ntasks:\n  - name: reads\n    noop: {note: \"{{ device.name }}\"}\n", nil, withVars, false, engine.NotPerDevice},
		{"a loop", "id: l\nhosts: web\ntasks:\n  - name: loops\n    noop: {}\n    loop: [a, b]\n", eng, withVars, false, engine.PerDeviceIteration},
		{"nothing per device", "id: n\nhosts: web\ntasks:\n  - name: plain\n    noop: {}\n", eng, withVars, false, engine.NotPerDevice},
		{"vars in a rollback step, a device bringing some", "id: r\nhosts: web\ntasks:\n  - name: undoable\n    noop: {}\n    rollback:\n      - noop: {note: \"{{ vars.site }}\"}\n", eng, withVars, true, engine.PerDeviceVarsDiffer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlRunbook(t, tc.runbook)
			asked := false
			cause := engine.RunsPerDevice(dag, "tasks[0]", dag.Nodes["tasks[0]"], tc.eng, func() []inventory.InventoryItem {
				asked = true
				return tc.devices()
			})
			if cause != tc.cause || asked != tc.asked {
				t.Errorf("cause %q, devices asked for %v; want %q, %v", cause, asked, tc.cause, tc.asked)
			}
		})
	}
}
