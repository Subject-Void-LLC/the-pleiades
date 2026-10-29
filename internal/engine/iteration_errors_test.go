// Tests of how loops, retries, vars and assert fail when what they depend on
// does: a workflow context that cannot be read or written, a run with no
// renderer, a condition that cannot be evaluated, and the shapes validation
// refuses.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// brokenContext is a workflow context whose reads or writes fail once told to.
type brokenContext struct {
	engine.WorkflowContext
	failRead, failMerge atomic.Bool
}

func (b *brokenContext) Read() (map[string]any, error) {
	if b.failRead.Load() {
		return nil, errors.New("the context cannot be read")
	}
	return b.WorkflowContext.Read()
}

func (b *brokenContext) Merge(register, device string, stats map[string]any) error {
	if b.failMerge.Load() {
		return errors.New("the context cannot be written")
	}
	return b.WorkflowContext.Merge(register, device, stats)
}

// runBroken runs runbook with a context the caller can break, and no renderer
// unless withRenderer.
func runBroken(t *testing.T, runbook string, withRenderer bool, wf engine.WorkflowContext, fn func(int, *engine.Task) (engine.ActionResult, error)) engine.RunResult {
	t.Helper()
	s := &stepper{fn: fn, builtin: engine.NewBuiltinActionExecutor()}
	var opts []engine.ExecutorOption
	if withRenderer {
		opts = append(opts, engine.WithRenderer(render.New()))
	}
	x := engine.NewExecutor(oneDevice(), s, lock.NewInProcessManager(), event.NewInProcessBus(), wf, 0, opts...)
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func failedWith(result engine.RunResult, text string) bool {
	for _, n := range result.Nodes {
		if n.Err != nil && strings.Contains(n.Err.Error(), text) {
			return true
		}
	}
	return false
}

func TestIteration_ABrokenContextFailsTheTaskAndSaysSo(t *testing.T) {
	t.Run("loop cannot register", func(t *testing.T) {
		wf := &brokenContext{WorkflowContext: engine.NewInProcessWorkflowContext()}
		wf.failMerge.Store(true)
		result := runBroken(t, "id: a\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    register: r\n    loop: [1, 2]\n", true, wf, echo)
		if !failedWith(result, "cannot be written") {
			t.Fatalf("%+v", result.Nodes)
		}
	})
	t.Run("until cannot register its last try", func(t *testing.T) {
		wf := &brokenContext{WorkflowContext: engine.NewInProcessWorkflowContext()}
		wf.failMerge.Store(true)
		result := runBroken(t, "id: b\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    register: r\n    until: [\"true\"]\n    delay: 0\n", true, wf, echo)
		if !failedWith(result, "cannot be written") {
			t.Fatalf("%+v", result.Nodes)
		}
	})
	t.Run("until cannot read the context", func(t *testing.T) {
		wf := &brokenContext{WorkflowContext: engine.NewInProcessWorkflowContext()}
		result := runBroken(t, "id: c\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    register: r\n    until: [\"true\"]\n    delay: 0\n", true, wf,
			func(int, *engine.Task) (engine.ActionResult, error) {
				wf.failRead.Store(true)
				return engine.ActionResult{Stats: map[string]any{"ok": true}}, nil
			})
		if !failedWith(result, "until") {
			t.Fatalf("%+v", result.Nodes)
		}
	})
	t.Run("a condition cannot read the context", func(t *testing.T) {
		wf := &brokenContext{WorkflowContext: engine.NewInProcessWorkflowContext()}
		wf.failRead.Store(true)
		result := runBroken(t, "id: d\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    when: [\"true\"]\n", true, wf, echo)
		if !result.HasErrors() {
			t.Fatalf("%+v", result.Nodes)
		}
	})
}

func TestIteration_WithNoRendererTheRunSaysSo(t *testing.T) {
	result := runBroken(t, "id: e\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    loop: \"{{ vars.names }}\"\n", false, engine.NewInProcessWorkflowContext(), echo)
	if !failedWith(result, "no template renderer") {
		t.Fatalf("a loop expression with no renderer: %+v", result.Nodes)
	}
	result = runBroken(t, "id: f\nhosts: web\nvars:\n  x: \"{{ vars.y }}\"\n  y: 1\ntasks:\n  - rbtest.step: {note: x}\n    when_cel: \"vars.x == 1\"\n", false, engine.NewInProcessWorkflowContext(), echo)
	if !result.HasErrors() {
		t.Fatalf("a templated var with no renderer: %+v", result.Nodes)
	}
}

func TestAssert_ACondtionThatCannotBeEvaluatedFails(t *testing.T) {
	result := runBroken(t, "id: g\nhosts: web\ntasks:\n  - assert:\n      that: [\"result.nothing.here == 1\"]\n", true, engine.NewInProcessWorkflowContext(), echo)
	if !failedWith(result, "failed to evaluate that") {
		t.Fatalf("%+v", result.Nodes)
	}
}

func TestIteration_MoreRefusedShapes(t *testing.T) {
	for name, body := range map[string]string{
		"loop on a block":              "block:\n      - noop: {}\n    loop: [1]",
		"until on a block":             "block:\n      - noop: {}\n    until: [\"true\"]",
		"changed_when on a block":      "block:\n      - noop: {}\n    changed_when: [\"true\"]",
		"loop given as a number":       "noop: {}\n    loop: 5",
		"delay too long":               "noop: {}\n    register: r\n    until: [\"true\"]\n    delay: 99999",
		"retries negative":             "noop: {}\n    register: r\n    until: [\"true\"]\n    retries: -1",
		"loop_var not a name":          "noop: {}\n    loop: [1]\n    loop_control: {loop_var: \"not a name\"}",
		"index_var reserved":           "noop: {}\n    loop: [1]\n    loop_control: {index_var: nodes}",
		"until does not compile":       "noop: {}\n    register: r\n    until: [\"this is (not valid\"]",
		"loop with rollback":           "noop: {}\n    loop: [1]\n    rollback:\n      - noop: {}",
		"until with rollback":          "noop: {}\n    register: r\n    until: [\"true\"]\n    rollback:\n      - noop: {}",
		"failed_when without register": "noop: {}\n    failed_when: [\"true\"]",
	} {
		t.Run(name, func(t *testing.T) {
			eval, _ := engine.NewCELEvaluator()
			if _, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: bad\ntasks:\n  - " + body + "\n")); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// TestLoop_TheRegisteredResultsAreBounded: a loop whose items each return a
// large result stops with a named failure instead of holding them all.
func TestLoop_TheRegisteredResultsAreBounded(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	result := runBroken(t, "id: big\nhosts: web\ntasks:\n  - rbtest.step: {note: x}\n    register: r\n    loop: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]\n", true, engine.NewInProcessWorkflowContext(),
		func(int, *engine.Task) (engine.ActionResult, error) {
			return engine.ActionResult{Stats: map[string]any{"out": big}}, nil
		})
	if !failedWith(result, "hold more than") {
		t.Fatalf("a loop kept 20 MiB of results: %d results", len(result.Nodes))
	}
}
