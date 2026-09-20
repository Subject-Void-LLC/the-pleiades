// Package engine_test: tests of check mode in the executor.
package engine_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// checkedMethod counts how often each of a registered method's two
// functions ran, which is the whole assertion most of these tests make: in
// check mode Invoke must run zero times.
type checkedMethod struct {
	name    string
	invokes atomic.Int32
	checks  atomic.Int32
}

// registerCheckedMethod registers a method whose Check reports wouldChange
// and, when recordInverse is set, misbehaves by recording an undo
// instruction. Passing supportsCheck false registers it with no Check at
// all.
func registerCheckedMethod(t *testing.T, suffix string, supportsCheck, wouldChange, recordInverse bool) *checkedMethod {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())

	m := &checkedMethod{name: "enginecheck." + suffix}
	d := collection.Descriptor{
		Name: m.name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			SupportsCheck: supportsCheck,
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			m.invokes.Add(1)
			return collection.Result{Changed: true}, nil
		},
	}
	if supportsCheck {
		d.Check = func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			m.checks.Add(1)
			if err := rc.SetStat("seen", "current-state"); err != nil {
				return collection.Result{}, err
			}
			if recordInverse {
				if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: "noop", Description: "undo a change that never happened"}); err != nil {
					return collection.Result{}, err
				}
			}
			return collection.Result{Changed: wouldChange}, nil
		}
	}
	if err := collection.Register(d); err != nil {
		t.Fatalf("registering %s: %v", m.name, err)
	}
	return m
}

// recordingJournal counts Record calls, so a test can prove a check wrote
// nothing to the run journal.
type recordingJournal struct {
	records atomic.Int32
}

func (j *recordingJournal) Record(context.Context, []engine.JournalEntry) error {
	j.records.Add(1)
	return nil
}

// newCheckExecutor builds the real executor chain cmd/pleiades builds,
// minus the transport layer: a Collection bridge over the builtin keywords.
func newCheckExecutor(resolver engine.TargetResolver, opts ...engine.ExecutorOption) *engine.Executor {
	actions := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext)
	return engine.NewExecutor(resolver, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
}

// nodeByID returns the one result for nodeID, failing the test if there is
// not exactly one.
func nodeByID(t *testing.T, result engine.RunResult, nodeID string) engine.NodeResult {
	t.Helper()
	var found []engine.NodeResult
	for _, n := range result.Nodes {
		if n.NodeID == nodeID {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one result for %s, got %d: %+v", nodeID, len(found), result.Nodes)
	}
	return found[0]
}

// TestCheckMode_RunsCheckNeverInvoke is the core claim: a check reaches a
// method's Check, never its Invoke, and reports what Check predicted.
func TestCheckMode_RunsCheckNeverInvoke(t *testing.T) {
	m := registerCheckedMethod(t, "core", true, true, false)
	dag := buildDAG(t, `{"id": "check-core", "tasks": [{"name": "t", "fqcn": "`+m.name+`", "register": "r"}]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Mode != collection.ModeCheck {
		t.Errorf("RunResult.Mode = %q, want %q", result.Mode, collection.ModeCheck)
	}
	if got := m.invokes.Load(); got != 0 {
		t.Fatalf("Invoke ran %d time(s) during a check; a check must never reach it", got)
	}
	if got := m.checks.Load(); got != 1 {
		t.Fatalf("Check ran %d time(s), want 1", got)
	}

	node := nodeByID(t, result, "tasks[0]")
	if node.Err != nil || node.Skipped || node.Unchecked {
		t.Fatalf("expected a clean checked result, got %+v", node)
	}
	if !node.Changed {
		t.Error("Changed did not carry Check's prediction that a real run would change something")
	}
	if node.Stats["seen"] != "current-state" {
		t.Errorf("Stats = %v, want what Check recorded", node.Stats)
	}
}

// TestCheckMode_ExecuteModeStillInvokes is the control for the test above:
// the same method, the same runbook, in the default mode, runs Invoke.
// Without it, a Check wired to both modes would pass the check test.
func TestCheckMode_ExecuteModeStillInvokes(t *testing.T) {
	m := registerCheckedMethod(t, "control", true, false, false)
	dag := buildDAG(t, `{"id": "check-control", "tasks": [{"name": "t", "fqcn": "`+m.name+`"}]}`)

	result, err := newCheckExecutor(mapResolver{}).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Mode != collection.ModeExecute {
		t.Errorf("RunResult.Mode = %q, want the default %q", result.Mode, collection.ModeExecute)
	}
	if m.invokes.Load() != 1 || m.checks.Load() != 0 {
		t.Fatalf("execute mode ran Invoke %d and Check %d time(s), want 1 and 0", m.invokes.Load(), m.checks.Load())
	}
}

// TestCheckMode_AnUncheckedMethodGivesItsOwnReason covers a method with no
// check that says why (Manifest.NoCheckReason): the unchecked line and
// validation's plan-time answer both carry its reason in place of the
// bare "does not declare check support".
func TestCheckMode_AnUncheckedMethodGivesItsOwnReason(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	const name, reason = "enginecheck.reasoned", "what it changes is decided by the device when it runs"
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, NoCheckReason: reason,
		},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			t.Error("a method with no check ran for real during a check")
			return collection.Result{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	dag := buildDAG(t, `{"id": "check-reasoned", "tasks": [{"name": "a", "fqcn": "`+name+`"}]}`)
	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := nodeByID(t, result, "tasks[0]"); !n.Unchecked || n.SkipReason != name+" cannot be checked: "+reason {
		t.Errorf("the unchecked task says %q (unchecked %v), want the method's own reason", n.SkipReason, n.Unchecked)
	}
	if ok, why := engine.Checkable(name, nil); ok || why != name+" declares no check support: "+reason {
		t.Errorf("Checkable = %v, %q, want false with the method's own reason", ok, why)
	}
}

// TestCheckMode_UncheckedIsNamedAndTheWalkContinues proves a method with
// no check support is reported by name, is never run, is not a failure,
// and does not stop a later task from being checked.
func TestCheckMode_UncheckedIsNamedAndTheWalkContinues(t *testing.T) {
	cannot := registerCheckedMethod(t, "cannot", false, false, false)
	can := registerCheckedMethod(t, "can", true, false, false)
	dag := buildDAG(t, `{"id": "check-mixed", "tasks": [
		{"name": "first", "fqcn": "`+cannot.name+`"},
		{"name": "second", "fqcn": "`+can.name+`"}
	]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("an unchecked task is not a failure, got %+v", result.Nodes)
	}

	first := nodeByID(t, result, "tasks[0]")
	if !first.Unchecked || !first.Skipped {
		t.Fatalf("expected the first task to be reported unchecked, got %+v", first)
	}
	if !strings.Contains(first.SkipReason, cannot.name) || !strings.Contains(first.SkipReason, "does not declare check support") {
		t.Errorf("SkipReason %q must name the method and say why", first.SkipReason)
	}
	if first.Changed {
		t.Error("an unchecked task reported a change; it must never be counted as an answer")
	}
	if cannot.invokes.Load() != 0 {
		t.Fatal("the method with no check support was invoked for real during a check")
	}

	if can.checks.Load() != 1 {
		t.Fatalf("the task after the unchecked one was checked %d time(s), want 1: the walk must carry on", can.checks.Load())
	}
}

// TestCheckMode_RefusesACheckThatRecordsAnInverse proves the engine, not
// the method, enforces that a check leaves no undo instruction behind.
func TestCheckMode_RefusesACheckThatRecordsAnInverse(t *testing.T) {
	m := registerCheckedMethod(t, "records-inverse", true, true, true)
	dag := buildDAG(t, `{"id": "check-inverse", "tasks": [{"name": "t", "fqcn": "`+m.name+`"}]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	node := nodeByID(t, result, "tasks[0]")
	if node.Err == nil {
		t.Fatalf("a check that recorded an undo instruction was accepted: %+v", node)
	}
	if !strings.Contains(node.Err.Error(), "undo instruction") {
		t.Errorf("error %q does not say what was refused", node.Err)
	}
	if node.Stats != nil {
		t.Errorf("the refused result's stats still reached the caller: %v", node.Stats)
	}
}

// TestCheckMode_WritesNoJournal proves a check never reaches the run
// journal, with the execute run as its control.
func TestCheckMode_WritesNoJournal(t *testing.T) {
	m := registerCheckedMethod(t, "journal", true, true, false)
	dag := buildDAG(t, `{"id": "check-journal", "tasks": [{"name": "t", "fqcn": "`+m.name+`"}]}`)

	checked := &recordingJournal{}
	if _, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck), engine.WithJournal(checked)).Run(context.Background(), dag); err != nil {
		t.Fatalf("check Run: %v", err)
	}
	if got := checked.records.Load(); got != 0 {
		t.Fatalf("a check wrote %d journal level(s); it must write none", got)
	}

	executed := &recordingJournal{}
	if _, err := newCheckExecutor(mapResolver{}, engine.WithJournal(executed)).Run(context.Background(), dag); err != nil {
		t.Fatalf("execute Run: %v", err)
	}
	if executed.records.Load() == 0 {
		t.Fatal("the execute control wrote no journal, so the check assertion above proves nothing")
	}
}

// TestCheckMode_AdmitsSimulateLockedDevices proves PLAN.md Section 9's
// lock in both directions: a simulate-locked device is checked, and is
// still never run against for real.
func TestCheckMode_AdmitsSimulateLockedDevices(t *testing.T) {
	m := registerCheckedMethod(t, "locked", true, true, false)
	locked := &inventorytest.Stub{StubID: "new-device", StubName: "new-device", StubState: inventory.StateSimulateLocked}
	quarantined := &inventorytest.Stub{StubID: "held-device", StubName: "held-device", StubState: inventory.StateQuarantined}
	resolver := mapResolver{"fleet": {locked, quarantined}}
	dag := buildDAG(t, `{"id": "check-locked", "tasks": [{"name": "t", "fqcn": "`+m.name+`", "params": {"target": "fleet"}}]}`)

	result, err := newCheckExecutor(resolver, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("check Run: %v", err)
	}
	byDevice := map[string]engine.NodeResult{}
	for _, n := range result.Nodes {
		byDevice[n.Device] = n
	}
	if n := byDevice["new-device"]; n.Skipped || n.Err != nil {
		t.Errorf("a check skipped the simulate-locked device, which exists to be checked: %+v", n)
	}
	// The exception is simulate-locked alone; quarantine still holds.
	if n := byDevice["held-device"]; !n.Skipped || n.Unchecked {
		t.Errorf("a check reached a quarantined device: %+v", n)
	}
	if m.checks.Load() != 1 {
		t.Errorf("Check ran %d time(s), want exactly once, for the simulate-locked device", m.checks.Load())
	}

	result, err = newCheckExecutor(resolver).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("execute Run: %v", err)
	}
	for _, n := range result.Nodes {
		if n.Device == "new-device" && !n.Skipped {
			t.Fatalf("an execute run reached a simulate-locked device: %+v", n)
		}
	}
	if m.invokes.Load() != 0 {
		t.Fatal("Invoke ran against a simulate-locked device: the lock was escalated")
	}
}

// TestCheckMode_UnevaluableConditionIsUnchecked proves a condition that
// reads the result of an unchecked task is reported as a gap rather than
// failing the run, and that the same runbook still fails in execute mode
// where the condition's input really should have existed.
func TestCheckMode_UnevaluableConditionIsUnchecked(t *testing.T) {
	cannot := registerCheckedMethod(t, "upstream", false, false, false)
	dag := buildDAG(t, `{"id": "check-condition", "tasks": [
		{"name": "produce", "fqcn": "`+cannot.name+`", "register": "out"},
		{"name": "consume", "fqcn": "noop", "when_cel": "stat.out[\"\"].missing_key == 'x'"}
	]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("a condition that could not be evaluated in a check failed the run: %+v", result.Nodes)
	}
	consume := nodeByID(t, result, "tasks[1]")
	if !consume.Unchecked || !strings.Contains(consume.SkipReason, "depends on the result of a task that could not be checked") {
		t.Fatalf("expected the dependent task reported unchecked with its reason, got %+v", consume)
	}
}

// TestCheckMode_UnknownModeRunsNothing proves a mode outside the closed set
// is never treated as execute.
func TestCheckMode_UnknownModeRunsNothing(t *testing.T) {
	m := registerCheckedMethod(t, "unknown-mode", true, true, false)
	dag := buildDAG(t, `{"id": "check-unknown", "tasks": [{"name": "t", "fqcn": "`+m.name+`"}]}`)

	journal := &recordingJournal{}
	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.Mode("simulate")), engine.WithJournal(journal)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	node := nodeByID(t, result, "tasks[0]")
	if node.Err == nil || !strings.Contains(node.Err.Error(), "unknown execution mode") {
		t.Fatalf("expected the node to fail naming the mode, got %+v", node)
	}
	if m.invokes.Load() != 0 || m.checks.Load() != 0 {
		t.Fatalf("an unknown mode ran Invoke %d and Check %d time(s), want neither", m.invokes.Load(), m.checks.Load())
	}
	if journal.records.Load() != 0 {
		t.Error("a run that executed nothing wrote a journal entry")
	}
}

// TestCheckMode_ExecutorWithoutCheckSupport proves an ActionExecutor that
// never implemented CheckExecutor gets every task reported unchecked,
// rather than having Execute called on it.
func TestCheckMode_ExecutorWithoutCheckSupport(t *testing.T) {
	var executed atomic.Int32
	actions := deviceRecordingActionExecutor{onExecute: func(inventory.InventoryItem) { executed.Add(1) }}
	dag := buildDAG(t, `{"id": "check-plain", "tasks": [{"name": "t", "fqcn": "noop"}]}`)

	x := engine.NewExecutor(mapResolver{}, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithMode(collection.ModeCheck))
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executed.Load() != 0 {
		t.Fatal("an executor with no check support was asked to Execute during a check")
	}
	if node := nodeByID(t, result, "tasks[0]"); !node.Unchecked {
		t.Fatalf("expected the task reported unchecked, got %+v", node)
	}
}

// TestCheckMode_BuiltinKeywordsAreChecked proves the two engine keywords,
// which never reach a device, answer a check by running as themselves.
func TestCheckMode_BuiltinKeywordsAreChecked(t *testing.T) {
	dag := buildDAG(t, `{"id": "check-keywords", "tasks": [
		{"name": "n", "fqcn": "noop", "params": {"changed": true}},
		{"name": "m", "fqcn": "set_metadata", "register": "meta", "params": {"data": {"k": "v"}}}
	]}`)

	result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := nodeByID(t, result, "tasks[0]"); n.Unchecked || !n.Changed {
		t.Errorf("noop with changed: true should check as 'would change', got %+v", n)
	}
	if n := nodeByID(t, result, "tasks[1]"); n.Unchecked || n.Err != nil {
		t.Errorf("set_metadata should check cleanly, got %+v", n)
	}
}
