// Tests of loops and retries (Phases 88 and 89) through the real Executor:
// a loop runs its method once per item and registers one result per item, an
// item that fails does not stop the rest, a condition decides each item, and
// an until task is tried again and registers only its last try.
package engine_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// stepper runs every call through fn, counting them, and runs set_fact and
// the other in-process actions as the engine does.
type stepper struct {
	mu      sync.Mutex
	calls   []string
	fn      func(n int, task *engine.Task) (engine.ActionResult, error)
	builtin engine.ActionExecutor
}

// Execute implements engine.ActionExecutor.
func (s *stepper) Execute(ctx context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	if task.FQCN == engine.SetFactName || task.FQCN == "noop" || task.FQCN == engine.FailName {
		return s.builtin.Execute(ctx, task, d)
	}
	s.mu.Lock()
	n := len(s.calls)
	s.calls = append(s.calls, fmt.Sprint(task.Params["note"]))
	s.mu.Unlock()
	return s.fn(n, task)
}

// oneDevice is a resolver with a single device under "web".
func oneDevice() mapResolver {
	return mapResolver{"web": {typedStub{&inventorytest.Stub{StubID: "a", StubName: "a", StubState: inventory.StateActive, Props: map[string]any{}}}}}
}

// runStepper runs runbook with fn as the method behind rbtest.step.
func runStepper(t *testing.T, mode collection.Mode, runbook string, fn func(n int, task *engine.Task) (engine.ActionResult, error)) (*stepper, engine.RunResult, engine.WorkflowContext) {
	t.Helper()
	s := &stepper{fn: fn, builtin: engine.NewBuiltinActionExecutor()}
	wf := engine.NewInProcessWorkflowContext()
	x := engine.NewExecutor(oneDevice(), s, lock.NewInProcessManager(), event.NewInProcessBus(), wf, 0,
		engine.WithRenderer(render.New()), engine.WithMode(mode))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return s, result, wf
}

// echo is a method that succeeds, reporting its note.
func echo(_ int, task *engine.Task) (engine.ActionResult, error) {
	return engine.ActionResult{Stats: map[string]any{"said": task.Params["note"]}}, nil
}

func TestLoop_RunsOncePerItemAndRegistersEach(t *testing.T) {
	s, result, wf := runStepper(t, collection.ModeExecute, `id: l
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item.name }}-{{ loop.index }}"
    loop:
      - {name: a}
      - {name: b}
      - {name: c}
    register: said
`, echo)
	if result.HasErrors() {
		t.Fatalf("%+v", result.Nodes)
	}
	if want := []string{"a-1", "b-2", "c-3"}; !slices.Equal(s.calls, want) {
		t.Fatalf("calls %v, want %v", s.calls, want)
	}
	var iterations []string
	for _, n := range result.Nodes {
		iterations = append(iterations, n.Iteration)
	}
	if want := []string{".item[0]", ".item[1]", ".item[2]"}; !slices.Equal(iterations, want) {
		t.Errorf("iterations %v, want %v", iterations, want)
	}
	tree, _ := wf.Read()
	rows := tree["said"].(map[string]any)["a"].(map[string]any)["results"].([]any)
	if len(rows) != 3 || rows[1].(map[string]any)["said"] != "b-2" || rows[1].(map[string]any)["failed"] != false {
		t.Errorf("register rows %v", rows)
	}
}

func TestLoop_ListComesFromAnEarlierResult(t *testing.T) {
	s, result, _ := runStepper(t, collection.ModeExecute, `id: dyn
hosts: web
tasks:
  - noop:
      names: [x, y]
    register: found
  - rbtest.step:
      note: "{{ item }}"
    loop: "{{ result.found.names }}"
`, echo)
	if result.HasErrors() || !slices.Equal(s.calls, []string{"x", "y"}) {
		t.Fatalf("calls %v, %+v", s.calls, result.Nodes)
	}
}

func TestLoop_AFailingItemDoesNotStopTheRest(t *testing.T) {
	s, result, wf := runStepper(t, collection.ModeExecute, `id: f
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item }}"
    loop: [1, 2, 3]
    register: r
`, func(n int, task *engine.Task) (engine.ActionResult, error) {
		if n == 1 {
			return engine.ActionResult{}, errors.New("item two broke")
		}
		return echo(n, task)
	})
	if len(s.calls) != 3 {
		t.Fatalf("ran %d items, want all 3", len(s.calls))
	}
	if !result.HasErrors() {
		t.Fatal("the task did not fail")
	}
	tree, _ := wf.Read()
	agg := tree["r"].(map[string]any)["a"].(map[string]any)
	if agg["failed"] != true || len(agg["results"].([]any)) != 3 {
		t.Errorf("aggregate %v", agg)
	}
}

func TestLoop_WhenIsDecidedPerItemAndLabelKeepsItemOutOfTheReport(t *testing.T) {
	s, result, _ := runStepper(t, collection.ModeExecute, `id: w
hosts: web
tasks:
  - rbtest.step:
      note: "{{ item.v }}"
    loop:
      - {v: keep, on: true}
      - {v: drop, on: false}
    loop_control:
      loop_var: thing
      label: "row {{ loop.index }}"
    when: ["item.on"]
`, echo)
	if result.HasErrors() || !slices.Equal(s.calls, []string{"keep"}) {
		t.Fatalf("calls %v, %+v", s.calls, result.Nodes)
	}
	if result.Nodes[0].Label != "row 1" || !result.Nodes[1].Skipped {
		t.Errorf("nodes %+v", result.Nodes)
	}
}

func TestUntil_TriesAgainAndRegistersOnlyTheLastTry(t *testing.T) {
	s, result, wf := runStepper(t, collection.ModeExecute, `id: u
hosts: web
tasks:
  - rbtest.step:
      note: wait
    register: up
    until: ["result.up.n >= 3"]
    retries: 5
    delay: 0
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"n": n + 1}}, nil
	})
	if result.HasErrors() || len(s.calls) != 3 {
		t.Fatalf("calls %d, %+v", len(s.calls), result.Nodes)
	}
	var its []string
	for _, n := range result.Nodes {
		its = append(its, n.Iteration)
	}
	if want := []string{".attempt[0]", ".attempt[1]", ".attempt[2]"}; !slices.Equal(its, want) {
		t.Errorf("iterations %v", its)
	}
	tree, _ := wf.Read()
	if got := tree["up"].(map[string]any)["a"].(map[string]any)["n"]; got != 3 {
		t.Errorf("registered n = %v, want the last try's 3", got)
	}
}

func TestUntil_ARetriedFailureDoesNotFailTheTaskButTheLastCan(t *testing.T) {
	_, result, _ := runStepper(t, collection.ModeExecute, `id: u2
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.ok == true"]
    retries: 2
    delay: 0
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		if n < 1 {
			return engine.ActionResult{}, errors.New("not yet")
		}
		return engine.ActionResult{Stats: map[string]any{"ok": true}}, nil
	})
	if result.HasErrors() {
		t.Fatalf("a retried failure failed the task: %+v", result.Nodes)
	}
	if result.Nodes[0].RetriedErr == "" || !strings.Contains(result.Nodes[0].RetriedErr, "not yet") {
		t.Errorf("first try %+v", result.Nodes[0])
	}

	_, result, _ = runStepper(t, collection.ModeExecute, `id: u3
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["result.r.ok == true"]
    retries: 1
    delay: 0
`, func(int, *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"ok": false}}, nil
	})
	if !result.HasErrors() || !strings.Contains(result.Nodes[len(result.Nodes)-1].Err.Error(), "until never held") {
		t.Fatalf("no failure naming until: %+v", result.Nodes)
	}
}

func TestUntil_ACheckNeverRetries(t *testing.T) {
	s, _, _ := runStepper(t, collection.ModeCheck, `id: c
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    until: ["false"]
    retries: 4
    delay: 0
`, echo)
	if len(s.calls) > 1 {
		t.Errorf("a check ran the task %d times", len(s.calls))
	}
}

func TestLoop_RefusedShapes(t *testing.T) {
	for name, body := range map[string]string{
		"both":        "loop: [1]\n    with_items: [1]",
		"text":        "loop: not a template",
		"number":      "loop: 5",
		"control":     "loop_control: {loop_var: x}",
		"reserved":    "loop: [1]\n    loop_control: {loop_var: vars}",
		"until noreg": "until: [\"true\"]",
		"retries":     "retries: 3",
		"too many":    "loop: [1]\n    register: r\n    until: [\"true\"]\n    retries: 101",
	} {
		t.Run(name, func(t *testing.T) {
			eval, _ := engine.NewCELEvaluator()
			_, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: bad\nhosts: web\ntasks:\n  - rbtest.step:\n      note: x\n    " + body + "\n"))
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}
