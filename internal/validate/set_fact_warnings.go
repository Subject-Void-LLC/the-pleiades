// Package validate: SetFactOverrideWarnings, which says when a run's extra
// variable has the name a set_fact sets, so the set_fact's value is never read.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// SetFactOverrideWarnings returns one warning for each name a set_fact sets
// that the run's extra variables (WorldView.Variables: a launch's, or the
// CLI's --extra-vars) also give. The extra variables are an override from
// outside the runbook and win over every scope, a set_fact included
// (internal/engine's vars.go), so an operator's value can never be replaced
// by one the run computes. The set_fact then sets a value nothing reads,
// which is legal and so only advice: the author may have meant another name.
// A caller that does not know the extra variables (VariablesKnown false, or
// none given) gets nothing.
func SetFactOverrideWarnings(world WorldView) []Finding {
	if !world.VariablesKnown || len(world.Variables) == 0 {
		return nil
	}
	from := "the run's extra variables"
	if world.VariablesFlag != "" {
		from = world.VariablesFlag
	}
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id, task := range world.DAG.Nodes {
		if isSetFactTask(task) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		names := make([]string, 0, len(task.Params))
		for k := range task.Params {
			if _, given := world.Variables[k]; given && !engine.IsSetFactOption(k) {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, Finding{
				RuleName: "vars",
				Node:     id,
				Message: fmt.Sprintf("task %s: set_fact sets vars.%s, and %s gives %s too, which wins over every scope, so the value this set_fact sets is never read",
					taskName(id, task), name, from, name),
			})
		}
	}
	return out
}
