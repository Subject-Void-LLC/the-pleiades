// The assert and fail actions (Phase 88): engine keywords, not Collection
// methods, since neither reaches a device. fail ends the task failed with a
// message. assert fails when any of its that: conditions is false. Both fail
// the way any task does, so ignore_errors, rescue and always treat them as
// any failure.
//
// assert's conditions are written as a when: list is, compiled once when the
// runbook builds (compileTaskPrograms) and evaluated against what the task
// would be decided with, so a typo in one is a build error, not a failure at
// task 47.
package engine

import (
	"fmt"
	"strings"
)

// The names of the two actions, bare and namespaced like set_metadata's.
const (
	AssertName = "assert"
	AssertFQCN = "pleiades.builtin.assert"
	FailName   = "fail"
	FailFQCN   = "pleiades.builtin.fail"
)

// isAssert reports whether fqcn is the assert action.
func isAssert(fqcn string) bool { return fqcn == AssertName || fqcn == AssertFQCN }

// assertThat returns the conditions an assert task's that: names, or nil for
// any other task.
func assertThat(task *Task) StringList {
	if !isAssert(task.FQCN) {
		return nil
	}
	switch v := task.Params["that"].(type) {
	case string:
		return StringList{v}
	case []any:
		out := make(StringList, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// validateAssert holds an assert task to a non-empty that: of strings, and
// any message to text.
func validateAssert(task *Task, id string) error {
	label := taskLabel(id, task)
	switch v := task.Params["that"].(type) {
	case string:
		if v == "" {
			return fmt.Errorf("task %s: assert needs a condition in that:", label)
		}
	case []any:
		if len(v) == 0 {
			return fmt.Errorf("task %s: assert needs at least one condition in that:", label)
		}
		for _, e := range v {
			if s, ok := e.(string); !ok || s == "" {
				return fmt.Errorf("task %s: each condition in that: is text, like a when: entry", label)
			}
		}
	default:
		return fmt.Errorf("task %s: assert needs that:, a condition or a list of them", label)
	}
	for k := range task.Params {
		if k != "that" && k != "fail_msg" && k != "success_msg" {
			return fmt.Errorf("task %s: assert takes that, fail_msg and success_msg, not %q", label, k)
		}
	}
	return nil
}

// runAssert evaluates an assert task's conditions, returning its result, or
// an error carrying fail_msg (or the failing conditions) when one is false.
func (r *run) runAssert(cmd nodeExecution, key string) (ActionResult, error) {
	task := cmd.Task
	ok, err := r.holds(task.progs.assert, task, key, ownRoots(cmd), nil)
	if err != nil {
		return ActionResult{}, fmt.Errorf("failed to evaluate that: %w", err)
	}
	if !ok {
		if msg, _ := task.Params["fail_msg"].(string); msg != "" {
			return ActionResult{}, fmt.Errorf("%s", msg)
		}
		return ActionResult{}, fmt.Errorf("assertion failed: %s", strings.Join(assertThat(task), " and "))
	}
	msg, _ := task.Params["success_msg"].(string)
	if msg == "" {
		msg = "all assertions passed"
	}
	return ActionResult{Stats: map[string]any{"msg": msg}}, nil
}

// failMessage is the message a fail task carries.
func failMessage(task *Task) string {
	if msg, _ := task.Params["msg"].(string); msg != "" {
		return msg
	}
	return "failed as requested from task"
}

// ownRoots are the roots a finished run of cmd's task is classified with: the
// ones it was decided with and its device.
func ownRoots(cmd nodeExecution) map[string]any {
	roots := make(map[string]any, len(cmd.Task.evalRoots)+2)
	for k, v := range cmd.Task.evalRoots {
		roots[k] = v
	}
	if cmd.Device != nil {
		if _, ok := roots[TemplateRootDevice]; !ok {
			roots[TemplateRootDevice] = deviceRoot(cmd.Device)
		}
		if _, ok := roots[hostVarsRoot]; !ok {
			roots[hostVarsRoot] = deviceVars(cmd.Device)
		}
	}
	return roots
}
