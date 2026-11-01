// Package validate: ParamTypeRule, which refuses a task parameter whose
// value is not of the type its method declares.
package validate

import (
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ParamTypeRule flags a task passing a parameter a value of a type other
// than the one its method declares (collection.ParamFits). A method's string
// reader takes any other value for absent, so stdin: 4 sent nothing and the
// task passed; an int reader refuses a quoted "22" only at run time, after
// earlier tasks have changed things. Nothing is converted, since the value
// a conversion produces is not always the one written: an unquoted 0644 is
// the number 420, and a mode of "420" is not what its author meant.
//
// A value holding a template is the engine's to judge once it renders
// (internal/engine's renderParams), and an undeclared parameter is
// ParamsRule's finding. Like ParamsRule, it checks only a method that is
// registered and implemented.
func ParamTypeRule(world WorldView) []Finding {
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		if len(task.Params) == 0 || !isCollectionName(task.FQCN) || dottedBuiltin(task.FQCN) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		types := fragment.Types(desc)
		keys := make([]string, 0, len(task.Params))
		for key := range task.Params {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			value := task.Params[key]
			typ, declared := types[key]
			if !declared || engine.HasTemplate(value) {
				continue
			}
			fits, err := collection.ParamFits(typ, value)
			if err != nil || fits {
				continue
			}
			label := id
			if task.Name != "" {
				label = fmt.Sprintf("%s (name %q)", id, task.Name)
			}
			findings = append(findings, Finding{
				RuleName: "param-type",
				Node:     id,
				Message: fmt.Sprintf("task %s passes parameter %q %s, and %s declares it %s%s",
					label, key, collection.ValueKind(value), task.FQCN, typ, typeAdvice(typ, value)),
			})
		}
	}
	return findings
}

// typeAdvice says how to write a value of typ, for the two mistakes YAML
// makes easy: a number or boolean where text was meant, and text where a
// number or boolean was.
func typeAdvice(typ string, value any) string {
	_, isText := value.(string)
	switch {
	case typ == collection.ParamTypeString && !isText:
		return ": quote it to pass text"
	case (typ == collection.ParamTypeInt || typ == collection.ParamTypeFloat || typ == collection.ParamTypeBool) && isText:
		return ": write it unquoted"
	}
	return ""
}

func init() {
	Register(ParamTypeRule)
}
