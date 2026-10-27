// Package validate: VarNamesRule, which holds every authored variable name and
// every set_fact to the shape the engine can read them in.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// VarNamesRule refuses, before anything runs:
//
//   - a key of the runbook's vars:, of a block's or task's vars:, or a
//     set_fact fact that is not a variable name, since {{ vars.name }} could
//     never read it;
//   - a set_fact's global or no_override that is not a boolean, since it
//     would be read as a fact otherwise;
//   - a set_fact that sets no fact at all.
//
// A block's vars: reach the tasks inside it (the builder copies them down), so
// a bad key is reported once, where it was written, and not again on every
// task it reached. A fact cannot be named global or no_override: those words
// are the two options, so a boolean beside a fact is never a fact.
func VarNamesRule(world WorldView) []Finding {
	var findings []Finding
	bad := func(node, where string, keys []string) {
		sort.Strings(keys)
		for _, k := range keys {
			if err := engine.CheckVarNames([]string{k}); err != nil {
				findings = append(findings, Finding{RuleName: "varnames", Node: node, Message: fmt.Sprintf("%s: %v", where, err)})
			}
		}
	}
	keysOf := func(m map[string]any) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out
	}

	bad("runbook", "the runbook's vars:", keysOf(world.DAG.Vars))
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		parent := parentTask(world.DAG, id)
		var own []string
		for k := range task.Vars {
			// A key the enclosing block carries was reported at the block.
			if parent != nil {
				if _, inherited := parent.Vars[k]; inherited {
					continue
				}
			}
			own = append(own, k)
		}
		bad(id, fmt.Sprintf("task %s, vars:", taskName(id, task)), own)

		if !isSetFactTask(task) {
			continue
		}
		var facts []string
		for k, v := range task.Params {
			if k == engine.SetFactGlobal || k == engine.SetFactNoOverride {
				if _, isBool := v.(bool); !isBool {
					findings = append(findings, Finding{RuleName: "varnames", Node: id,
						Message: fmt.Sprintf("task %s: set_fact option %s must be true or false, not %v", taskName(id, task), k, v)})
				}
				continue
			}
			facts = append(facts, k)
		}
		if len(facts) == 0 {
			findings = append(findings, Finding{RuleName: "varnames", Node: id,
				Message: fmt.Sprintf("task %s: set_fact needs at least one variable to set", taskName(id, task))})
		}
		bad(id, fmt.Sprintf("task %s, set_fact", taskName(id, task)), facts)
	}
	return findings
}

// init registers VarNamesRule with the rules Validate runs.
func init() {
	Register(VarNamesRule)
}
