// Tests of authored variables (Phase 87) through the real Executor: the order
// of the tiers, a block's variables reaching its tasks, variables reading each
// other, a set_fact (one device's and a global one), and a device's own vars
// property.
package engine_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// varsDevices returns two typed devices named a and b under the target "web",
// each with the given vars property.
func varsDevices(varsA, varsB map[string]any) mapResolver {
	mk := func(name string, vars map[string]any) inventory.InventoryItem {
		props := map[string]any{"site": name}
		if vars != nil {
			props["vars"] = vars
		}
		return typedStub{&inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive, Props: props}}
	}
	return mapResolver{"web": {mk("a", varsA), mk("b", varsB)}}
}

// factAware runs a set_fact in process and every other call as paramsSeen.
type factAware struct {
	seen    *paramsSeen
	builtin engine.ActionExecutor
}

// Execute implements engine.ActionExecutor.
func (f factAware) Execute(ctx context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	if task.FQCN == engine.SetFactName {
		return f.builtin.Execute(ctx, task, d)
	}
	return f.seen.Execute(ctx, task, d)
}

// varsFixtures registers the shared fixtures once for this file's tests.
var varsFixtures sync.Once

// runVars runs runbook on resolver with extra as the run's variables and
// returns what each rbtest.make call saw, sorted, and the result.
func runVars(t *testing.T, resolver mapResolver, extra map[string]any, runbook string) ([]string, engine.RunResult) {
	t.Helper()
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	seen := &paramsSeen{}
	opts := []engine.ExecutorOption{engine.WithRenderer(render.New())}
	if extra != nil {
		opts = append(opts, engine.WithVariables(extra))
	}
	x := engine.NewExecutor(resolver, factAware{seen, engine.NewBuiltinActionExecutor()}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	slices.Sort(seen.seen)
	return seen.seen, result
}

// TestVars_TiersWinInOrder: the narrower scope wins, so a task's vars beat the
// block's, a device's facts and its own vars beat the runbook's, and the
// extra variables beat all of them.
func TestVars_TiersWinInOrder(t *testing.T) {
	seen, result := runVars(t, varsDevices(map[string]any{"t": "device", "u": "device"}, map[string]any{"t": "device", "u": "device"}), map[string]any{"e": "extra"}, `id: tiers
hosts: web
vars:
  t: runbook
  u: runbook
  v: runbook
  w: runbook
tasks:
  - name: set a fact over the task's own
    set_fact:
      e: fact
      f: fact
  - block:
      - name: read
        vars:
          w: task
          f: task
          x: task
        rbtest.make:
          name: r
          note: "t={{ vars.t }} u={{ vars.u }} v={{ vars.v }} w={{ vars.w }} e={{ vars.e }} f={{ vars.f }} x={{ vars.x }}"
    vars:
      v: block
      w: block
`)
	if result.HasErrors() {
		t.Fatalf("%+v", result.Nodes)
	}
	want := "a: t=device u=device v=block w=task e=extra f=task x=task"
	if len(seen) != 2 || seen[0] != want {
		t.Errorf("saw %v, want %q first", seen, want)
	}
}

// TestVars_ReadEachOtherAndRefuseACircle: a variable reading another renders
// whatever order they are written in, and two reading each other fail naming
// them.
func TestVars_ReadEachOtherAndRefuseACircle(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: chain
hosts: web
vars:
  full: "{{ vars.base }}/{{ vars.leaf }}"
  base: /srv
  leaf: "{{ vars.name | upper }}"
  name: app
tasks:
  - rbtest.make:
      name: r
      note: "{{ vars.full }}"
`)
	if result.HasErrors() || len(seen) != 2 || seen[0] != "a: /srv/APP" {
		t.Fatalf("saw %v, %+v", seen, result.Nodes)
	}
	_, result = runVars(t, varsDevices(nil, nil), nil, `id: circle
hosts: web
vars:
  x: "{{ vars.y }}"
  y: "{{ vars.x }}"
tasks:
  - rbtest.make:
      name: r
      note: "{{ vars.x }}"
`)
	if !result.HasErrors() {
		t.Fatal("a circle of variables rendered")
	}
	if msg := result.Nodes[0].Err.Error(); !strings.Contains(msg, "variable") {
		t.Errorf("error %q does not name the variable", msg)
	}
}

// TestVars_ExtraVariablesAreNeverRendered: a launch value holding {{ stays
// text, since only authored tiers are templates.
func TestVars_ExtraVariablesAreNeverRendered(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), map[string]any{"msg": "{{ vars.other }}"}, `id: raw
hosts: web
vars:
  other: authored
tasks:
  - rbtest.make:
      name: r
      note: "{{ vars.msg }}"
`)
	if result.HasErrors() || len(seen) != 2 || seen[0] != "a: {{ vars.other }}" {
		t.Fatalf("saw %v, %+v", seen, result.Nodes)
	}
}

// TestVars_ADevicesOwnVarsDifferByDevice: the vars property a device brings
// (group_vars and host_vars) is read per device, and beats the runbook's.
func TestVars_ADevicesOwnVarsDifferByDevice(t *testing.T) {
	seen, result := runVars(t, varsDevices(map[string]any{"vlan": 10, "dns": "a-dns"}, map[string]any{"vlan": 20, "dns": "b-dns"}), nil, `id: hostvars
hosts: web
vars:
  dns: runbook-dns
tasks:
  - rbtest.make:
      name: r
      note: "vlan {{ vars.vlan }} dns {{ vars.dns }}"
`)
	want := []string{"a: vlan 10 dns a-dns", "b: vlan 20 dns b-dns"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}

// TestVars_ASecretShapedDeviceVarIsNotThere: a device var named like a secret
// does not reach a template.
func TestVars_ASecretShapedDeviceVarIsNotThere(t *testing.T) {
	_, result := runVars(t, varsDevices(map[string]any{"snmp_password": "hunter2"}, map[string]any{"snmp_password": "hunter2"}), nil, `id: secret
hosts: web
tasks:
  - rbtest.make:
      name: r
      note: "{{ vars.snmp_password }}"
`)
	if !result.HasErrors() {
		t.Fatal("a secret-named device var was rendered")
	}
}

// TestSetFact_IsPerDeviceUnlessGlobal: a fact belongs to the device it was set
// for; a global one is every device's, and two devices setting one to
// different values in a task is an error.
func TestSetFact_IsPerDeviceUnlessGlobal(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: facts
hosts: web
tasks:
  - set_fact:
      mine: "{{ device.name }}-fact"
  - set_fact:
      shared: one-for-all
      global: true
  - rbtest.make:
      name: r
      note: "{{ vars.mine }} {{ vars.shared }}"
`)
	want := []string{"a: a-fact one-for-all", "b: b-fact one-for-all"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}

	_, result = runVars(t, varsDevices(nil, nil), nil, `id: clash
hosts: web
tasks:
  - set_fact:
      shared: "{{ device.name }}"
      global: true
`)
	if !result.HasErrors() {
		t.Fatal("two devices set one global fact to different values")
	}
	found := false
	for _, n := range result.Nodes {
		if n.Err != nil && strings.Contains(n.Err.Error(), "different values") {
			found = true
		}
	}
	if !found {
		t.Errorf("no node named the clash: %+v", result.Nodes)
	}
}

// TestSetFact_RefusesABadName: a fact name that {{ vars.name }} could not read,
// a global flag that is not a boolean, and no facts at all each fail the task.
func TestSetFact_RefusesABadName(t *testing.T) {
	for name, body := range map[string]string{
		"bad name":        "bad-name: 1",
		"global not bool": "x: 1\n      global: maybe",
		"no facts":        "global: true",
	} {
		t.Run(name, func(t *testing.T) {
			_, result := runVars(t, varsDevices(nil, nil), nil, "id: bad\nhosts: web\ntasks:\n  - set_fact:\n      "+body+"\n")
			if !result.HasErrors() {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// TestSetFact_NoOverrideKeepsTheFirstValue: with no_override a fact already
// set keeps its value, per device and global, and without it the later wins.
func TestSetFact_NoOverrideKeepsTheFirstValue(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: keep
hosts: web
tasks:
  - set_fact:
      mine: first
      shared: first
      global: true
  - set_fact:
      mine: second
      shared: second
      global: true
      no_override: true
  - set_fact:
      later: first
  - set_fact:
      later: second
  - rbtest.make:
      name: r
      note: "{{ vars.mine }} {{ vars.shared }} {{ vars.later }}"
`)
	// mine and shared are set first, and the no_override setting leaves them;
	// later has no no_override, so its second setting wins.
	want := []string{"a: first first second", "b: first first second"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}

// TestVars_AGlobalFactReplacesTheRunbooksVar: a global set_fact assigns in the
// runbook's own scope, so it replaces the declaration, while a task's own vars
// still beat it.
func TestVars_AGlobalFactReplacesTheRunbooksVar(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: global
hosts: web
vars:
  g: runbook
  h: runbook
tasks:
  - set_fact:
      g: fact
      h: fact
      global: true
  - vars:
      h: task
    rbtest.make:
      name: r
      note: "{{ vars.g }} {{ vars.h }}"
`)
	want := []string{"a: fact task", "b: fact task"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}
