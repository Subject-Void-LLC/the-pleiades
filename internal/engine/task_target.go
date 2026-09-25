// Package engine: the build-time rule for a task's params.target, the
// engine's own device selector (TaskTarget, action.go).
package engine

import (
	"encoding/json"
	"fmt"
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
	raw, present := task.Params["target"]
	if !present {
		return nil
	}
	if target, ok := raw.(string); ok && target != "" {
		return nil
	}
	return fmt.Errorf("task %s sets params.target to %s: a target must be a non-empty string naming one inventory host or tag (leave it out to use the runbook's hosts:)", taskLabel(id, task), valueKind(raw))
}

// valueKind names the kind of a decoded YAML or JSON value for an error
// message, without printing the value.
func valueKind(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		if v == "" {
			return "an empty string"
		}
		return "a string"
	case bool:
		return "a boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	default:
		return fmt.Sprintf("a value of type %T", v)
	}
}
