// Package engine: module-as-key task syntax sugar. A task may write its
// module name directly as a mapping key (net.cli.command: {...}) instead
// of the explicit fqcn:/params: pair Task itself decodes
// (dag.go). This file rewrites the sugar into that explicit shape on the
// raw parsed tree, before the normal yaml.Unmarshal/json.Unmarshal call
// into WorkflowDef/Task ever runs, so Task, WorkflowDef, Conditional, and
// AcquisitionStrategy never need to know sugar syntax exists, and
// TestBuildFromYAML_MatchesJSON's bidirectional-parity guarantee holds by
// construction: both formats are normalized to the identical fqcn:/
// params: shape before either one is decoded.
package engine

import (
	"encoding/json"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"
)

// ReservedTaskKeys are Task's own YAML/JSON key names (dag.go's Task
// struct tags). Any other key on a task map is either module-as-key
// sugar's module name, or an author's mistake; see normalizeTaskNode.
// Exported so the documentation generator (tools/gendocs) can build the
// task-key reference from this exact list: the reference cannot drift
// from what the parser actually accepts, because both read the same map.
var ReservedTaskKeys = map[string]bool{
	"name":             true,
	"fqcn":             true,
	"params":           true,
	"register":         true,
	"check_mode":       true,
	"when":             true,
	"when_or":          true,
	"when_cel":         true,
	"register_mask":    true,
	"secret_mask":      true,
	"lock_acquisition": true,
	"block":            true,
	"rescue":           true,
	"always":           true,
	"parallel":         true,
}

// --- YAML path -------------------------------------------------------

// normalizeWorkflowYAML rewrites module-as-key sugar throughout payload's
// pretasks/tasks/posttasks, at any block/rescue/always depth, then
// decodes the result into def. This is parseWorkflowYAML's replacement
// for a bare yaml.Unmarshal(payload, def) call.
//
// A payload that does not parse as a top-level YAML mapping (already
// rejected above by parseWorkflowYAML's own Ansible-shape sniff, or
// genuinely malformed) is decoded unchanged, so the normal decode error
// path still fires with its usual message rather than a confusing one
// from this file.
func normalizeWorkflowYAML(payload []byte, def *WorkflowDef) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		if err := yaml.Unmarshal(payload, def); err != nil {
			return fmt.Errorf("failed to unmarshal YAML: %w", err)
		}
		return nil
	}

	root := doc.Content[0]
	keys := make([]string, 0, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		keys = append(keys, root.Content[i].Value)
	}
	if err := checkRunbookKeys(keys); err != nil {
		return err
	}
	for _, key := range []string{"pretasks", "tasks", "posttasks"} {
		if seq := findMappingValue(root, key); seq != nil {
			if err := normalizeTaskListNode(seq, key); err != nil {
				return err
			}
		}
	}

	if err := doc.Decode(def); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	return nil
}

// normalizeWorkflowYAMLTaskList rewrites module-as-key sugar throughout
// data, a bare YAML list of tasks (the shape an import_tasks file uses,
// as opposed to a full runbook document with pretasks/tasks/posttasks),
// then decodes the result into out. Mirrors normalizeWorkflowYAML for the
// one other place this engine decodes a task list from raw YAML
// (resolveOneImport, import_tasks.go).
func normalizeWorkflowYAMLTaskList(data []byte, out *[]Task) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.SequenceNode {
		return yaml.Unmarshal(data, out)
	}
	if err := normalizeTaskListNode(doc.Content[0], "import"); err != nil {
		return err
	}
	return doc.Decode(out)
}

// normalizeTaskListNode rewrites module-as-key sugar on every task
// mapping node in seq (a YAML sequence node of task maps), recursing into
// each task's own block/rescue/always sub-lists at any depth. It mutates
// seq's Content in place. label is this list's own path prefix (e.g.
// "tasks", "tasks[1].block"), used to build each task's own label
// ("tasks[1]", "tasks[1].block[0]") for error messages, matching the
// synthesized node-ID scheme tasktree.go's synthesizeChain/collectSubtree
// produce later for the same tree shape.
func normalizeTaskListNode(seq *yaml.Node, label string) error {
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s: expected a YAML list of tasks, got a YAML node of kind %v", label, seq.Kind)
	}
	for i, task := range seq.Content {
		if err := normalizeTaskNode(task, fmt.Sprintf("%s[%d]", label, i)); err != nil {
			return err
		}
	}
	return nil
}

// normalizeTaskNode rewrites task, a single task mapping node, in place
// if it uses module-as-key sugar, then recurses into its own
// block/rescue/always/parallel sequences. label identifies this task in
// error messages (see normalizeTaskListNode).
func normalizeTaskNode(task *yaml.Node, label string) error {
	if task.Kind != yaml.MappingNode {
		return fmt.Errorf("task %s: expected a YAML map, got a YAML node of kind %v", label, task.Kind)
	}

	type kv struct{ key, val *yaml.Node }
	var nonReserved []kv
	hasFQCN, hasBlock, hasParallel := false, false, false

	for i := 0; i+1 < len(task.Content); i += 2 {
		key := task.Content[i]
		switch key.Value {
		case "fqcn":
			hasFQCN = true
		case "block":
			hasBlock = true
		case "parallel":
			hasParallel = true
		}
		if !ReservedTaskKeys[key.Value] {
			nonReserved = append(nonReserved, kv{key, task.Content[i+1]})
		}
	}

	switch {
	case len(nonReserved) > 1:
		names := make([]string, len(nonReserved))
		for i, p := range nonReserved {
			names[i] = p.key.Value
		}
		return fmt.Errorf("task %s has multiple unrecognized keys %v: module-as-key syntax allows exactly one module name per task", label, names)
	case len(nonReserved) == 1 && (hasFQCN || hasBlock || hasParallel):
		conflict := "fqcn:"
		switch {
		case hasBlock:
			conflict = "block:"
		case hasParallel:
			conflict = "parallel:"
		}
		return fmt.Errorf("task %s sets both %s and an unrecognized key %q: a task must be exactly one of a module call, a block, or a parallel group, and module-as-key sugar cannot combine with an explicit fqcn:/block:/parallel:", label, conflict, nonReserved[0].key.Value)
	case len(nonReserved) == 1:
		if err := rewriteModuleKeyNode(task, nonReserved[0].key, nonReserved[0].val, label); err != nil {
			return err
		}
	}

	for _, key := range []string{"block", "rescue", "always", "parallel"} {
		if seq := findMappingValue(task, key); seq != nil {
			if err := normalizeTaskListNode(seq, label+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

// rewriteModuleKeyNode replaces task's single (key, val) module-as-key
// pair with fqcn: key.Value and, if val carries any arguments, params:
// val, in place. val must be a mapping node (the module's arguments) or
// an empty/null scalar (a module with no arguments); anything else is a
// hard error naming label rather than a silent coercion.
func rewriteModuleKeyNode(task, key, val *yaml.Node, label string) error {
	hasParams := true
	if val.Kind == yaml.ScalarNode && val.Tag == "!!null" {
		hasParams = false
	} else if val.Kind != yaml.MappingNode {
		return fmt.Errorf("task %s: module %q must be a map of arguments (module-as-key syntax), got a YAML node of kind %v", label, key.Value, val.Kind)
	}

	fqcnKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "fqcn"}
	fqcnVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key.Value}

	newContent := make([]*yaml.Node, 0, len(task.Content)+2)
	for i := 0; i+1 < len(task.Content); i += 2 {
		if task.Content[i] == key {
			newContent = append(newContent, fqcnKey, fqcnVal)
			if hasParams {
				paramsKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "params"}
				newContent = append(newContent, paramsKey, val)
			}
			continue
		}
		newContent = append(newContent, task.Content[i], task.Content[i+1])
	}
	task.Content = newContent
	return nil
}

// findMappingValue returns m's value node for key, or nil if m has no
// such key. m must be a mapping node.
func findMappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// --- JSON path ---------------------------------------------------------

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
		return fmt.Errorf("task %s sets both %s and an unrecognized key %q: a task must be exactly one of a module call, a block, or a parallel group, and module-as-key sugar cannot combine with an explicit fqcn/block/parallel", label, conflict, nonReservedKeys[0])
	case len(nonReservedKeys) == 1:
		key := nonReservedKeys[0]
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
