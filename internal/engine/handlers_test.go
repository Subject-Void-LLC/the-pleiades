// Tests that a block's rescue: and always: run, per device, through the
// real Executor: which tasks run on which device, what the run reports, and
// that a runbook without either stops at its first failure as it always did.
package engine_test

import (
	"context"
	"errors"
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// scripted is an ActionExecutor that fails the tasks fail names (task name
// "@" device name), holds the ones hold names for a person's decision, and
// records, in order, every task it ran and the params it ran with.
type scripted struct {
	fail   map[string]bool
	hold   map[string]bool
	mu     sync.Mutex
	ran    []string
	params map[string]map[string]any
}

func (s *scripted) Execute(_ context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	key := task.Name + "@"
	if d != nil {
		key += d.Name()
	}
	s.mu.Lock()
	s.ran = append(s.ran, key)
	if s.params == nil {
		s.params = map[string]map[string]any{}
	}
	s.params[key] = task.Params
	s.mu.Unlock()
	if s.hold[key] {
		return engine.ActionResult{}, &sdk.HeldError{Key: key, Reason: "a person decides"}
	}
	if s.fail[key] {
		return engine.ActionResult{}, errors.New("scripted failure of " + key)
	}
	return engine.ActionResult{}, nil
}

// ranSorted is every task the executor ran, sorted, for an order-free
// comparison where devices run side by side.
func (s *scripted) ranSorted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.ran)
	slices.Sort(out)
	return out
}

// twoDevices resolves hosts "web" to web1 and web2.
func twoDevices() mapResolver {
	return mapResolver{"web": {
		&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "web2", StubName: "web2", StubState: inventory.StateActive},
	}}
}

// runScripted runs runbook on web1 and web2 with actions.
func runScripted(t *testing.T, runbook string, actions *scripted) engine.RunResult {
	t.Helper()
	x := engine.NewExecutor(twoDevices(), actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// resultOf returns the result of task node on device, or false.
func resultOf(r engine.RunResult, node, device string) (engine.NodeResult, bool) {
	for _, n := range r.Nodes {
		if n.NodeID == node && n.Device == device {
			return n, true
		}
	}
	return engine.NodeResult{}, false
}

// guarded is a block over web whose second task fails where a test says,
// with a rescue, an always and a task after it.
const guarded = `id: g
hosts: web
tasks:
  - name: guarded
    block:
      - name: b0
        noop: {}
      - name: b1
        noop: {}
      - name: b2
        noop: {}
    rescue:
      - name: r0
        noop: {}
      - name: r1
        noop: {}
    always:
      - name: a0
        noop: {}
  - name: after
    noop: {}
`

// TestRescue_AnswersAFailureOnTheDeviceItHappenedOn: b1 fails on web1 only.
// web1 skips b2 and runs the rescue; web2 runs the block to its end and no
// rescue; both run always and the task after the block; the run has no
// error, says it was rescued, and names the rescue on web1's b1.
func TestRescue_AnswersAFailureOnTheDeviceItHappenedOn(t *testing.T) {
	actions := &scripted{fail: map[string]bool{"b1@web1": true}}
	result := runScripted(t, guarded, actions)
	want := []string{"a0@web1", "a0@web2", "after@web1", "after@web2", "b0@web1", "b0@web2", "b1@web1", "b1@web2", "b2@web2", "r0@web1", "r1@web1"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
	failed, _ := resultOf(result, "tasks[0].block[1]", "web1")
	if failed.Err == nil || failed.RescuedBy != "tasks[0].rescue[0]" {
		t.Errorf("web1's b1: %+v", failed)
	}
	for node, device := range map[string]string{"tasks[0].block[2]": "web1", "tasks[0].rescue[0]": "web2"} {
		skipped, ok := resultOf(result, node, device)
		if !ok || !skipped.Skipped || skipped.SkipReason == "" {
			t.Errorf("%s on %s: %+v", node, device, skipped)
		}
	}
}

// TestAlways_RunsAfterAFailureAndTheRunStillFails: with no rescue, a failure
// runs the block's always on every device and then ends the run, before the
// task after the block.
func TestAlways_RunsAfterAFailureAndTheRunStillFails(t *testing.T) {
	runbook := strings.Replace(guarded, "    rescue:\n      - name: r0\n        noop: {}\n      - name: r1\n        noop: {}\n", "", 1)
	actions := &scripted{fail: map[string]bool{"b1@web1": true}}
	result := runScripted(t, runbook, actions)
	want := []string{"a0@web1", "a0@web2", "b0@web1", "b0@web2", "b1@web1", "b1@web2", "b2@web2"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if !result.HasErrors() || result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
}

// TestRescue_ThatFailsStillRunsAlwaysAndKeepsBothFailures: the rescue's
// first task fails too, so its second does not run, always does, and the
// run fails carrying both failures, the block's first.
func TestRescue_ThatFailsStillRunsAlwaysAndKeepsBothFailures(t *testing.T) {
	actions := &scripted{fail: map[string]bool{"b1@web1": true, "r0@web1": true}}
	result := runScripted(t, guarded, actions)
	want := []string{"a0@web1", "a0@web2", "b0@web1", "b0@web2", "b1@web1", "b1@web2", "b2@web2", "r0@web1"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if !result.HasErrors() || result.Rescued() {
		t.Fatalf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
	var failed []string
	for _, n := range result.Nodes {
		if n.Err != nil {
			failed = append(failed, n.NodeID+"@"+n.Device)
		}
	}
	if !slices.Equal(failed, []string{"tasks[0].block[1]@web1", "tasks[0].rescue[0]@web1"}) {
		t.Errorf("failures %v, want the block's then the rescue's", failed)
	}
}

// TestPlainBlock_StopsAtItsFirstFailureAsBefore: a block with no rescue and
// no always changes nothing: the first failure ends the run at that level,
// on every device.
func TestPlainBlock_StopsAtItsFirstFailureAsBefore(t *testing.T) {
	runbook := `id: p
hosts: web
tasks:
  - name: plain
    block:
      - name: b0
        noop: {}
      - name: b1
        noop: {}
  - name: after
    noop: {}
`
	actions := &scripted{fail: map[string]bool{"b0@web1": true}}
	result := runScripted(t, runbook, actions)
	if got := actions.ranSorted(); !slices.Equal(got, []string{"b0@web1", "b0@web2"}) {
		t.Fatalf("ran %v, want only the level that failed", got)
	}
	if !result.HasErrors() {
		t.Error("the run has no error")
	}
}

// TestNestedBlock_FailsIntoTheEnclosingRescue: an inner block with no
// handlers of its own fails; the rest of it and of the outer block is
// skipped on that device, and the outer rescue answers it.
func TestNestedBlock_FailsIntoTheEnclosingRescue(t *testing.T) {
	runbook := `id: n
hosts: web
tasks:
  - name: outer
    block:
      - name: inner
        block:
          - name: i0
            noop: {}
          - name: i1
            noop: {}
      - name: o1
        noop: {}
    rescue:
      - name: r0
        noop: {}
`
	actions := &scripted{fail: map[string]bool{"i0@web1": true}}
	result := runScripted(t, runbook, actions)
	want := []string{"i0@web1", "i0@web2", "i1@web2", "o1@web2", "r0@web1"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
}

// TestRescue_ReadsItsOwnDevicesFailureAsError: both devices fail at
// different tasks, and each device's rescue renders its own failure.
func TestRescue_ReadsItsOwnDevicesFailureAsError(t *testing.T) {
	runbook := `id: e
hosts: web
tasks:
  - name: guarded
    block:
      - name: b0
        noop: {}
      - name: b1
        noop: {}
    rescue:
      - name: r0
        noop:
          failed_task: "{{ error.task }}"
          said: "{{ error.message }}"
`
	actions := &scripted{fail: map[string]bool{"b0@web1": true, "b1@web2": true}}
	result := runScripted(t, runbook, actions)
	if result.HasErrors() {
		t.Fatalf("the run failed: %+v", result.Nodes)
	}
	for device, task := range map[string]string{"web1": "tasks[0].block[0]", "web2": "tasks[0].block[1]"} {
		p := actions.params["r0@"+device]
		if p["failed_task"] != task || !strings.Contains(p["said"].(string), "scripted failure of") {
			t.Errorf("r0 on %s rendered %v, want error.task %s", device, p, task)
		}
	}
}

// TestRescue_IsNotPredictedInACheck: in a check, a rescue task is
// conditional_skip, not run and not a gap, and always is checked.
func TestRescue_IsNotPredictedInACheck(t *testing.T) {
	x := engine.NewExecutor(twoDevices(), engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(), 0, engine.WithMode("check"))
	result, err := x.Run(context.Background(), yamlRunbook(t, guarded))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, n := range result.Nodes {
		inRescue := strings.Contains(n.NodeID, ".rescue[")
		switch {
		case inRescue && (!n.Conditional || n.Unchecked || !n.Skipped):
			t.Errorf("rescue %s: %+v", n.NodeID, n)
		case !inRescue && (n.Conditional || n.Err != nil):
			t.Errorf("%s: %+v", n.NodeID, n)
		}
	}
	if _, ok := resultOf(result, "tasks[0].always[0]", "web1"); !ok {
		t.Error("always was not checked")
	}
}

// TestHandlers_JournalWhatEachDeviceSkipped: a task a device's failure
// kept from running, and a rescue on a device that did not fail, are each
// journaled as skipped with a kind of their own, so the run's record says
// why each did not run there.
func TestHandlers_JournalWhatEachDeviceSkipped(t *testing.T) {
	journal := &contextCheckingJournal{}
	actions := &scripted{fail: map[string]bool{"b1@web1": true}}
	x := engine.NewExecutor(twoDevices(), actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()), engine.WithJournal(journal))
	if _, err := x.Run(context.Background(), yamlRunbook(t, guarded)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[string]engine.SkipKind{
		"tasks[0].block[2]@web1":  engine.SkipKindBlockFailed,
		"tasks[0].rescue[0]@web2": engine.SkipKindNotFailed,
		"tasks[0].rescue[1]@web2": engine.SkipKindNotFailed,
	}
	for _, e := range journal.entries {
		key := e.NodeID + "@" + e.DeviceID
		if kind, ok := want[key]; ok {
			if e.Outcome != engine.OutcomeSkipped || e.SkipKind != kind {
				t.Errorf("%s journaled %s/%s, want skipped/%s", key, e.Outcome, e.SkipKind, kind)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Errorf("not journaled: %v (of %d entries)", want, len(journal.entries))
	}
	// On web1 the failed task, its rescue and the always are journaled in
	// the order they ran, which is the order a rollback undoes newest first:
	// always, then rescue, then the block (internal/rollback's plan).
	seq := map[string]int{}
	for _, e := range journal.entries {
		if e.DeviceID == "web1" {
			seq[e.NodeID] = e.Sequence
		}
	}
	if !(seq["tasks[0].block[1]"] < seq["tasks[0].rescue[0]"] && seq["tasks[0].rescue[0]"] < seq["tasks[0].rescue[1]"] &&
		seq["tasks[0].rescue[1]"] < seq["tasks[0].always[0]"]) {
		t.Errorf("web1's journal order: %v", seq)
	}
}

// TestGuardedBlock_RunsOnTheRunbooksHostsOnly: a task inside a block with a
// rescue: or an always: that names a target of its own is refused when the
// runbook builds, since its device's handling could not include it; inside a
// plain block it is not.
func TestGuardedBlock_RunsOnTheRunbooksHostsOnly(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, handler string
		refused       bool
	}{
		{"with a rescue", "    rescue:\n      - name: r\n        noop: {}\n", true},
		{"with an always", "    always:\n      - name: a\n        noop: {}\n", true},
		{"a plain block", "", false},
	} {
		src := "id: t\nhosts: web\ntasks:\n  - name: g\n    block:\n      - name: elsewhere\n        noop:\n          target: db\n" + tc.handler
		_, err := engine.NewBuilder(eval).BuildFromYAML([]byte(src))
		if refused := err != nil && strings.Contains(err.Error(), "runs somewhere other than the runbook's hosts"); refused != tc.refused {
			t.Errorf("%s: err = %v, want refused %v", tc.name, err, tc.refused)
		}
	}
}

// TestHeld_RunsAlwaysButNoRescue: a failure held for a person's decision is
// not a failure a rescue answers: the rescue does not run, always does, and
// the run ends with the held task's error.
func TestHeld_RunsAlwaysButNoRescue(t *testing.T) {
	actions := &scripted{hold: map[string]bool{"b1@web1": true}}
	result := runScripted(t, guarded, actions)
	want := []string{"a0@web1", "a0@web2", "b0@web1", "b0@web2", "b1@web1", "b1@web2", "b2@web2"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if !result.HasErrors() || result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v", result.HasErrors(), result.Rescued())
	}
	if r, _ := resultOf(result, "tasks[0].rescue[0]", "web1"); !r.Skipped || !strings.Contains(r.SkipReason, "held") {
		t.Errorf("web1's rescue: %+v", r)
	}
}

// TestAlways_AFailureSkipsTheRestOfIt: a failure inside always: skips the
// rest of that always on that device and ends the run; the other device
// runs it all.
func TestAlways_AFailureSkipsTheRestOfIt(t *testing.T) {
	runbook := `id: a
hosts: web
tasks:
  - name: guarded
    block:
      - name: b0
        noop: {}
    always:
      - name: a0
        noop: {}
      - name: a1
        noop: {}
  - name: after
    noop: {}
`
	actions := &scripted{fail: map[string]bool{"a0@web1": true}}
	result := runScripted(t, runbook, actions)
	want := []string{"a0@web1", "a0@web2", "a1@web2", "b0@web1", "b0@web2"}
	if got := actions.ranSorted(); !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v", got, want)
	}
	if !result.HasErrors() {
		t.Error("the run has no error")
	}
}

// TestRescue_OnATaskWithNoDevice: a runbook with no hosts runs every task
// once with no device, and its block's handling is that one run's.
func TestRescue_OnATaskWithNoDevice(t *testing.T) {
	runbook := `id: c
tasks:
  - name: guarded
    block:
      - name: b0
        noop: {}
      - name: b1
        noop: {}
    rescue:
      - name: r0
        noop:
          failed: "{{ error.task }}"
`
	actions := &scripted{fail: map[string]bool{"b0@": true}}
	result := runScripted(t, runbook, actions)
	if got := actions.ranSorted(); !slices.Equal(got, []string{"b0@", "r0@"}) {
		t.Fatalf("ran %v", got)
	}
	if result.HasErrors() || !result.Rescued() || actions.params["r0@"]["failed"] != "tasks[0].block[0]" {
		t.Errorf("HasErrors %v, Rescued %v, r0 params %v", result.HasErrors(), result.Rescued(), actions.params["r0@"])
	}
}

// TestConditionRoots: which roots a condition names, and which registers it
// tests for with has(), read from the parsed CEL.
func TestConditionRoots(t *testing.T) {
	c := engine.Conditional{WhenCEL: "has(result.probe) && error.unreached && nodes.other[''].ok"}
	if uses, err := engine.ConditionUsesRoot(c, "error"); err != nil || !uses {
		t.Errorf("error: %v %v", uses, err)
	}
	if uses, err := engine.ConditionUsesRoot(engine.Conditional{When: []string{"stat.x.ok"}}, "error"); err != nil || uses {
		t.Errorf("no error root: %v %v", uses, err)
	}
	tested, err := engine.ConditionTestsPresence(c)
	if err != nil || !tested["probe"] || tested["other"] {
		t.Errorf("tested %v %v", tested, err)
	}
	bad := engine.Conditional{WhenCEL: "has("}
	if _, err := engine.ConditionUsesRoot(bad, "error"); err == nil {
		t.Error("an unparsable condition was read")
	}
	if _, err := engine.ConditionTestsPresence(bad); err == nil {
		t.Error("an unparsable condition was read")
	}
}

// TestEndingRun_StillRunsTheAlwaysItIsInside: a failure of the task itself
// (its parameters fail to render) ends the run whatever encloses it, and
// the run still runs the always: of the block it is inside, on every
// device, before it stops, and nothing after the block.
func TestEndingRun_StillRunsTheAlwaysItIsInside(t *testing.T) {
	runbook := `id: r
hosts: web
tasks:
  - name: guarded
    block:
      - name: b0
        noop:
          x: "{{ vars.missing }}"
      - name: b1
        noop: {}
    rescue:
      - name: r0
        noop: {}
    always:
      - name: a0
        noop: {}
  - name: after
    noop: {}
`
	actions := &scripted{}
	result := runScripted(t, runbook, actions)
	if got := actions.ranSorted(); !slices.Equal(got, []string{"a0@web1", "a0@web2"}) {
		t.Fatalf("ran %v, want only the always, on both devices", got)
	}
	if !result.HasErrors() {
		t.Error("the run has no error")
	}
}
