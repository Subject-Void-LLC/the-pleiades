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
// names nothing to check, and so is a name the condition tests for with
// has() or in: such a condition is written for the register's absence, as
// HandlersRule reads it, and its only writer may be a task tag selection
// left out (select.go leaves guarding that read to the author). An optional
// read (result.?name, result[?"name"]) is such a test too.
//
// A condition whose references cannot be read is refused here, naming why,
// and never taken to read nothing (FAILURE_PATTERNS 1351). The builder
// compiles every condition, and every condition that compiles parses with the
// readers' parser (engine.ParseCondition), so a built runbook does not reach
// this; it is the one refusal the other rules reading a condition
// (HandlersRule, TemplateRule, VarsRule) rely on when they skip one.
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
			findings = append(findings, Finding{RuleName: ConditionRuleName, Node: id,
				Message: fmt.Sprintf("task %s's condition cannot be read, so which registers, variables and roots it reads cannot be checked: %v", taskName(id, task), err)})
			continue
		}
		// The same sources parsed just above, so this cannot fail; were it to,
		// nothing would count as tested, and every read would be held to the
		// writers, the stricter side.
		tested, _ := engine.ConditionTestsPresence(task.Conditional)
		for _, name := range names {
			var message string
			switch {
			case tested[name]:
				continue
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
