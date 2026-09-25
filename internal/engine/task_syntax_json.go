// Package engine: the JSON half of module-as-key task syntax sugar (see
// task_syntax.go for the YAML half and for what the sugar is). Both halves
// apply the same rules, so a runbook written in either format is read the
// same way.
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
)

// normalizeWorkflowJSON rewrites module-as-key sugar throughout payload's
// pretasks/tasks/posttasks and returns the rewritten JSON bytes, ready
// for the normal json.Unmarshal(_, &WorkflowDef{}) decode (Build, dag.go).
// A payload that does not even decode as a JSON object is returned
// unchanged, so the real decode below surfaces its own standard error
// rather than a confusing one from this file.
func normalizeWorkflowJSON(payload []byte) ([]byte, error) {
	var generic map[string]interface{}
	if err := json.Unmarshal(payload, &generic); err != nil {
		return payload, nil
	}

	for _, key := range []string{"pretasks", "tasks", "posttasks"} {
		if list, ok := generic[key]; ok {
			if err := normalizeTaskListJSON(list, key); err != nil {
				return nil, err
			}
		}
	}

	rewritten, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal normalized runbook JSON: %w", err)
	}
	return rewritten, nil
}

// normalizeTaskListJSON rewrites module-as-key sugar (see
// normalizeTaskListNode, this file's YAML counterpart) throughout list, a
// []interface{} of task maps decoded from JSON, recursing into each
// task's own block/rescue/always at any depth. It mutates each task map
// in place. label mirrors normalizeTaskListNode's own path-label scheme.
func normalizeTaskListJSON(list interface{}, label string) error {
	items, ok := list.([]interface{})
	if !ok {
		return nil // wrong shape; let the real decode surface its own error
	}
	for i, item := range items {
		task, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if err := normalizeTaskMapJSON(task, fmt.Sprintf("%s[%d]", label, i)); err != nil {
			return err
		}
	}
	return nil
}

// normalizeTaskMapJSON rewrites task, a single task object decoded from
// JSON, in place if it uses module-as-key sugar, then recurses into its
// own block/rescue/always/parallel. label identifies this task in error
// messages.
func normalizeTaskMapJSON(task map[string]interface{}, label string) error {
	_, hasFQCN := task["fqcn"]
	_, hasBlock := task["block"]
	_, hasParallel := task["parallel"]

	var nonReservedKeys []string
	for key := range task {
		if !ReservedTaskKeys[key] {
			nonReservedKeys = append(nonReservedKeys, key)
		}
	}
	sort.Strings(nonReservedKeys) // map iteration order is random; keep error text deterministic

	if err := refuseAnsibleTaskKeywords(label, nonReservedKeys); err != nil {
		return err
	}
	switch {
	case len(nonReservedKeys) > 1:
		return fmt.Errorf("task %s has multiple unrecognized keys %v: module-as-key syntax allows exactly one module name per task", label, nonReservedKeys)
	case len(nonReservedKeys) == 1 && (hasFQCN || hasBlock || hasParallel):
		conflict := "fqcn"
		switch {
		case hasBlock:
			conflict = "block"
		case hasParallel:
			conflict = "parallel"
		}
		return fmt.Errorf("task %s sets both %s and an unrecognized key %q%s: a task must be exactly one of a module call, a block, or a parallel group, and module-as-key sugar cannot combine with an explicit fqcn/block/parallel", label, conflict, nonReservedKeys[0], taskKeyHint(nonReservedKeys[0]))
	case len(nonReservedKeys) == 1:
		key := nonReservedKeys[0]
		if err := refuseNonModuleKey(label, key); err != nil {
			return err
		}
		val := task[key]
		delete(task, key)
		task["fqcn"] = key
		if val != nil {
			params, ok := val.(map[string]interface{})
			if !ok {
				return fmt.Errorf("task %s: module %q must be an object of arguments (module-as-key syntax), got %T", label, key, val)
			}
			task["params"] = params
		}
	}

	for _, key := range []string{"block", "rescue", "always", "parallel"} {
		if sub, ok := task[key]; ok {
			if err := normalizeTaskListJSON(sub, label+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}
