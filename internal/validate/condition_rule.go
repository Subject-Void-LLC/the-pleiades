// Package validate: ConditionRule, which holds a condition's register reads
// to the registers the runbook writes, as TemplateRule holds a parameter
// template's (FAILURE_PATTERNS 561).
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// ConditionRuleName is ConditionRule's RuleName.
const ConditionRuleName = "condition"

// ConditionRule refuses a when, when_or or when_cel that names a register no
// task in the runbook registers, or that only the task itself registers,
// which does not exist until that task has run. Like TemplateRule this is
// existence, not order: a rescue or always task has no place in a total
// order, and a register read before it is written still fails the task
// when its condition is evaluated. A read no name can be read off (a
// computed index, a whole root handed to a macro) is left alone, since it
// names nothing to check. A condition that does not parse is the builder's
// error, never reaching here from a built runbook.
func ConditionRule(world WorldView) []Finding {
	writers := map[string]int{}
	for _, task := range world.DAG.Nodes {
		if task.Register != "" {
			writers[task.Register]++
		}
	}

	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var findings []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		names, _, err := engine.ConditionReads(task.Conditional)
		if err != nil {
			continue
		}
		for _, name := range names {
			var message string
			switch {
			case writers[name] == 0:
				message = fmt.Sprintf("task %s's condition reads the register %q, and no task in this runbook registers it", taskName(id, task), name)
			case writers[name] == 1 && task.Register == name:
				message = fmt.Sprintf("task %s's condition reads the register %q, this task's own, which does not exist until the task has run", taskName(id, task), name)
			default:
				continue
			}
			findings = append(findings, Finding{RuleName: ConditionRuleName, Node: id, Message: message})
		}
	}
	return findings
}

func init() {
	Register(ConditionRule)
}
