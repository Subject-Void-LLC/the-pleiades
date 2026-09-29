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
	"fmt"
	"reflect"
	"strings"

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
	"within":           true,
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
	"tags":             true,
	"rollback":         true,
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
	// After the sugar rewrite, so a module name written as a key has
	// already become fqcn:/params: and is not mistaken for an unknown key.
	if err := checkKnownKeys(yamlKeyTree{root}, reflect.TypeFor[WorkflowDef](), "yaml", ""); err != nil {
		return err
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
	if err := checkKnownKeys(yamlKeyTree{doc.Content[0]}, reflect.TypeFor[[]Task](), "yaml", "import"); err != nil {
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
		// A YAML merge key (<<) is not a module name: its merged keys are
		// checked against Task's own fields by checkKnownKeys.
		if key.ShortTag() == "!!merge" {
			continue
		}
		if !ReservedTaskKeys[key.Value] {
			nonReserved = append(nonReserved, kv{key, task.Content[i+1]})
		}
	}

	names := make([]string, len(nonReserved))
	for i, p := range nonReserved {
		names[i] = p.key.Value
	}
	if err := refuseAnsibleTaskKeywords(label, names); err != nil {
		return err
	}
	switch {
	case len(nonReserved) > 1:
		return fmt.Errorf("task %s has multiple unrecognized keys %v: module-as-key syntax allows exactly one module name per task", label, names)
	case len(nonReserved) == 1 && (hasFQCN || hasBlock || hasParallel):
		conflict := "fqcn:"
		switch {
		case hasBlock:
			conflict = "block:"
		case hasParallel:
			conflict = "parallel:"
		}
		return fmt.Errorf("task %s sets both %s and an unrecognized key %q%s: a task must be exactly one of a module call, a block, or a parallel group, and module-as-key sugar cannot combine with an explicit fqcn:/block:/parallel:", label, conflict, names[0], taskKeyHint(names[0]))
	case len(nonReserved) == 1:
		if err := refuseNonModuleKey(label, names[0]); err != nil {
			return err
		}
		if err := rewriteModuleKeyNode(task, nonReserved[0].key, nonReserved[0].val, label); err != nil {
			return err
		}
	}

	// rollback: holds tasks too (a task's authored undo), written the same
	// way, so it is normalized with the others.
	for _, key := range []string{"block", "rescue", "always", "parallel", "rollback"} {
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

// refuseAnsibleTaskKeywords refuses the first of keys, a task's keys that
// are not task keys, that is an Ansible keyword, with a message saying so
// rather than reading it as a module name.
func refuseAnsibleTaskKeywords(label string, keys []string) error {
	for _, key := range keys {
		if isAnsibleTaskKeyword(key) {
			return fmt.Errorf("task %s uses %q, an Ansible keyword a native runbook does not support; %s says what each Ansible keyword becomes", label, key, migrationGuide)
		}
	}
	return nil
}

// refuseNonModuleKey refuses key, the one key on task label that is not a
// task key, when it cannot be a module name: a module name is always
// namespaced (pkg/collection.Register refuses one with no dot), so an
// undotted key is a mistake unless it is one of the engine's own actions.
// Without this, a misspelled params: holding a map became a task calling
// a method named "paramz", which validation skipped as an engine word and
// the run then failed on.
func refuseNonModuleKey(label, key string) error {
	if strings.Contains(key, ".") || isEngineAction(key) {
		return nil
	}
	return fmt.Errorf("task %s: %q is neither a task key nor a module name%s; a module name is always namespaced, like pkg.apt.install", label, key, taskKeyHint(key))
}

// taskKeyHint returns a " (did you mean ...?)" suffix naming the task key
// closest to key, or "" when none is close.
func taskKeyHint(key string) string {
	if near := nearestKey(key, ReservedTaskKeys); near != "" {
		return fmt.Sprintf(" (did you mean %q?)", near)
	}
	return ""
}

// isEngineAction reports whether name is one of the engine's own undotted
// actions: the in-process built-ins, the transport actions, and the
// import_tasks keyword.
func isEngineAction(name string) bool {
	if name == "import_tasks" {
		return true
	}
	if _, ok := engineActionStatKeys[name]; ok {
		return true
	}
	_, ok := ActionCapability[name]
	return ok
}
