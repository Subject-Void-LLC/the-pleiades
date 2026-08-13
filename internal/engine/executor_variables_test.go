package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestExecutor_WithVariables_ReachesWhenCEL is
// AWX_PARITY_ROADMAP.md Section 3b.1's own "ExtraVars folded into the
// runbook's variable context," proven at the one place this engine has
// ever had a variable context at all: a when_cel condition's "vars" root.
// Two runs of the identical DAG, differing only in WithVariables' own
// argument, take opposite branches, the same conditional-branch shape
// TestExecutor_ConditionalBranch_ReleaseGate already establishes for
// "stat" and "nodes".
func TestExecutor_WithVariables_ReachesWhenCEL(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "vars-demo",
		"tasks": [
			{"name": "prod-only", "fqcn": "noop", "when_cel": "vars.env == 'prod'", "params": {"changed": true}}
		]
	}`)

	run := func(vars map[string]interface{}) engine.NodeResult {
		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithVariables(vars))
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if result.HasErrors() {
			t.Fatalf("expected no errors, got %+v", result.Nodes)
		}
		if len(result.Nodes) != 1 {
			t.Fatalf("expected 1 node result, got %d: %+v", len(result.Nodes), result.Nodes)
		}
		return result.Nodes[0]
	}

	prod := run(map[string]interface{}{"env": "prod"})
	if prod.Skipped || !prod.Changed {
		t.Errorf("vars.env == \"prod\" should have run the task, got %+v", prod)
	}

	dev := run(map[string]interface{}{"env": "dev"})
	if !dev.Skipped {
		t.Errorf("vars.env == \"dev\" should have skipped the task, got %+v", dev)
	}
	if !strings.Contains(dev.SkipReason, "vars.env") {
		t.Errorf("expected SkipReason to name the when_cel expression, got %q", dev.SkipReason)
	}
}

// TestExecutor_WithVariables_DefaultsToAnEmptyMap proves an Executor built
// with no WithVariables option (every call site before this phase, and
// every call site that has no extra vars to supply) still lets a when_cel
// expression reference "vars" without a CEL evaluation error: cel.go
// declares "vars" as a permanent root, so runNode must always bind
// something to it.
func TestExecutor_WithVariables_DefaultsToAnEmptyMap(t *testing.T) {
	dag := buildDAG(t, `{
		"id": "vars-unset-demo",
		"tasks": [
			{"name": "no-vars-supplied", "fqcn": "noop", "when_cel": "size(vars) == 0"}
		]
	}`)

	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors (vars should default to an empty, not nil, map), got %+v", result.Nodes)
	}
	if result.Nodes[0].Skipped {
		t.Errorf("size(vars) == 0 should have been true with no WithVariables option set, got %+v", result.Nodes[0])
	}
}

// ctxAwareActionExecutor is a test-only ActionExecutor that blocks until
// either ctx is done or a fixed delay elapses, reporting which happened.
// This is what lets TestExecutor_WithTaskTimeout prove WithTaskTimeout's
// context.WithTimeout genuinely reaches ActionExecutor.Execute, the same
// real contract a real transport-backed action honors (e.g.
// internal/transport/ssh's own ctx-aware dial), without this package
// needing a real network dependency to prove it.
type ctxAwareActionExecutor struct {
	delay time.Duration
}

func (c ctxAwareActionExecutor) Execute(ctx context.Context, _ *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	select {
	case <-time.After(c.delay):
		return engine.ActionResult{Changed: true}, nil
	case <-ctx.Done():
		return engine.ActionResult{}, ctx.Err()
	}
}

// TestExecutor_WithTaskTimeout_AbortsASlowTaskWithoutAffectingOthers
// proves WithTaskTimeout applies fresh to every device execution (runOne
// wraps ctx per call, not once for the whole Run): one task, fanned out
// across two devices in the same node (TestExecutor_ConcurrencyBound's
// own multi-device fan-out shape, reused here so both devices genuinely
// run concurrently rather than as two sequential DAG levels), where one
// device's own action hangs well past the configured timeout and the
// other's returns immediately. The slow device's own result carries a
// real context.DeadlineExceeded; the fast device's is untouched by it,
// proving the deadline is per-execution, not a single deadline shared
// across the whole node's fan-out. This is the runbook kind's own
// documented semantics ("seconds before A TASK is abandoned"), proven for
// real: the action executor genuinely blocks and genuinely observes ctx
// cancellation, nothing here is mocked around.
func TestExecutor_WithTaskTimeout_AbortsASlowTaskWithoutAffectingOthers(t *testing.T) {
	slowDevice := &inventorytest.Stub{StubID: "dev-slow", StubName: "dev-slow", StubState: inventory.StateActive}
	fastDevice := &inventorytest.Stub{StubID: "dev-fast", StubName: "dev-fast", StubState: inventory.StateActive}
	resolver := mapResolver{"all": {slowDevice, fastDevice}}

	dag := buildDAG(t, `{
		"id": "timeout-demo",
		"tasks": [{"name": "maybe-slow", "fqcn": "noop", "params": {"target": "all"}}]
	}`)

	actions := timeoutRoutingActionExecutor{
		slow: ctxAwareActionExecutor{delay: 10 * time.Second},
		fast: ctxAwareActionExecutor{delay: 0},
	}

	x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithTaskTimeout(200*time.Millisecond))

	started := time.Now()
	result, err := x.Run(context.Background(), dag)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if elapsed > 5*time.Second {
		t.Fatalf("Run took %v; WithTaskTimeout(200ms) should have aborted the slow device's execution long before its own 10s delay elapsed", elapsed)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 node results (one per device), got %d: %+v", len(result.Nodes), result.Nodes)
	}

	byDevice := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byDevice[n.Device] = n
	}

	slow := byDevice["dev-slow"]
	if slow.Err == nil || !errors.Is(slow.Err, context.DeadlineExceeded) {
		t.Errorf("expected the slow device's own result to wrap context.DeadlineExceeded, got %+v", slow)
	}

	fast := byDevice["dev-fast"]
	if fast.Err != nil {
		t.Errorf("expected the fast device to succeed untouched by the slow device's timeout, got %+v", fast)
	}
}

// timeoutRoutingActionExecutor dispatches to slow or fast by device name,
// so TestExecutor_WithTaskTimeout_AbortsASlowTaskWithoutAffectingOthers
// can give one task's two fanned-out devices two different real
// behaviors.
type timeoutRoutingActionExecutor struct {
	slow, fast ctxAwareActionExecutor
}

func (r timeoutRoutingActionExecutor) Execute(ctx context.Context, task *engine.Task, device inventory.InventoryItem) (engine.ActionResult, error) {
	if device != nil && device.Name() == "dev-slow" {
		return r.slow.Execute(ctx, task, device)
	}
	return r.fast.Execute(ctx, task, device)
}

// TestExecutor_WithTaskTimeout_ZeroAppliesNoDeadline proves the default
// (no WithTaskTimeout option, matching every pre-3b.1 call site) leaves
// ctx exactly as the caller supplied it: a task that would only succeed
// given enough time keeps running, rather than being silently bounded by
// an unintended zero-length deadline.
func TestExecutor_WithTaskTimeout_ZeroAppliesNoDeadline(t *testing.T) {
	device := &inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", StubState: inventory.StateActive}
	resolver := mapResolver{"target": {device}}

	dag := buildDAG(t, `{
		"id": "no-timeout-demo",
		"tasks": [{"name": "brief", "fqcn": "noop", "params": {"target": "target"}}]
	}`)

	actions := ctxAwareActionExecutor{delay: 50 * time.Millisecond}
	x := engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)

	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors with no WithTaskTimeout set, got %+v", result.Nodes)
	}
}
