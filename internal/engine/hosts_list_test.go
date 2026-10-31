// Tests for hosts: written as a list: the parse (and the line each refusal
// names), the union a resolver returns, and a real executor run over it.
package engine_test

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// hostsRunbook is a runbook whose hosts: line is the one under test.
func hostsRunbook(hosts string) string {
	return "id: x\n" + hosts + "\ntasks:\n  - name: n\n    noop: {}\n"
}

// TestHostsList_ParsesToTheSameDAGAsTheCommaString checks that a list and
// Ansible's a,b spelling build one DAG, version included, so nothing
// downstream can tell them apart.
func TestHostsList_ParsesToTheSameDAGAsTheCommaString(t *testing.T) {
	list, err := buildYAML(t, hostsRunbook("hosts: [sw1, sw2]"))
	if err != nil {
		t.Fatal(err)
	}
	block, err := buildYAML(t, hostsRunbook("hosts:\n  - sw1\n  - sw2"))
	if err != nil {
		t.Fatal(err)
	}
	str, err := buildYAML(t, hostsRunbook("hosts: sw1,sw2"))
	if err != nil {
		t.Fatal(err)
	}
	for name, dag := range map[string]*engine.DAG{"flow list": list, "block list": block} {
		if dag.Hosts != str.Hosts || dag.Version != str.Version {
			t.Errorf("%s: hosts %q version %s; the string form gave %q %s", name, dag.Hosts, dag.Version, str.Hosts, str.Version)
		}
	}
	one, err := buildYAML(t, hostsRunbook("hosts: [sw1]"))
	if err != nil || one.Hosts != "sw1" {
		t.Errorf("a one-item list: hosts %v, err %v", one, err)
	}
}

// TestHostsList_ABadValueNamesItsLine checks each refusal says where it is.
func TestHostsList_ABadValueNamesItsLine(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, line, want string }{
		"empty list":            {"hosts: []", "line 2", "empty list"},
		"mapping item":          {"hosts:\n  - sw1\n  - {a: b}", "line 4", "item 2 is a mapping"},
		"nested list":           {"hosts:\n  - [sw1]", "line 3", "item 1 is a list"},
		"empty item":            {"hosts:\n  - sw1\n  - \"\"", "line 4", "item 2 is empty"},
		"comma in an item":      {"hosts: [\"a,b\"]", "line 2", "holds a comma"},
		"a mapping, not a list": {"hosts:\n  a: b", "line 3", "not a mapping"},
	} {
		_, err := buildYAML(t, hostsRunbook(tc.yaml))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.line) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %q, want it to say %q and %q", name, err, tc.line, tc.want)
		}
	}
}

// TestResolveTargets covers the union, its order and duplicates, a tag beside
// a name, a name that holds a comma, and a name that matches nothing.
func TestResolveTargets(t *testing.T) {
	mk := func(name string, tags ...inventory.Tag) *inventorytest.Stub {
		return &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive, StubTags: tags}
	}
	sw1, sw2, odd := mk("sw1", "edge"), mk("sw2", "edge"), mk("a,b")
	byName := map[string][]inventory.InventoryItem{
		"sw1": {sw1}, "sw2": {sw2}, "edge": {sw1, sw2}, "a,b": {odd},
	}
	r := engine.TargetResolverFunc(func(target string) []inventory.InventoryItem { return byName[target] })

	names := func(items []inventory.InventoryItem) string {
		var out []string
		for _, d := range items {
			out = append(out, d.Name())
		}
		return strings.Join(out, " ")
	}
	for name, tc := range map[string]struct{ target, devices, missing string }{
		"a name":                   {"sw1", "sw1", ""},
		"a tag":                    {"edge", "sw1 sw2", ""},
		"a list in order":          {"sw2,sw1", "sw2 sw1", ""},
		"a tag and a member of it": {"edge,sw1", "sw1 sw2", ""},
		"spaces around names":      {"sw1 , sw2", "sw1 sw2", ""},
		"a literal comma name":     {"a,b", "a,b", ""},
		"nothing matches":          {"nope", "", "nope"},
		"one of a list missing":    {"sw1,nope,sw2", "sw1 sw2", "nope"},
		"every one missing":        {"x,y", "", "x y"},
		"no target":                {"", "", ""},
	} {
		devices, missing := engine.ResolveTargets(r, tc.target)
		if got := names(devices); got != tc.devices {
			t.Errorf("%s: devices %q, want %q", name, got, tc.devices)
		}
		if got := strings.Join(missing, " "); got != tc.missing {
			t.Errorf("%s: missing %q, want %q", name, got, tc.missing)
		}
	}
}

// TestExecutor_AHostsListRunsOnEachMemberOnce runs a built hosts: list through
// the real executor, and a list with a missing member through it again: the
// second must refuse rather than run on the members that exist.
func TestExecutor_AHostsListRunsOnEachMemberOnce(t *testing.T) {
	sw1 := &inventorytest.Stub{StubID: "sw1", StubName: "sw1", StubState: inventory.StateActive}
	sw2 := &inventorytest.Stub{StubID: "sw2", StubName: "sw2", StubState: inventory.StateActive}
	resolver := mapResolver{"sw1": {sw1}, "sw2": {sw2}}

	run := func(hosts string) ([]string, error) {
		var mu sync.Mutex
		var ran []string
		actions := deviceRecordingActionExecutor{onExecute: func(d inventory.InventoryItem) {
			mu.Lock()
			defer mu.Unlock()
			if d != nil {
				ran = append(ran, d.Name())
			}
		}}
		dag, err := buildYAML(t, hostsRunbook(hosts))
		if err != nil {
			t.Fatal(err)
		}
		x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		res, err := x.Run(context.Background(), dag)
		if err == nil && res.HasErrors() {
			for _, n := range res.Nodes {
				if n.Err != nil {
					err = n.Err
				}
			}
		}
		sort.Strings(ran)
		return ran, err
	}

	ran, err := run("hosts: [sw2, sw1, sw2]")
	if err != nil || strings.Join(ran, " ") != "sw1 sw2" {
		t.Errorf("a list of two (one named twice): ran %v, err %v", ran, err)
	}
	ran, err = run("hosts: [sw1, ghost]")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("a list naming a missing device: ran %v, err %v, want a refusal naming ghost", ran, err)
	}
	if len(ran) != 0 {
		t.Errorf("a list naming a missing device ran on %v before refusing", ran)
	}
}
