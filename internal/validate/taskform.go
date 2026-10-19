// TaskFormWarnings: the advice that a task name its method as its key.
package validate

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// TaskFormRule is the RuleName of the task form's warning.
const TaskFormRule = "task-form"

// TaskFormWarnings advises, in a warning that never fails validation, that
// each task written as fqcn: and params: be written with its method as its
// key, the form a runbook is written in (a task's pkg.apt.install: key
// holding {name: nginx}, rather than fqcn: pkg.apt.install beside params:).
// The engine still reads fqcn: and params:, so an older runbook runs as it
// did; the warning names each such task, in pretasks, tasks and posttasks
// and in every block, rescue and always within them, by the node ID a run
// gives it. A file that is not a YAML mapping is left to the build, which
// refuses it.
func TaskFormWarnings(source []byte) []Finding {
	var doc yaml.Node
	if yaml.Unmarshal(source, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	var out []Finding
	var walk func(seq *yaml.Node, prefix string)
	walk = func(seq *yaml.Node, prefix string) {
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return
		}
		for i, task := range seq.Content {
			id := fmt.Sprintf("%s[%d]", prefix, i)
			if task.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(task.Content); k += 2 {
				key, value := task.Content[k].Value, task.Content[k+1]
				switch key {
				case "fqcn":
					out = append(out, Finding{RuleName: TaskFormRule, Node: id, Message: fmt.Sprintf(
						"%s is written as fqcn: and params:; write its method as the task's key, %s: with its parameters beneath (fqcn: is read only for older runbooks)",
						id, value.Value)})
				case "block", "rescue", "always":
					walk(value, id+"."+key)
				}
			}
		}
	}
	root := doc.Content[0]
	for k := 0; k+1 < len(root.Content); k += 2 {
		switch key := root.Content[k].Value; key {
		case "pretasks", "tasks", "posttasks":
			walk(root.Content[k+1], key)
		}
	}
	return out
}
