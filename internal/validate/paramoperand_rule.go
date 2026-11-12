// Package validate: ParamOperandRule, which refuses a task parameter naming
// a package, unit or service with a value its program would read as
// something other than one name.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ParamOperandRule flags a task passing a parameter its method declares an
// operand (collection.ParamFormatOperand or one of its siblings) a value
// the program the method runs would read as something other than the one
// name: an option, a pattern matching many names, or a form of the
// program's own grammar that selects something else, such as apt-get's
// trailing "-" (collection.OperandProblem). The method refuses it again before it sends
// anything; this says so before the run, rather than after earlier tasks
// have changed the device.
//
// It shares ParamChoiceRule's scope: a registered, implemented method, and
// a parameter it declares (its own or a fragment's). A value holding a
// template is the engine's to judge once it renders, and a value of the
// wrong type is ParamTypeRule's finding, so neither is judged here.
func ParamOperandRule(world WorldView) []Finding {
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
			if _, isOperand := collection.OperandKind(p.Format); !declared || !isOperand || engine.HasTemplate(value) {
				continue
			}
			// A value of the wrong type is ParamTypeRule's to report;
			// reporting it twice would make one mistake read as two.
			if fits, err := collection.ParamFits(p.Type, value); err != nil || !fits {
				continue
			}
			problem := collection.OperandProblem(p, value)
			if problem == "" {
				continue
			}
			findings = append(findings, Finding{
				RuleName: "param-operand",
				Node:     id,
				Message:  fmt.Sprintf("task %s passes parameter %q a value %s cannot pass on as one name: %s", taskName(id, task), key, task.FQCN, problem),
			})
		}
	}
	return findings
}

func init() {
	Register(ParamOperandRule)
}
