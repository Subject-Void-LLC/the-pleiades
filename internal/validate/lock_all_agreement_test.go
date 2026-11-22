// Package validate_test: validate and the run agree on which tasks may take
// lock_acquisition: all_at_plan_time, for every cause the engine has of a
// task running on each device on its own (engine.PerDeviceCauses). Each case
// is one runbook asked of validate's rule and of a real run through the
// executor; both must refuse it, in the same words, or both accept it. A
// cause with no case fails the test, so a cause added to the engine cannot
// reach the run alone.
package validate_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// lockAllTask is the name every case gives the task that asks for the lock.
const lockAllTask = "every lock"

// lockAllAction is an action executor that fails the task named fails, which
// is how a case reaches a rescue, and counts the calls of the lock-all task.
type lockAllAction struct{ calls atomic.Int64 }

// Execute implements engine.ActionExecutor.
func (c *lockAllAction) Execute(_ context.Context, task *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	if task.Name == "fails" {
		return engine.ActionResult{}, errors.New("failed on purpose")
	}
	if task.Name == lockAllTask {
		c.calls.Add(1)
	}
	return engine.ActionResult{}, nil
}

// webDevices resolves the target web to two active devices, web1 to the
// first of them, and anything else to none. vars, when set, are variables
// web1 brings of its own.
type webDevices struct{ vars map[string]any }

// Resolve implements engine.TargetResolver.
func (w webDevices) Resolve(target string) []inventory.InventoryItem {
	web1 := &inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive}
	if w.vars != nil {
		web1.Props = map[string]inventory.PropertyValue{engine.DeviceVarsProperty: w.vars}
	}
	switch target {
	case "web":
		return []inventory.InventoryItem{web1, &inventorytest.Stub{StubID: "web2", StubName: "web2", StubState: inventory.StateActive}}
	case "web1":
		return []inventory.InventoryItem{web1}
	}
	return nil
}

// lockAllCase is one runbook asked of validate and of the run.
type lockAllCase struct {
	name string
	// cause is why the lock-all task runs per device, and so why both refuse
	// it; engine.NotPerDevice for a control both accept.
	cause engine.PerDeviceCause
	// unlocked says the task, despite its name, does not ask for the lock:
	// a control that runs per device for cause and is refused by neither.
	unlocked bool
	// runbook is the whole runbook, its lock-all task named lockAllTask.
	runbook string
	// skipTags, when set, is the tag selection the run and validate are
	// given, as run --skip-tags gives it.
	skipTags []string
	// after, when set, changes the built runbook into one the builder would
	// not produce: the lock on a task the builder refuses it on, or a
	// condition that cannot be read. It is how the engine's and validate's
	// backstops are reached; every other case is the runbook as written.
	after func(t *testing.T, dag *engine.DAG)
	// resolver is the inventory, webDevices{} when left nil.
	resolver engine.TargetResolver
}

// oneTask is a runbook on the web devices whose one task is the lock-all
// task with keys beside it.
func oneTask(keys string) string {
	return "id: l\nhosts: web\ntasks:\n  - name: " + lockAllTask + "\n    noop: {}\n" + keys
}

// lockOn sets lock_acquisition: all_at_plan_time on node after the build.
func lockOn(node string) func(*testing.T, *engine.DAG) {
	return func(t *testing.T, dag *engine.DAG) {
		t.Helper()
		task := dag.Nodes[node]
		if task == nil {
			t.Fatalf("no node %s", node)
		}
		task.LockAcquisition = engine.AcquisitionAllAtPlanTime
	}
}

// lockAllCases is every case. The rescue and handler runbooks the builder
// refuses outright are in TestLockAllTheBuilderRefuses; here the same two
// causes are reached the ways a built runbook still can reach them.
func lockAllCases() []lockAllCase {
	const lock = "    lock_acquisition: all_at_plan_time\n"
	const deviceRead = "    when: [\"device.name != ''\"]\n"
	// A block that runs as one unit while its task with a target of its own
	// is kept, and per device once --skip-tags leaves that task out.
	const unitBlock = "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: fails\n        noop: {}\n      - name: elsewhere\n        noop: {target: web1}\n        tags: [other]\n    rescue:\n      - name: " + lockAllTask + "\n        noop: {}\n        lock_acquisition: all_at_plan_time\n"
	const rescued = "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: fails\n        noop: {}\n    rescue:\n      - name: " + lockAllTask + "\n        noop: {}\n"
	const notified = "id: h\nhosts: web\ntasks:\n  - name: changes\n    noop: {}\n    notify: " + lockAllTask + "\nhandlers:\n  - name: " + lockAllTask + "\n    noop: {}\n"
	const setFact = "id: v\nhosts: web\ntasks:\n  - name: set it\n    set_fact:\n      x: 1\n  - name: " + lockAllTask + "\n    noop: {}\n" + lock
	return []lockAllCase{
		{name: "a rescue task, its block per device under a tag selection", cause: engine.PerDeviceRescue, runbook: unitBlock, skipTags: []string{"other"}},
		{name: "a rescue task locked after the build", cause: engine.PerDeviceRescue, runbook: rescued, after: lockOn("tasks[0].rescue[0]")},
		{name: "a loop", cause: engine.PerDeviceIteration, runbook: oneTask(lock + "    loop: [a, b]\n")},
		{name: "with_items", cause: engine.PerDeviceIteration, runbook: oneTask(lock + "    with_items: [a, b]\n")},
		{name: "a loop written as an expression", cause: engine.PerDeviceIteration, runbook: strings.Replace(oneTask(lock+"    loop: \"{{ vars.names }}\"\n"), "tasks:\n", "vars:\n  names: [a, b]\ntasks:\n", 1)},
		{name: "a retry", cause: engine.PerDeviceIteration, runbook: oneTask(lock + "    register: r\n    until: [\"true\"]\n")},
		{name: "a loop that also reads device", cause: engine.PerDeviceIteration, runbook: oneTask(lock + "    loop: [a, b]\n" + deviceRead)},
		// Inside a block with a rescue the run decides in another place
		// (runGuarded), and gives the same reason there.
		{name: "a loop inside a block with a rescue", cause: engine.PerDeviceIteration, runbook: "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: " + lockAllTask + "\n        noop: {}\n        lock_acquisition: all_at_plan_time\n        loop: [a, b]\n    rescue:\n      - name: answer\n        noop: {}\n"},
		{name: "a handler locked after the build", cause: engine.PerDeviceHandler, runbook: notified, after: lockOn("tasks.handlers[0]")},
		{name: "a condition that cannot be read", cause: engine.PerDeviceConditionUnreadable, runbook: oneTask(lock + "    when_cel: \"true\"\n"), after: func(_ *testing.T, dag *engine.DAG) {
			dag.Nodes["tasks[0]"].WhenCEL = "((("
		}},
		{name: "a condition reading device", cause: engine.PerDeviceConditionReadsDevice, runbook: oneTask(lock + deviceRead)},
		{name: "a condition reading a stored fact", cause: engine.PerDeviceConditionReadsFacts, runbook: oneTask(lock + "    when: facts[\"os.family\"] == \"debian\"\n")},
		{name: "a parameter reading device", cause: engine.PerDeviceParamsReadDevice, runbook: "id: l\nhosts: web\ntasks:\n  - name: " + lockAllTask + "\n    noop: {note: \"{{ device.name }}\"}\n" + lock},
		{name: "a condition reading vars, after a set_fact", cause: engine.PerDeviceVarsDiffer, runbook: setFact + "    when: [\"vars.x == 1\"]\n"},
		{name: "a parameter reading vars, after a set_fact", cause: engine.PerDeviceVarsDiffer, runbook: strings.Replace(setFact, "    noop: {}\n"+lock, "    noop: {note: \"{{ vars.x }}\"}\n"+lock, 1)},
		{name: "a condition reading vars a device brings", cause: engine.PerDeviceVarsDiffer, runbook: oneTask(lock + "    when_cel: \"vars.?site.orValue('') == 'hq'\"\n"), resolver: webDevices{vars: map[string]any{"site": "hq"}}},

		{name: "control: every lock, nothing per device", runbook: oneTask(lock)},
		{name: "control: a loop, a lock per device", cause: engine.PerDeviceIteration, unlocked: true, runbook: oneTask("    loop: [a, b]\n")},
		{name: "control: a retry, a lock per device", cause: engine.PerDeviceIteration, unlocked: true, runbook: oneTask("    register: r\n    until: [\"true\"]\n")},
		{name: "control: vars no device brings and no task sets", runbook: oneTask(lock + "    when_cel: \"vars.?site.orValue('') == ''\"\n")},
		// A unit's rescue is decided once for the unit, so it takes the lock.
		{name: "control: a rescue task in a block that runs as one unit", runbook: unitBlock},
	}
}

// TestLockAll_ValidateAndTheRunAgreeForEveryCause builds each runbook once
// and asks both: validate's rule, and a real run through the executor.
// Whatever the run refuses for taking every lock together, validate refuses
// in the run's own words, which are the cause's (LockAllReason), and what
// the run accepts validate accepts. The engine's own answer for the task is
// held to the case's cause too, so a case cannot pass on another cause that
// happens to share its words. Every cause the engine has needs a case.
func TestLockAll_ValidateAndTheRunAgreeForEveryCause(t *testing.T) {
	covered := map[engine.PerDeviceCause]bool{}
	for _, tc := range lockAllCases() {
		if !tc.unlocked {
			covered[tc.cause] = true
		}
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, tc.runbook)
			if tc.after != nil {
				tc.after(t, dag)
			}
			if tc.skipTags != nil {
				selected, err := engine.Select(dag, engine.TagFilter{SkipTags: tc.skipTags})
				if err != nil {
					t.Fatal(err)
				}
				dag = selected
			}
			resolver := tc.resolver
			if resolver == nil {
				resolver = webDevices{}
			}
			node := ""
			for id, task := range dag.Nodes {
				if task.Name == lockAllTask {
					node = id
				}
			}
			if node == "" {
				t.Fatalf("the runbook has no task named %q", lockAllTask)
			}
			reason := tc.cause.LockAllReason()
			if (tc.cause == engine.NotPerDevice) != (reason == "") {
				t.Fatalf("cause %q has the reason %q", tc.cause, reason)
			}
			if tc.unlocked {
				reason = ""
			}

			// The engine's answer, asked as validate asks it.
			world := validate.WorldView{DAG: dag, Resolver: resolver}
			got, atRunTime := engine.RunsPerDevice(dag, node, dag.Nodes[node], render.New(), func() []inventory.InventoryItem {
				devices, _ := world.ResolveAll(engine.TaskTarget(dag, dag.Nodes[node]))
				return devices
			})
			if got != tc.cause || atRunTime {
				t.Errorf("engine.RunsPerDevice: %q (at run time %v), want %q", got, atRunTime, tc.cause)
			}

			// Plan time.
			var planned []string
			for _, f := range validate.TemplateRule(world) {
				planned = append(planned, f.Message)
			}
			if reason == "" && len(planned) != 0 {
				t.Errorf("validate: findings %q, want none", planned)
			}
			if reason != "" && (len(planned) != 1 || !strings.HasSuffix(planned[0], ": "+reason)) {
				t.Errorf("validate: findings %q, want one ending in %q", planned, reason)
			}

			// The run itself, on the same compiled runbook.
			action := &lockAllAction{}
			x := engine.NewExecutor(resolver, action, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatal(err)
			}
			var ran []string
			for _, n := range result.Nodes {
				if n.Err != nil && engine.BaseNodeID(n.NodeID) == node {
					ran = append(ran, n.Err.Error())
				}
			}
			if reason == "" && (len(ran) != 0 || action.calls.Load() == 0) {
				t.Errorf("run: the task's errors %q after %d calls, want it to run without one", ran, action.calls.Load())
			}
			if reason != "" && (len(ran) != 1 || !strings.HasSuffix(ran[0], ": "+reason)) {
				t.Errorf("run: the task's errors %q, want one ending in %q", ran, reason)
			}
			if reason != "" && action.calls.Load() != 0 {
				t.Errorf("a refused task still ran %d times", action.calls.Load())
			}
			// The same words: validate's finding is the run's node error with
			// the word task before it.
			if len(planned) != len(ran) || (len(ran) == 1 && planned[0] != "task "+ran[0]) {
				t.Errorf("validate and the run must say the same thing\nvalidate: %q\nrun: %q", planned, ran)
			}
		})
	}
	for _, cause := range engine.PerDeviceCauses() {
		if !covered[cause] {
			t.Errorf("the engine runs a task per device for the cause %q, and no case here holds validate and the run to it: add one to lockAllCases", cause)
		}
	}
}

// TestLockAllTheBuilderRefuses: a rescue task in a block that handles
// failures per device, and a handler, can never take the lock, so the
// runbook does not build, on any tier, and the refusal ends in the words the
// run and validate use for the same cause. A rescue task in a block that
// runs as one unit builds.
func TestLockAllTheBuilderRefuses(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, runbook string
		cause         engine.PerDeviceCause
	}{
		{"a rescue task", "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: fails\n        noop: {}\n    rescue:\n      - name: every lock\n        noop: {}\n        lock_acquisition: all_at_plan_time\n", engine.PerDeviceRescue},
		{"a task of a block inside a rescue", "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: fails\n        noop: {}\n    rescue:\n      - name: inner\n        block:\n          - name: every lock\n            noop: {}\n            lock_acquisition: all_at_plan_time\n", engine.PerDeviceRescue},
		{"a handler", "id: h\nhosts: web\ntasks:\n  - name: changes\n    noop: {}\n    notify: every lock\nhandlers:\n  - name: every lock\n    noop: {}\n    lock_acquisition: all_at_plan_time\n", engine.PerDeviceHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewBuilder(eval).BuildFromYAML([]byte(tc.runbook))
			if err == nil || !strings.HasSuffix(err.Error(), ": "+tc.cause.LockAllReason()) || !strings.Contains(err.Error(), "every lock") {
				t.Fatalf("build: %v, want a refusal naming the task and ending in %q", err, tc.cause.LockAllReason())
			}
		})
	}
	unit := "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: fails\n        noop: {}\n      - name: elsewhere\n        noop: {target: web1}\n    rescue:\n      - name: every lock\n        noop: {}\n        lock_acquisition: all_at_plan_time\n"
	if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte(unit)); err != nil {
		t.Errorf("a unit's rescue task taking every lock: %v, want it built", err)
	}
}

// TestLockAllIsAskedInOnePlace: lock_all.go is the only file of this package
// that names engine.AcquisitionAllAtPlanTime, and it asks
// engine.RunsPerDevice. A rule that tested the lock against questions of its
// own is how validate came to miss a cause the run had (loops, then rescue
// tasks), so a second place is refused here rather than found in a run.
func TestLockAllIsAskedInOnePlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	asks := false
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "AcquisitionAllAtPlanTime":
				if file != "lock_all.go" {
					t.Errorf("%s names AcquisitionAllAtPlanTime: whether a task may take that lock is asked of engine.RunsPerDevice, in lock_all.go alone", file)
				}
			case "RunsPerDevice":
				asks = asks || file == "lock_all.go"
			}
			return true
		})
	}
	if !asks {
		t.Error("lock_all.go does not ask engine.RunsPerDevice")
	}
}
