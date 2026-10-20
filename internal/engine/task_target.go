// Package engine: the build-time rule for a task's params.target, the
// engine's own device selector (TaskTarget, action.go).
package engine

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// validateTarget refuses a params.target that is present but is not a
// non-empty string. TaskTarget reads the key as "the host or tag this
// task runs against" and falls back to the runbook's hosts: when the key
// is absent; before this check it fell back the same way for a list, a
// number or an empty string, so a task written for other devices ran
// against hosts: instead, at validation and at run time alike
// (FAILURE_PATTERNS 11). The error names the value's kind, never the
// value itself.
func validateTarget(task *Task, id string) error {
	raw, present := task.Params[collection.TargetParam]
	target, isText := raw.(string)
	templated := isText && strings.Contains(target, "{{")
	switch {
	case templated && task.Within == "":
		return fmt.Errorf("task %s renders params.target from data, so it needs a within: naming the device or tag the rendered target must stay inside", taskLabel(id, task))
	case task.Within != "" && strings.Contains(task.Within, "{{"):
		return fmt.Errorf("task %s renders within:, which is the bound on a rendered target and must be written literally", taskLabel(id, task))
	case task.Within != "" && !templated:
		return fmt.Errorf("task %s sets within: without a rendered params.target: within: bounds a target chosen from data, and nothing here is chosen from data", taskLabel(id, task))
	}
	if !present {
		return nil
	}
	if isText && target != "" {
		return nil
	}
	return fmt.Errorf("task %s sets params.target to %s: a target must be a non-empty string naming one inventory host or tag (leave it out to use the runbook's hosts:)", taskLabel(id, task), collection.ValueKind(raw))
}
