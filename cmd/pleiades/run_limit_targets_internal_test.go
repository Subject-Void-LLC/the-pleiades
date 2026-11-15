// Tests that --limit and serial: batches narrow which of a task's target
// devices a run reaches without calling a target the narrowing hid one that
// matches nothing: run 10's `target "wan,c8kv": wan matches no inventory host
// or tag` under --limit CORE1, though validate had passed.
package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// wanWorld is run 10's inventory in small: three routers tagged c8kv and one
// tagged wan.
func wanWorld() validate.WorldView {
	var items []inventory.InventoryItem
	for _, name := range []string{"core1", "br1", "br2"} {
		items = append(items, &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubTags: []inventory.Tag{"c8kv"}, StubState: inventory.StateActive})
	}
	items = append(items, &inventorytest.Stub{StubID: "wan1", StubName: "wan1", StubTags: []inventory.Tag{"wan"}, StubState: inventory.StateActive})
	return validate.WorldView{Items: items}
}

// buildRunbook builds runbook's DAG.
func buildRunbook(t *testing.T, runbook string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook))
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

// limitWorld is wanWorld narrowed by limit for dag, as run's pipeline narrows it.
func limitWorld(t *testing.T, dag *engine.DAG, limit string) validate.WorldView {
	t.Helper()
	l, err := engine.ParseLimit(limit)
	if err != nil {
		t.Fatal(err)
	}
	world, err := applyLimit(wanWorld(), dag, l, limit)
	if err != nil {
		t.Fatal(err)
	}
	world.DAG = dag
	return world
}

// configureWAN is run 10's runbook in small: a task on the wan router, named
// by its tag and as a list with the Catalysts, then one on every host.
const configureWAN = `id: configure_wan
hosts: c8kv,wan
tasks:
  - name: wan links
    noop:
      target: wan
  - name: wan and the catalysts
    noop:
      target: wan,c8kv
  - name: every router
    noop: {}
`

// TestLimit_ATargetOutsideTheLimitIsSkippedNotRefused: under --limit core1,
// the wan router's task is skipped naming the limit, the list reaches core1
// alone, and every host task runs on core1; validation on the limited world
// agrees, warning of nothing.
func TestLimit_ATargetOutsideTheLimitIsSkippedNotRefused(t *testing.T) {
	dag := buildRunbook(t, configureWAN)
	world := limitWorld(t, dag, "core1")
	if report := validate.Validate(world); report.HasErrors() || len(report.Warnings) > 0 {
		t.Errorf("validation on the limited world: %s", report.String())
	}
	log := &batchLog{}
	x := engine.NewExecutor(world, log, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err := x.Run(context.Background(), dag)
	if err != nil || result.HasErrors() {
		t.Fatalf("run: %v, %+v", err, result.Nodes)
	}
	if got, want := strings.Join(sortedLevels(log.order), ","), "wan and the catalysts@core1,every router@core1"; got != want {
		t.Errorf("ran %s, want %s", got, want)
	}
	skipped := result.Nodes[0]
	if !skipped.Skipped || skipped.SkipReason != `target "wan" names no device this run reaches: wan1 is outside --limit "core1"` {
		t.Errorf("the wan task: %+v", skipped)
	}
}

// TestLimit_ANameThatMatchesNothingIsStillRefused: a target the inventory
// has no device or tag for fails the run under a limit as without one, and
// validation still warns of it there.
func TestLimit_ANameThatMatchesNothingIsStillRefused(t *testing.T) {
	dag := buildRunbook(t, strings.Replace(configureWAN, "target: wan\n", "target: nosuch\n", 1))
	world := limitWorld(t, dag, "core1")
	result, err := engine.NewExecutor(world, &batchLog{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0).Run(context.Background(), dag)
	if err != nil || len(result.Nodes) == 0 || result.Nodes[0].Err == nil || !strings.Contains(result.Nodes[0].Err.Error(), `target "nosuch" matches no inventory host or tag`) {
		t.Errorf("a target matching nothing under a limit: %v, %+v", err, result.Nodes)
	}
}

// TestLimit_ValidationAgreesWithTheRunOnANarrowedTarget: the two checks that
// read a target that resolves to nothing, CapabilityRule for a transport
// action and the unmatched-target warning for a method, do not call a target
// the limit hid one that matches nothing, and still do for one that does; and
// the limited world's findings are validate's own (the wan router lacks the
// transport action's capability, which both refuse).
func TestLimit_ValidationAgreesWithTheRunOnANarrowedTarget(t *testing.T) {
	for target, wantFinding := range map[string]bool{"wan": false, "nosuch": true} {
		dag := buildRunbook(t, "id: v\nhosts: c8kv,wan\ntasks:\n  - name: legacy\n    ssh_exec:\n      target: "+target+"\n      command: uptime\n  - name: method\n    net.ios.command:\n      target: "+target+"\n      command: show version\n")
		world := limitWorld(t, dag, "core1")
		findings := validate.CapabilityRule(world)
		matchedNothing := false
		for _, f := range findings {
			if strings.Contains(f.Message, "matches no inventory host or tag") {
				matchedNothing = true
			}
		}
		if matchedNothing != wantFinding {
			t.Errorf("target %s: CapabilityRule found a target matching nothing %v, want %v (%+v)", target, matchedNothing, wantFinding, findings)
		}
		warned := len(validate.UnmatchedTargetWarnings(world)) > 0
		if warned != wantFinding {
			t.Errorf("target %s: unmatched-target warning %v, want %v", target, warned, wantFinding)
		}
		// Every rule holds the task to the devices validate holds it to, so
		// the limited world finds exactly what validate finds.
		whole := wanWorld()
		whole.DAG = dag
		if got, want := findingMessages(validate.Validate(world)), findingMessages(validate.Validate(whole)); len(want) == 0 || !slices.Equal(got, want) {
			t.Errorf("target %s: under --limit %v, validate %v", target, got, want)
		}
	}
}

// findingMessages returns report's findings' messages, sorted.
func findingMessages(report validate.Report) []string {
	var out []string
	for _, f := range report.Findings {
		out = append(out, f.Message)
	}
	slices.Sort(out)
	return out
}

// TestSerial_ATargetInAnotherBatchRunsInItsOwn: a task naming the wan router
// is skipped in each batch the router is not in and runs in the one it is,
// rather than failing the first batch; under --limit as well.
func TestSerial_ATargetInAnotherBatchRunsInItsOwn(t *testing.T) {
	dag := buildRunbook(t, "id: s\nhosts: c8kv,wan\nserial: 2\ntasks:\n  - name: wan links\n    noop:\n      target: wan\n")
	for _, limit := range []string{"", "br*,wan1"} {
		world := wanWorld()
		if limit != "" {
			world = limitWorld(t, dag, limit)
		}
		runWorld, plan := planSerial(world, dag, collection.ModeExecute)
		if plan == nil {
			t.Fatal("no batch plan")
		}
		log := &batchLog{}
		x := engine.NewExecutor(runWorld, log, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err, stopped := runBatches(context.Background(), x, dag, plan, false)
		if err != nil || stopped != nil || result.HasErrors() {
			t.Fatalf("limit %q: %v, %v, %+v", limit, err, stopped, result.Nodes)
		}
		if got := strings.Join(log.order, ","); got != "wan links@wan1" {
			t.Errorf("limit %q: ran %s, want wan links@wan1 once", limit, got)
		}
		skips := 0
		for _, n := range result.Nodes {
			if n.Skipped && strings.Contains(n.SkipReason, "wan1 is in another serial: batch") {
				skips++
			}
		}
		if want := len(plan.batches) - 1; skips != want {
			t.Errorf("limit %q: %d batches skipped the task, want %d: %+v", limit, skips, want, result.Nodes)
		}
		// The batch running last hides nothing of its own, and a host the
		// limit left out says the limit, not a batch, hid it.
		for _, item := range wanWorld().Items {
			why := plan.resolver.HiddenBecause(item)
			switch {
			case item.Name() == "wan1" && why != "":
				t.Errorf("limit %q: wan1, in the batch running, hidden because %q", limit, why)
			case item.Name() == "core1" && limit != "" && why != `outside --limit "br*,wan1"`:
				t.Errorf("limit %q: core1 hidden because %q", limit, why)
			}
		}
	}
}

// guardedWAN is a block whose task fails where batchLog is told to fail it,
// with a rescue and an always on the wan router.
const guardedWAN = `id: g
hosts: c8kv,wan
SERIAL
tasks:
  - name: guarded
    block:
      - name: breaks
        noop: {}
    rescue:
      - name: fix on wan
        noop:
          target: wan
    always:
      - name: cleanup on wan
        noop:
          target: wan
`

// checkNotRescued fails t unless result failed, nothing in it reads as
// rescued, and its rescue task failed naming why.
func checkNotRescued(t *testing.T, result engine.RunResult, why string) {
	t.Helper()
	if !result.HasErrors() || result.Rescued() {
		t.Fatalf("HasErrors %v, Rescued %v: %+v", result.HasErrors(), result.Rescued(), result.Nodes)
	}
	for _, n := range result.Nodes {
		if n.NodeID == "tasks[0].rescue[0]" && (n.Err == nil || !strings.Contains(n.Err.Error(), "does not answer the failure its block is handling") || !strings.Contains(n.Err.Error(), why)) {
			t.Errorf("the rescue: %+v", n)
		}
		if n.NodeID == "tasks[0].always[0]" && (!n.Skipped || !strings.Contains(n.SkipReason, why)) {
			t.Errorf("the always: %+v", n)
		}
	}
}

// TestLimit_ARescueOutsideTheLimitDoesNotRescue: under --limit core1 a block
// failing on core1 whose rescue names only the wan router is not rescued;
// the run fails, naming the limit.
func TestLimit_ARescueOutsideTheLimitDoesNotRescue(t *testing.T) {
	dag := buildRunbook(t, strings.Replace(guardedWAN, "SERIAL\n", "", 1))
	world := limitWorld(t, dag, "core1")
	result, err := engine.NewExecutor(world, &batchLog{fail: map[string]bool{"core1": true}}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0).Run(context.Background(), dag)
	if err != nil {
		t.Fatal(err)
	}
	checkNotRescued(t, result, `wan1 is outside --limit "core1"`)
}

// TestSerial_ARescueInAnotherBatchDoesNotRescue: a block failing on br1 in
// the first batch, whose rescue names only the wan router of the second, is
// not rescued; the batch fails and the rollout stops.
func TestSerial_ARescueInAnotherBatchDoesNotRescue(t *testing.T) {
	dag := buildRunbook(t, strings.Replace(guardedWAN, "SERIAL\n", "serial: 2\n", 1))
	runWorld, plan := planSerial(wanWorld(), dag, collection.ModeExecute)
	x := engine.NewExecutor(runWorld, &batchLog{fail: map[string]bool{"br1": true}}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err, stopped := runBatches(context.Background(), x, dag, plan, false)
	if err != nil || stopped == nil {
		t.Fatalf("err %v, stopped %v", err, stopped)
	}
	checkNotRescued(t, result, "wan1 is in another serial: batch")
}
