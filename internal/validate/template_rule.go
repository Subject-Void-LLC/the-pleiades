// Package validate: TemplateRule, which checks a task's rendered parameters
// (Phase 117a) before anything runs.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TemplateRule checks every task parameter that holds a template, at plan
// time rather than at the task that would fail:
//
//   - it compiles, with only filters the renderer knows;
//   - each expression reads vars, nodes or result, the three roots a
//     rendered parameter has (internal/engine's render_params.go);
//   - a read of nodes.<register> or result.<register> names a register
//     some other task in the runbook writes. Like SecretMaskRule this is
//     existence, not order, for the reason that rule records (a rescue or
//     always task has no place in a total order); a register read before it
//     is written is still a hard error when the task renders, never a
//     silent empty value;
//   - an expression in a parameter the method marks as command text ends
//     with quote or cli_token, so data from a ticket cannot add a command
//     or an argument;
//   - a parameter the method marks as a URL names its scheme and host, or
//     begins with "/", before its first expression, so data cannot choose
//     where the request goes;
//   - an expression in a parameter the method marks as a project path the
//     run writes reads only vars, so a device's output cannot choose which
//     project file is replaced;
//   - a secret reference, {{ secret.<name>.<field> }}, appears only in a
//     parameter its method resolves itself (collection.Param.SecretRefs),
//     written exactly so and standing apart from any other expression.
//
// A rendered target is held to the same rules, and to within: besides,
// which the runbook builder requires beside it. A task's rollback: steps
// are held to them as its own params are.
func TemplateRule(world WorldView) []Finding {
	eng := render.New()
	registered := make(map[string]bool)
	for _, task := range world.DAG.Nodes {
		if task.Register != "" {
			registered[task.Register] = true
		}
	}

	var findings []Finding
	// Each task's rollback: steps too: they render when the engine undoes
	// the task, held by the executor to the same rules (renderTask).
	for _, pt := range paramTasks(world.DAG) {
		id, task := pt.id, pt.task
		// A condition reads device with no template in any parameter, so
		// this comes before the parameters are looked at.
		if task.LockAcquisition == engine.AcquisitionAllAtPlanTime && (engine.ParamsReadRoot(eng, task.Params, engine.TemplateRootDevice) || conditionReadsDevice(task)) {
			findings = append(findings, Finding{RuleName: "template", Node: id, Message: fmt.Sprintf("task %s: %s", taskName(id, task), engine.ErrDeviceRootLockAll)})
		}
		// A loop written as one expression is rendered before any item exists,
		// so it may read what a parameter may, except the item.
		if src, ok := loopExpression(task); ok {
			if tmpl, err := eng.Compile(src); err != nil {
				findings = append(findings, Finding{RuleName: "template", Node: id, Message: fmt.Sprintf("task %s, loop: %v", taskName(id, task), err)})
			} else {
				for _, problem := range engine.TemplateProblems(tmpl, "") {
					findings = append(findings, Finding{RuleName: "template", Node: id, Message: fmt.Sprintf("task %s, loop: %s", taskName(id, task), problem)})
				}
			}
		}
		if !engine.HasTemplate(task.Params) {
			continue
		}
		formats := engine.ParamFormats(task.FQCN)
		secretRefs := engine.ParamSecretRefs(task.FQCN)
		keys := make([]string, 0, len(task.Params))
		for k := range task.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, source := range engine.TemplateStrings(task.Params[key]) {
				problems := checkTemplate(eng, source, formats[key], registered, task)
				if key == collection.TargetParam {
					if tmpl, err := eng.Compile(source); err == nil {
						problems = append(problems, engine.TargetTemplateProblems(tmpl)...)
					}
				}
				// A secret reference is the method's to resolve, never the
				// renderer's: it is judged by its own rule and not compiled.
				if isRef, refProblems := engine.SecretRefProblems(source, secretRefs[key]); isRef {
					problems = refProblems
				}
				for _, problem := range problems {
					findings = append(findings, Finding{
						RuleName: "template",
						Node:     id,
						Message:  fmt.Sprintf("task %s, parameter %s: %s", taskName(id, task), key, problem),
					})
				}
			}
		}
	}
	return findings
}

// checkTemplate returns what is wrong with one template string, or nothing:
// the executor's own rules (engine.TemplateProblems) plus the one only the
// whole runbook can answer, whether a register it reads exists.
func checkTemplate(eng render.Engine, source string, format collection.ParamFormat, registered map[string]bool, task *engine.Task) []string {
	own := task.Register
	tmpl, err := eng.Compile(source)
	if err != nil {
		return []string{err.Error()}
	}
	problems := engine.TaskTemplateProblems(tmpl, format, task)
	for _, expr := range tmpl.Expressions() {
		root := expr.Path[0]
		if (root != engine.TemplateRootNodes && root != engine.TemplateRootResult) || len(expr.Path) < 2 {
			continue
		}
		switch register := expr.Path[1]; {
		case register == own:
			problems = append(problems, fmt.Sprintf("an expression reads %s.%s, this task's own register, which does not exist until the task has run", root, register))
		case !registered[register]:
			problems = append(problems, fmt.Sprintf("an expression reads %s.%s, and no task in this runbook registers %q", root, register, register))
		}
	}
	return problems
}

// loopExpression returns the task's loop when it is written as an
// expression.
func loopExpression(task *engine.Task) (string, bool) {
	var src any = task.Loop
	if src == nil {
		src = task.WithItems
	}
	text, ok := src.(string)
	return text, ok && text != ""
}

// conditionReadsDevice reports whether task's condition reads the device
// root.
func conditionReadsDevice(task *engine.Task) bool {
	uses, err := engine.ConditionUsesRoot(task.Conditional, engine.TemplateRootDevice)
	return err == nil && uses
}

// taskName is a task's label for a finding: its id and, when it has one,
// its name.
func taskName(id string, task *engine.Task) string {
	if task.Name != "" {
		return fmt.Sprintf("%s (name %q)", id, task.Name)
	}
	return id
}

func init() {
	Register(TemplateRule)
}
