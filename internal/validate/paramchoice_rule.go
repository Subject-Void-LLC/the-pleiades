// Package validate: ParamChoiceRule, which refuses a task parameter whose
// value is outside the closed set its method declares.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ParamChoiceRule flags a task passing a parameter that declares a closed
// set (collection.Param.Choices) a value outside it, naming the set, so an
// author who guessed a value (a route protocol, a datastore, a lock
// policy) learns the real ones before the run rather than from the method
// halfway through it, or from a device that took the guess for something
// else.
//
// It shares ParamTypeRule's scope: a registered, implemented method, and a
// parameter it declares (its own or a fragment's). A value holding a
// template is the engine's to judge once it renders, and a value of the
// wrong type is ParamTypeRule's finding, so neither is judged here.
func ParamChoiceRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if len(task.Params) == 0 || !isCollectionName(task.FQCN) || dottedBuiltin(task.FQCN) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		decls := fragment.ByName(desc)
		keys := make([]string, 0, len(task.Params))
		for key := range task.Params {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			value := task.Params[key]
			p, declared := decls[key]
			if !declared || len(p.Choices) == 0 || engine.HasTemplate(value) {
				continue
			}
			// A value of the wrong type is ParamTypeRule's to report;
			// reporting it twice would make one mistake read as two.
			if fits, err := collection.ParamFits(p.Type, value); err != nil || !fits {
				continue
			}
			problem := collection.ChoiceProblem(p, value)
			if problem == "" {
				continue
			}
			label := id
			if task.Name != "" {
				label = fmt.Sprintf("%s (name %q)", id, task.Name)
			}
			findings = append(findings, Finding{
				RuleName: "param-choice",
				Node:     id,
				Message:  fmt.Sprintf("task %s passes parameter %q a value %s does not take: %s", label, key, task.FQCN, problem),
			})
		}
	}
	return findings
}

func init() {
	Register(ParamChoiceRule)
}
