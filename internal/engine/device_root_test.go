// Tests of the device root: a task reading it renders and decides once per
// device, with that device's name and allowed properties; a property whose
// name names a secret, or that is not a plain value, is not there; and a
// target may not read it.
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

// paramsSeen records each call's rendered note param, by device.
type paramsSeen struct {
	mu   sync.Mutex
	seen []string
}

func (p *paramsSeen) Execute(_ context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	note, _ := task.Params["note"].(string)
	p.seen = append(p.seen, d.Name()+": "+note)
	return engine.ActionResult{}, nil
}

func TestDeviceRoot_RendersAndDecidesPerDevice(t *testing.T) {
	registerRollbackFixtures(t)
	props := func(site string) map[string]any {
		return map[string]any{"site": site, "snmp_password": "not-shown", "nested": map[string]any{"a": 1}}
	}
	resolver := mapResolver{"web": {
		&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive, Props: props("hq")},
		&inventorytest.Stub{StubID: "web2", StubName: "web2", StubState: inventory.StateActive, Props: props("br1")},
	}}
	seen := &paramsSeen{}
	x := engine.NewExecutor(resolver, seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, `id: d
hosts: web
tasks:
  - name: each says where it is
    rbtest.make:
      name: x
      note: "{{ device.name }} at {{ device.properties.site }}"
  - name: only at hq
    when: ["device.properties.site == 'hq'"]
    rbtest.make:
      name: y
      note: "hq only"
`))
	if err != nil {
		t.Fatal(err)
	}
	if result.HasErrors() {
		t.Fatalf("%+v", result.Nodes)
	}
	slices.Sort(seen.seen)
	if want := []string{"web1: hq only", "web1: web1 at hq", "web2: web2 at br1"}; !slices.Equal(seen.seen, want) {
		t.Errorf("ran %v, want %v", seen.seen, want)
	}

	// A secret-named or structured property is not in the root, so a
	// template reading one fails as reading any absent name does.
	for _, read := range []string{"device.properties.snmp_password", "device.properties.nested"} {
		seen.seen = nil
		result, err := x.Run(context.Background(), yamlRunbook(t, "id: s\nhosts: web\ntasks:\n  - rbtest.make:\n      name: z\n      note: \"{{ "+read+" }}\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !result.HasErrors() || len(seen.seen) != 0 {
			t.Errorf("%s was rendered: %v", read, seen.seen)
		}
	}

	// A target may not read the device: the target is what chooses it.
	result, err = x.Run(context.Background(), yamlRunbook(t, "id: t\ntasks:\n  - rbtest.make:\n      name: z\n      target: \"{{ device.name }}\"\n    within: web\n"))
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, n := range result.Nodes {
		if n.Err != nil && strings.Contains(n.Err.Error(), "a target cannot read device") {
			failed = true
		}
	}
	if !failed {
		t.Errorf("a target reading device: %+v", result.Nodes)
	}
}
