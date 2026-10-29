// Tests of changed_when, failed_when, ignore_errors, assert and fail
// (Phase 88) through the real Executor.
package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestFailedWhenAndChangedWhenReclassifyTheResult(t *testing.T) {
	_, result, _ := runStepper(t, collection.ModeExecute, `id: cw
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    changed_when: ["result.r.code == 2"]
    failed_when: ["result.r.code == 9"]
`, func(int, *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"code": 2}}, nil
	})
	if result.HasErrors() || !result.Nodes[0].Changed {
		t.Fatalf("changed_when did not set changed: %+v", result.Nodes[0])
	}
	_, result, _ = runStepper(t, collection.ModeExecute, `id: fw
hosts: web
tasks:
  - rbtest.step:
      note: x
    register: r
    failed_when: ["result.r.code == 9"]
`, func(int, *engine.Task) (engine.ActionResult, error) {
		return engine.ActionResult{Stats: map[string]any{"code": 9}}, nil
	})
	if !result.HasErrors() || !strings.Contains(result.Nodes[0].Err.Error(), "failed_when held") {
		t.Fatalf("failed_when did not fail the task: %+v", result.Nodes[0])
	}
}

func TestIgnoreErrorsRecordsTheFailureAndGoesOn(t *testing.T) {
	s, result, _ := runStepper(t, collection.ModeExecute, `id: ie
hosts: web
tasks:
  - rbtest.step:
      note: first
    ignore_errors: true
  - rbtest.step:
      note: second
`, func(n int, task *engine.Task) (engine.ActionResult, error) {
		if n == 0 {
			return engine.ActionResult{}, errors.New("broke")
		}
		return engine.ActionResult{}, nil
	})
	if result.HasErrors() || len(s.calls) != 2 {
		t.Fatalf("the run did not go on: calls %v, %+v", s.calls, result.Nodes)
	}
	if first := result.Nodes[0]; first.Err == nil || !first.Ignored {
		t.Errorf("the ignored failure was not recorded: %+v", first)
	}
}

func TestAssertAndFail(t *testing.T) {
	_, result, _ := runStepper(t, collection.ModeExecute, `id: a
hosts: web
tasks:
  - noop:
      n: 3
    register: r
  - assert:
      that:
        - "result.r.n == 3"
      success_msg: fine
`, echo)
	if result.HasErrors() {
		t.Fatalf("a true assertion failed: %+v", result.Nodes)
	}
	_, result, _ = runStepper(t, collection.ModeExecute, `id: a2
hosts: web
tasks:
  - noop:
      n: 3
    register: r
  - assert:
      that: ["result.r.n == 4"]
      fail_msg: n was not 4
`, echo)
	if !result.HasErrors() || !strings.Contains(result.Nodes[1].Err.Error(), "n was not 4") {
		t.Fatalf("a false assertion did not fail with its message: %+v", result.Nodes)
	}
	_, result, _ = runStepper(t, collection.ModeExecute, `id: f
hosts: web
tasks:
  - fail:
      msg: stop here
    ignore_errors: true
  - noop: {}
`, echo)
	if result.HasErrors() || result.Nodes[0].Err == nil || !strings.Contains(result.Nodes[0].Err.Error(), "stop here") {
		t.Fatalf("fail with ignore_errors: %+v", result.Nodes)
	}
}

func TestFailureKeywordsRefusedShapes(t *testing.T) {
	for name, body := range map[string]string{
		"changed_when without register": "rbtest.step: {note: x}\n    changed_when: [\"true\"]",
		"ignore_errors on a block":      "block:\n      - rbtest.step: {note: x}\n    ignore_errors: true",
		"assert without that":           "assert: {fail_msg: x}",
		"assert bad key":                "assert: {that: [\"true\"], oops: 1}",
		"loop with rollback":            "rbtest.step: {note: x}\n    loop: [1]\n    rollback:\n      - rbtest.unmake: {name: x}",
	} {
		t.Run(name, func(t *testing.T) {
			eval, _ := engine.NewCELEvaluator()
			_, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: bad\nhosts: web\ntasks:\n  - " + body + "\n"))
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}
