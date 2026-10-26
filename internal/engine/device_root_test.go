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
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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
	// site is a key the type declares its accessors read; snmp_password,
	// nested and note are not, and an operator's own key is never there.
	registerDeviceRootTypes()
	props := func(site string) map[string]any {
		return map[string]any{"site": site, "snmp_password": "not-shown", "nested": map[string]any{"a": 1},
			"note": "an operator's own", "token_text": "AKIAIOSFODNN7EXAMPLEKEYS0123456789abcdefABCDEF"}
	}
	resolver := mapResolver{"web": {
		typedStub{&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive, Props: props("hq"), StubTags: []inventory.Tag{"hq"}}},
		typedStub{&inventorytest.Stub{StubID: "web2", StubName: "web2", StubState: inventory.StateActive, Props: props("br1")}},
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
    when: ["'hq' in device.tags"]
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
	for _, read := range []string{"device.properties.snmp_password", "device.properties.nested", "device.properties.note", "device.properties.token_text"} {
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

// registerDeviceRootTypes declares the two test device types' accessor keys,
// once per process: the registry refuses a second declaration, and a test
// run again in the same process (go test -count) would otherwise panic.
var registerDeviceRootTypes = sync.OnceFunc(func() {
	record.RegisterDispatchProperties(deviceRootTestType, "site", "snmp_password", "nested", "token_text")
	record.RegisterDispatchProperties(deviceRootTestType+"2", "port", "enabled")
})

// deviceRootTestType is the device type the test's devices report.
const deviceRootTestType = "device_root_test"

// typedStub is a stub that reports a device type, as record.Base does.
type typedStub struct{ *inventorytest.Stub }

// DeviceType reports the test's type.
func (typedStub) DeviceType() string { return deviceRootTestType }

// TestDeviceRoot_EveryPathAndItsLimits: a task reading device with a target
// rendered from data resolves its devices from the rendered target; a task
// in a block that runs as one unit, and a rollback_on_failure undo step,
// read the device they run on too; and lock_acquisition: all_at_plan_time
// with device is refused.
func TestDeviceRoot_EveryPathAndItsLimits(t *testing.T) {
	registerRollbackFixtures(t)
	seen := &paramsSeen{}
	run := func(runbook string, opts ...engine.ExecutorOption) engine.RunResult {
		t.Helper()
		opts = append([]engine.ExecutorOption{engine.WithRenderer(render.New())}, opts...)
		x := engine.NewExecutor(twoDevices(), seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
		result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	nodeErrors := func(r engine.RunResult) string {
		var b strings.Builder
		for _, n := range r.Nodes {
			if n.Err != nil {
				b.WriteString(n.Err.Error() + "; ")
			}
		}
		return b.String()
	}

	result := run("id: r\ntasks:\n  - rbtest.make:\n      name: x\n      target: \"{{ vars.which }}\"\n      note: \"{{ device.name }}\"\n    within: web\n",
		engine.WithVariables(map[string]any{"which": "web2"}))
	if result.HasErrors() || !slices.Equal(seen.seen, []string{"web2: web2"}) {
		t.Fatalf("a rendered target with device: %v, %s", seen.seen, nodeErrors(result))
	}

	seen.seen = nil
	result = run(`id: u
hosts: web
tasks:
  - name: a unit
    block:
      - rbtest.make: {name: one, note: "{{ device.name }} in the unit"}
      - rbtest.make: {name: two, target: web, note: "its own target"}
    rescue:
      - noop: {}
`)
	slices.Sort(seen.seen)
	if result.HasErrors() || !slices.Contains(seen.seen, "web1: web1 in the unit") || !slices.Contains(seen.seen, "web2: web2 in the unit") {
		t.Fatalf("a unit's task with device: %v, %s", seen.seen, nodeErrors(result))
	}

	// An authored undo step a block's rollback_on_failure runs reads the
	// device it undoes on: rendering it fails otherwise, and the rescue never
	// runs.
	undoing := &undoable{fail: map[string]bool{"b1@web1": true}}
	x := engine.NewExecutor(twoDevices(), undoing, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	rescued, err := x.Run(context.Background(), yamlRunbook(t, `id: f
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
        rollback:
          - name: "undo on {{ device.name }}"
            rbtest.make: {name: "back-{{ device.name }}"}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	if !rescued.Rescued() || !slices.Contains(undoing.calls("web1"), "r0") {
		t.Fatalf("an undo step reading device: web1 ran %v, %s", undoing.calls("web1"), nodeErrors(rescued))
	}

	result = run("id: l\nhosts: web\ntasks:\n  - rbtest.make: {name: x, note: \"{{ device.name }}\"}\n    lock_acquisition: all_at_plan_time\n")
	if !strings.Contains(nodeErrors(result), engine.ErrDeviceRootLockAll) {
		t.Errorf("device with all_at_plan_time: %s", nodeErrors(result))
	}
}

// nodeErrs is every error a run's nodes report, joined.
func nodeErrs(r engine.RunResult) string {
	var b strings.Builder
	for _, n := range r.Nodes {
		if n.Err != nil {
			b.WriteString(n.Err.Error() + "; ")
		}
	}
	return b.String()
}

// typedStub2 reports the second test device type, whose declared keys
// include a number and a boolean.
type typedStub2 struct{ *inventorytest.Stub }

// DeviceType reports the type with port and enabled declared.
func (typedStub2) DeviceType() string { return deviceRootTestType + "2" }

// TestDeviceRoot_CapabilitiesScalarsAndTheFailuresOfTheDevicePath: a
// condition reads the device's capabilities, a plain number and boolean its
// type reads are there, a target that cannot render or resolve fails the
// node naming that, a render that fails does so on the device path, a
// unit's rescue reads error beside device, and a run with no renderer
// refuses a rendered target.
func TestDeviceRoot_CapabilitiesScalarsAndTheFailuresOfTheDevicePath(t *testing.T) {
	registerRollbackFixtures(t)
	registerDeviceRootTypes()
	web1 := typedStub2{&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameSSHTransport}, Props: map[string]any{"port": 22, "enabled": true}}}
	resolver := mapResolver{"web": {web1}}
	seen := &paramsSeen{}
	withRenderer := engine.WithRenderer(render.New())
	run := func(actions engine.ActionExecutor, runbook string, opts ...engine.ExecutorOption) engine.RunResult {
		t.Helper()
		x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
		result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	result := run(seen, `id: c
hosts: web
tasks:
  - rbtest.make: {name: x, note: "{{ device.properties.port }} {{ device.properties.enabled }}"}
    when: ["'SSHTransportCapable' in device.capabilities"]
`, withRenderer)
	if result.HasErrors() || !slices.Equal(seen.seen, []string{"web1: 22 true"}) {
		t.Fatalf("capabilities and scalars: %v, %s", seen.seen, nodeErrs(result))
	}

	for name, tc := range map[string]struct{ runbook, want string }{
		"a target that cannot render":    {"id: a\ntasks:\n  - rbtest.make: {name: x, target: \"{{ vars.nosuch }}\", note: \"{{ device.name }}\"}\n    within: web\n", "failed to render the target"},
		"a target that resolves nowhere": {"id: b\nhosts: nosuch\ntasks:\n  - rbtest.make: {name: x, note: \"{{ device.name }}\"}\n", "failed to resolve target"},
		"a render that fails":            {"id: c\nhosts: web\ntasks:\n  - rbtest.make: {name: x, note: \"{{ device.name }} {{ vars.nosuch }}\"}\n", "failed to render"},
	} {
		if r := run(seen, tc.runbook, withRenderer); !strings.Contains(nodeErrs(r), tc.want) {
			t.Errorf("%s: %s", name, nodeErrs(r))
		}
	}

	// A unit's rescue task reads error beside device: it renders only if the
	// unit's failure reached the per-device path.
	failing := &undoable{fail: map[string]bool{"boom@web1": true}}
	rescued := run(failing, `id: u
hosts: web
tasks:
  - name: a unit
    block:
      - name: boom
        rbtest.make: {name: one}
      - name: elsewhere
        rbtest.make: {name: two, target: web}
    rescue:
      - name: r0
        rbtest.make: {name: r, note: "{{ device.name }} answers {{ error.task }}"}
`, withRenderer)
	if !rescued.Rescued() || !slices.Contains(failing.calls("web1"), "r0") {
		t.Errorf("a unit's rescue reading device and error: rescued %v, web1 ran %v, %s", rescued.Rescued(), failing.calls("web1"), nodeErrs(rescued))
	}

	// No renderer: a target rendered from data cannot be.
	r := run(seen, "id: n\ntasks:\n  - rbtest.make: {name: x, target: \"{{ vars.which }}\"}\n    within: web\n    when: [\"'x' in device.tags\"]\n")
	if !strings.Contains(nodeErrs(r), "no template renderer") {
		t.Errorf("no renderer: %s", nodeErrs(r))
	}
}
