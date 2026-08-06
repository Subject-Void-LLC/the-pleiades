package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
	"go.uber.org/goleak"
)

// mapResolver is a minimal engine.TargetResolver test double: a plain map
// from target string to the devices it names. The real production
// resolver is validate.WorldView (internal/validate/validate.go); this
// package tests against the interface directly rather than importing
// validate, so these tests exercise exactly the contract Executor depends
// on and nothing more.
type mapResolver map[string][]inventory.InventoryItem

func (m mapResolver) Resolve(target string) []inventory.InventoryItem { return m[target] }

// buildDAG compiles payload into a *engine.DAG through the real Builder,
// exactly the path a runbook file goes through, so these tests exercise
// genuinely compiled CEL conditions, not hand-built Program values.
func buildDAG(t *testing.T, payload string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := engine.NewBuilder(eval).Build([]byte(payload))
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}
	return dag
}

// TestExecutor_ControllerSideChain confirms a runbook of controller-side
// tasks (no target device) runs every node in order, none skipped, none
// failed.
func TestExecutor_ControllerSideChain(t *testing.T) {
	defer goleak.VerifyNone(t)

	dag := buildDAG(t, `{
		"id": "chain",
		"tasks": [
			{"name": "a", "fqcn": "noop"},
			{"name": "b", "fqcn": "noop"},
			{"name": "c", "fqcn": "noop"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 node results, got %d: %+v", len(result.Nodes), result.Nodes)
	}

	wantOrder := []string{"tasks[0]", "tasks[1]", "tasks[2]"}
	for i, want := range wantOrder {
		if result.Nodes[i].NodeID != want {
			t.Errorf("expected result %d to be %s, got %s", i, want, result.Nodes[i].NodeID)
		}
		if result.Nodes[i].Device != "" {
			t.Errorf("expected a controller-side task to report no device, got %q", result.Nodes[i].Device)
		}
	}
}

// TestExecutor_ConditionalBranch_ReleaseGate is Phase W5's Release Gate:
// a multi-node workflow with a conditional edge executes end to end
// locally and takes the correct branch. "precheck" registers a stat;
// "reboot"'s when_cel reads it and is true, so it runs and reports
// changed; "skip-me"'s when_cel reads the same stat and is false, so it
// is skipped, never executed at all.
func TestExecutor_ConditionalBranch_ReleaseGate(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "conditional-demo",
		"tasks": [
			{"name": "precheck", "fqcn": "noop", "register": "precheck", "params": {"needs_reboot": true}},
			{"name": "reboot", "fqcn": "noop", "when_cel": "stat.precheck[\"\"].needs_reboot == true", "params": {"changed": true}},
			{"name": "skip-me", "fqcn": "noop", "when_cel": "stat.precheck[\"\"].needs_reboot == false"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 node results (the third Skipped, not omitted), got %d: %+v", len(result.Nodes), result.Nodes)
	}

	byID := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byID[n.NodeID] = n
	}

	if byID["tasks[1]"].Skipped || !byID["tasks[1]"].Changed {
		t.Fatalf("expected reboot (tasks[1]) to run and report changed, got %+v", byID["tasks[1]"])
	}
	if !byID["tasks[2]"].Skipped {
		t.Fatalf("expected skip-me (tasks[2]) to be skipped, got %+v", byID["tasks[2]"])
	}
	if !strings.Contains(byID["tasks[2]"].SkipReason, "stat.precheck[\"\"].needs_reboot == false") {
		t.Errorf("expected SkipReason to name skip-me's own when_cel expression, got %q", byID["tasks[2]"].SkipReason)
	}
}

// TestExecutor_ConditionalBranch_NodesVariable is Phase 9's own end-to-end
// proof, at the real Executor call site rather than the bare
// Program/Evaluator primitives: a task's when_cel can reach an earlier
// task's registered result through the "nodes" root (PLAN.md Section 27's
// cross-node aggregation), not only through "stat". It is the same
// three-task shape as TestExecutor_ConditionalBranch_ReleaseGate
// immediately above, with "nodes." in place of "stat." in every when_cel
// expression, proving runNode really does bind both roots to the same
// WorkflowContext snapshot (executor.go) rather than only "stat" working
// by convention.
func TestExecutor_ConditionalBranch_NodesVariable(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "conditional-demo-nodes",
		"tasks": [
			{"name": "precheck", "fqcn": "noop", "register": "precheck", "params": {"needs_reboot": true}},
			{"name": "reboot", "fqcn": "noop", "when_cel": "nodes.precheck[\"\"].needs_reboot == true", "params": {"changed": true}},
			{"name": "skip-me", "fqcn": "noop", "when_cel": "nodes.precheck[\"\"].needs_reboot == false"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 node results (the third Skipped, not omitted), got %d: %+v", len(result.Nodes), result.Nodes)
	}

	byID := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byID[n.NodeID] = n
	}

	if byID["tasks[1]"].Skipped || !byID["tasks[1]"].Changed {
		t.Fatalf("expected reboot (tasks[1]) to run and report changed, got %+v", byID["tasks[1]"])
	}
	if !byID["tasks[2]"].Skipped {
		t.Fatalf("expected skip-me (tasks[2]) to be skipped, got %+v", byID["tasks[2]"])
	}
	if !strings.Contains(byID["tasks[2]"].SkipReason, "nodes.precheck[\"\"].needs_reboot == false") {
		t.Errorf("expected SkipReason to name skip-me's own when_cel expression, got %q", byID["tasks[2]"].SkipReason)
	}
}

// TestExecutor_DeviceFanOut confirms a task whose target resolves to
// several devices runs against every one of them, each getting its own
// NodeResult carrying that device's ID.
func TestExecutor_DeviceFanOut(t *testing.T) {
	devices := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "host-a", StubName: "host-a", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "host-b", StubName: "host-b", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "host-c", StubName: "host-c", StubState: inventory.StateActive},
	}
	resolver := mapResolver{"webservers": devices}

	dag := buildDAG(t, `{
		"id": "fanout",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "webservers"}}]
	}`)

	x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("expected one result per resolved device, got %d: %+v", len(result.Nodes), result.Nodes)
	}

	gotDevices := map[string]bool{}
	for _, n := range result.Nodes {
		gotDevices[n.Device] = true
	}
	for _, d := range devices {
		if !gotDevices[string(d.ID())] {
			t.Errorf("expected a result for device %q, got results: %+v", d.ID(), result.Nodes)
		}
	}
}

// TestExecutor_Parallel_RunsConcurrently confirms a parallel task's
// children genuinely run concurrently through the real Executor.Run call
// (not just LevelIterator.Next in isolation, which TestLevelIterator_Diamond,
// level_iterator_test.go, already proves), using the same high-water-mark
// tracking pattern as TestExecutor_ConcurrencyBound below. It also proves
// the synthetic fan-out/join markers never reach the ActionExecutor at
// all (the count of onExecute calls equals exactly childCount, not
// childCount+2), and that every child plus both markers still produced a
// NodeResult, so Phase 10's synthetic fast path (executor.go's runNode)
// is provably inert rather than silently dropping results.
func TestExecutor_Parallel_RunsConcurrently(t *testing.T) {
	const childCount = 4

	var current, peak, calls int32
	tracker := trackingActionExecutor{
		onExecute: func() {
			atomic.AddInt32(&calls, 1)
			n := atomic.AddInt32(&current, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&current, -1)
		},
	}

	dag := buildDAG(t, `{
		"id": "parallel-concurrency",
		"tasks": [
			{"name": "fanout", "parallel": [
				{"name": "p0", "fqcn": "noop"},
				{"name": "p1", "fqcn": "noop"},
				{"name": "p2", "fqcn": "noop"},
				{"name": "p3", "fqcn": "noop"}
			]}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, tracker, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	if got := atomic.LoadInt32(&peak); got != childCount {
		t.Fatalf("expected all %d parallel children to run concurrently (peak == %d), observed peak %d", childCount, childCount, got)
	}
	if got := atomic.LoadInt32(&calls); got != childCount {
		t.Fatalf("expected exactly %d ActionExecutor calls (the synthetic fanout/join markers must never reach it), got %d", childCount, got)
	}

	wantIDs := map[string]bool{
		"tasks[0].fanout": true, "tasks[0].join": true,
		"tasks[0].parallel[0]": true, "tasks[0].parallel[1]": true,
		"tasks[0].parallel[2]": true, "tasks[0].parallel[3]": true,
	}
	if len(result.Nodes) != len(wantIDs) {
		t.Fatalf("expected %d node results, got %d: %+v", len(wantIDs), len(result.Nodes), result.Nodes)
	}
	for _, n := range result.Nodes {
		if !wantIDs[n.NodeID] {
			t.Errorf("unexpected node result %q", n.NodeID)
		}
		if n.Err != nil {
			t.Errorf("node %q: expected no error, got %v", n.NodeID, n.Err)
		}
	}
}

// TestExecutor_UnknownTargetIsError confirms a non-empty target that
// resolves to no device fails the node with an actionable error, rather
// than silently doing nothing (this codebase's own established defect
// class; see FAILURE_PATTERNS.md).
func TestExecutor_UnknownTargetIsError(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "bad-target",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "nonexistent"}}]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the node: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the node to fail, got %+v", result.Nodes)
	}
	if !strings.Contains(result.Nodes[0].Err.Error(), "nonexistent") {
		t.Errorf("expected the error to name the target, got: %v", result.Nodes[0].Err)
	}
}

// TestExecutor_RefusesNonActiveDevice is the runtime half of the chain
// audit's lifecycle finding (IMPLEMENTATION.md Phase W5):
// LifecycleState.CanExecute had zero production callers anywhere, so a
// quarantined, decommissioning, or otherwise non-Active device accepted
// real work. It targets one Active and one quarantined device: the
// quarantined one must be skipped, with its device ID and state named in
// SkipReason, and the trackingActionExecutor below (a real ActionExecutor,
// not a bypassed one) must never have been invoked for it at all, since
// the point of a runtime guard is that the action genuinely never runs.
func TestExecutor_RefusesNonActiveDevice(t *testing.T) {
	active := &inventorytest.Stub{StubID: "active-host", StubName: "active-host", StubState: inventory.StateActive}
	quarantined := &inventorytest.Stub{StubID: "quarantined-host", StubName: "quarantined-host", StubState: inventory.StateQuarantined}
	resolver := mapResolver{"fleet": {active, quarantined}}

	var mu sync.Mutex
	var executedAgainst []string
	recording := deviceRecordingActionExecutor{onExecute: func(device inventory.InventoryItem) {
		mu.Lock()
		defer mu.Unlock()
		if device != nil {
			executedAgainst = append(executedAgainst, string(device.ID()))
		}
	}}

	dag := buildDAG(t, `{
		"id": "lifecycle-guard",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "fleet"}}]
	}`)

	x := engine.NewExecutor(resolver, recording, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors (a lifecycle skip is not a failure), got %+v", result.Nodes)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected one result per resolved device, got %d: %+v", len(result.Nodes), result.Nodes)
	}

	byDevice := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byDevice[n.Device] = n
	}

	activeResult := byDevice[string(active.ID())]
	if activeResult.Skipped {
		t.Errorf("expected the active device to actually run, got skipped: %+v", activeResult)
	}

	quarantinedResult := byDevice[string(quarantined.ID())]
	if !quarantinedResult.Skipped {
		t.Fatalf("expected the quarantined device to be skipped, got %+v", quarantinedResult)
	}
	if quarantinedResult.Device != string(quarantined.ID()) {
		t.Errorf("expected a lifecycle skip to still name the device, got Device=%q", quarantinedResult.Device)
	}
	if !strings.Contains(quarantinedResult.SkipReason, "quarantined") {
		t.Errorf("expected SkipReason to name the actual state, got %q", quarantinedResult.SkipReason)
	}
	if !strings.Contains(quarantinedResult.SkipReason, string(quarantined.ID())) {
		t.Errorf("expected SkipReason to name the device, got %q", quarantinedResult.SkipReason)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range executedAgainst {
		if id == string(quarantined.ID()) {
			t.Fatalf("the ActionExecutor was invoked against the quarantined device; the runtime guard must stop this before dispatch, not just report it as skipped")
		}
	}
	if len(executedAgainst) != 1 || executedAgainst[0] != string(active.ID()) {
		t.Errorf("expected the ActionExecutor to run exactly once, against the active device, got %v", executedAgainst)
	}
}

// TestExecutor_LockAcquisitionFailure confirms a device already locked by
// someone else fails that device's node with an actionable error, rather
// than silently skipping the lock (Executor's first real caller of
// lock.Manager).
func TestExecutor_LockAcquisitionFailure(t *testing.T) {
	locks := lock.NewInProcessManager()

	device := &inventorytest.Stub{StubID: "locked-host", StubName: "locked-host", StubState: inventory.StateActive}
	lease, err := locks.Acquire(context.Background(), string(device.ID()), time.Minute, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("failed to pre-acquire the lock: %v", err)
	}
	defer lease.Release(context.Background())

	resolver := mapResolver{"target1": {device}}
	dag := buildDAG(t, `{
		"id": "locked",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "target1"}}]
	}`)

	x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), locks, event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the node: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the node to fail due to lock contention, got %+v", result.Nodes)
	}
	if !strings.Contains(result.Nodes[0].Err.Error(), "lock") {
		t.Errorf("expected a lock-related error, got: %v", result.Nodes[0].Err)
	}
}

// TestExecutor_AcquisitionAllAtPlanTime is this phase's own Release Gate
// scenario for the "all-at-plan-time" acquisition strategy: a task
// targeting three devices, one of them already locked by a rival holder,
// set to AcquisitionAllAtPlanTime. It must fail the whole node with zero
// devices having actually run their action (proving all-or-nothing for
// real, not just declared), unlike the default AcquisitionPerDeviceAsReached
// strategy (proven in the same test), which still runs the two unblocked
// devices despite the third being contended.
func TestExecutor_AcquisitionAllAtPlanTime(t *testing.T) {
	deviceA := &inventorytest.Stub{StubID: "fleet-a", StubName: "fleet-a", StubState: inventory.StateActive}
	deviceB := &inventorytest.Stub{StubID: "fleet-b", StubName: "fleet-b", StubState: inventory.StateActive}
	deviceC := &inventorytest.Stub{StubID: "fleet-c", StubName: "fleet-c", StubState: inventory.StateActive}
	resolver := mapResolver{"fleet": {deviceA, deviceB, deviceC}}

	t.Run("all-at-plan-time runs zero devices when one is contended", func(t *testing.T) {
		locks := lock.NewInProcessManager()
		rival, err := locks.Acquire(context.Background(), string(deviceB.ID()), time.Minute, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("setup: failed to pre-lock %s: %v", deviceB.ID(), err)
		}
		defer func() { _ = rival.Release(context.Background()) }()

		var mu sync.Mutex
		var executedAgainst []string
		recording := deviceRecordingActionExecutor{onExecute: func(device inventory.InventoryItem) {
			mu.Lock()
			defer mu.Unlock()
			if device != nil {
				executedAgainst = append(executedAgainst, string(device.ID()))
			}
		}}

		dag := buildDAG(t, `{
			"id": "all-at-plan-time",
			"tasks": [{"name": "reload", "fqcn": "noop", "params": {"target": "fleet"}, "lock_acquisition": "all_at_plan_time"}]
		}`)

		x := engine.NewExecutor(resolver, recording, locks, event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run itself should not fail, only the node: %v", err)
		}
		if !result.HasErrors() {
			t.Fatalf("expected the node to fail due to lock contention, got %+v", result.Nodes)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(executedAgainst) != 0 {
			t.Fatalf("expected zero devices to run under AcquisitionAllAtPlanTime when one is contended, got action executed against %v", executedAgainst)
		}
	})

	t.Run("per-device-as-reached still runs the unblocked devices", func(t *testing.T) {
		locks := lock.NewInProcessManager()
		rival, err := locks.Acquire(context.Background(), string(deviceB.ID()), time.Minute, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("setup: failed to pre-lock %s: %v", deviceB.ID(), err)
		}
		defer func() { _ = rival.Release(context.Background()) }()

		var mu sync.Mutex
		var executedAgainst []string
		recording := deviceRecordingActionExecutor{onExecute: func(device inventory.InventoryItem) {
			mu.Lock()
			defer mu.Unlock()
			if device != nil {
				executedAgainst = append(executedAgainst, string(device.ID()))
			}
		}}

		dag := buildDAG(t, `{
			"id": "per-device-as-reached",
			"tasks": [{"name": "reload", "fqcn": "noop", "params": {"target": "fleet"}}]
		}`)

		x := engine.NewExecutor(resolver, recording, locks, event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run itself should not fail, only the node: %v", err)
		}
		if !result.HasErrors() {
			t.Fatalf("expected the contended device's own node result to fail, got %+v", result.Nodes)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(executedAgainst) != 2 {
			t.Fatalf("expected the default per-device-as-reached strategy to still run the 2 unblocked devices, got action executed against %v", executedAgainst)
		}
		for _, id := range executedAgainst {
			if id == string(deviceB.ID()) {
				t.Fatalf("the contended device must never have run, got action executed against it: %v", executedAgainst)
			}
		}
	})

	t.Run("all-at-plan-time runs every device when none are contended", func(t *testing.T) {
		var mu sync.Mutex
		var executedAgainst []string
		recording := deviceRecordingActionExecutor{onExecute: func(device inventory.InventoryItem) {
			mu.Lock()
			defer mu.Unlock()
			if device != nil {
				executedAgainst = append(executedAgainst, string(device.ID()))
			}
		}}

		dag := buildDAG(t, `{
			"id": "all-at-plan-time-uncontended",
			"tasks": [{"name": "reload", "fqcn": "noop", "params": {"target": "fleet"}, "lock_acquisition": "all_at_plan_time"}]
		}`)

		x := engine.NewExecutor(resolver, recording, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.HasErrors() {
			t.Fatalf("expected no errors with nothing contended, got %+v", result.Nodes)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(executedAgainst) != 3 {
			t.Fatalf("expected all 3 devices to run when none are contended, got action executed against %v", executedAgainst)
		}
	})
}

// TestExecutor_ActionFailureStopsSubsequentLevels confirms a node that
// fails (here, an fqcn with no in-process implementation) stops the walk
// before the next level runs, rather than continuing on with a result
// that may have depended on it.
func TestExecutor_ActionFailureStopsSubsequentLevels(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "fails-then-stops",
		"tasks": [
			{"name": "unsupported", "fqcn": "ssh_exec"},
			{"name": "after", "fqcn": "noop"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run itself should not fail, only the node: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the first node to fail, got %+v", result.Nodes)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("expected the walk to stop after the failing level, got %d results: %+v", len(result.Nodes), result.Nodes)
	}
}

// TestExecutor_EventsPublished confirms Executor is a real event.Bus
// caller: every node outcome publishes exactly one event, observable by a
// real subscriber.
func TestExecutor_EventsPublished(t *testing.T) {
	bus := event.NewInProcessBus()

	var mu sync.Mutex
	var statuses []string
	err := bus.Subscribe(context.Background(), "pleiades.events.>", func(e event.Event) error {
		mu.Lock()
		defer mu.Unlock()
		var payload struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(e.Data, &payload)
		statuses = append(statuses, payload.Status)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	dag := buildDAG(t, `{
		"id": "events",
		"tasks": [
			{"name": "a", "fqcn": "noop", "params": {"changed": true}},
			{"name": "b", "fqcn": "noop", "when_cel": "false"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), bus, engine.NewInProcessWorkflowContext(), 0)
	if _, err := x.Run(context.Background(), dag); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Publish is fire-and-forget (delivered on its own goroutine); give
	// the in-process bus a moment to deliver before asserting.
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(statuses)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 published events, got %d: %v", len(statuses), statuses)
	}
	want := map[string]bool{"changed": false, "skipped": false}
	for _, s := range statuses {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for status, seen := range want {
		if !seen {
			t.Errorf("expected a %q event, got statuses: %v", status, statuses)
		}
	}
}

// TestExecutor_ConcurrencyBound confirms maxConcurrency genuinely bounds
// how many device executions run their action at once, across the whole
// Run call, using a fake ActionExecutor that tracks the high-water mark
// of concurrently-running calls.
func TestExecutor_ConcurrencyBound(t *testing.T) {
	const deviceCount = 8
	const bound = 2

	devices := make([]inventory.InventoryItem, deviceCount)
	for i := range devices {
		id := inventory.DeviceID("host-" + string(rune('a'+i)))
		devices[i] = &inventorytest.Stub{StubID: id, StubName: string(id), StubState: inventory.StateActive}
	}
	resolver := mapResolver{"all": devices}

	var current int32
	var peak int32
	tracker := trackingActionExecutor{
		onExecute: func() {
			n := atomic.AddInt32(&current, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&current, -1)
		},
	}

	dag := buildDAG(t, `{
		"id": "bounded",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "all"}}]
	}`)

	x := engine.NewExecutor(resolver, tracker, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), bound)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Nodes)
	}

	if got := atomic.LoadInt32(&peak); got > bound {
		t.Fatalf("expected concurrency to never exceed %d, observed %d", bound, got)
	}
	if got := atomic.LoadInt32(&peak); got < 2 {
		t.Errorf("expected genuine concurrency (peak >= 2) with %d devices and a bound of %d, observed %d", deviceCount, bound, got)
	}
}

// trackingActionExecutor is a test-only ActionExecutor whose onExecute
// hook runs on every call, used to observe how many calls are in flight
// at once.
type trackingActionExecutor struct {
	onExecute func()
}

func (t trackingActionExecutor) Execute(_ context.Context, _ *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	t.onExecute()
	return engine.ActionResult{}, nil
}

// deviceRecordingActionExecutor is a test-only ActionExecutor that reports
// which device (if any) it was actually invoked against, used to prove a
// runtime guard stopped dispatch before the action ran rather than merely
// reporting the outcome as skipped afterward.
type deviceRecordingActionExecutor struct {
	onExecute func(device inventory.InventoryItem)
}

func (d deviceRecordingActionExecutor) Execute(_ context.Context, _ *engine.Task, device inventory.InventoryItem) (engine.ActionResult, error) {
	d.onExecute(device)
	return engine.ActionResult{}, nil
}
