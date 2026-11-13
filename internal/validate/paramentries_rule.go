// Package validate: ParamEntriesRule, which holds a structured parameter's
// entries to the keys and types its method declares.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ParamEntriesRule refuses a task whose structured parameter (a list of
// maps, or a map) holds an entry its method's declaration of that
// parameter's entries does not admit (collection.Param.Entries): a key no
// entry takes, a required key left out, or a value of another type or
// outside its choices (collection.EntryProblems). A parameter that declares
// no entries is not checked here, and a text value holding a template is
// the engine's to judge once it renders.
//
// It exists because YAML's flow syntax splits an unquoted comma into a key
// of its own, so {text: x, reason: a, b} is three keys, and validation
// passed it while the run refused it (FAILURE_PATTERNS 584). Like
// ParamTypeRule, it checks only a method that is registered and
// implemented.
func ParamEntriesRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if len(task.Params) == 0 || !isCollectionName(task.FQCN) || dottedBuiltin(task.FQCN) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		label := taskLabel(id, task)
		for _, p := range desc.Manifest.Doc.Params {
			value, given := task.Params[p.Name]
			if !given || len(p.Entries) == 0 {
				continue
			}
			problems := collection.EntryProblems(p, value)
			slices.Sort(problems)
			for _, problem := range problems {
				findings = append(findings, Finding{
					RuleName: "param-entries",
					Node:     id,
					Message:  fmt.Sprintf("task %s: %s's %s", label, task.FQCN, problem),
				})
			}
		}
	}
	return findings
}

func init() {
	Register(ParamEntriesRule)
}
