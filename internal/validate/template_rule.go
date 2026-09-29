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
//     where the request goes.
//
// A rendered target is held to the same rules, and to within: besides,
// which the runbook builder requires beside it.
func TemplateRule(world WorldView) []Finding {
	eng := render.New()
	registered := make(map[string]bool)
	for _, task := range world.DAG.Nodes {
		if task.Register != "" {
			registered[task.Register] = true
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
		if !engine.HasTemplate(task.Params) {
			continue
		}
		formats := engine.ParamFormats(task.FQCN)
		keys := make([]string, 0, len(task.Params))
		for k := range task.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, source := range engine.TemplateStrings(task.Params[key]) {
				for _, problem := range checkTemplate(eng, source, formats[key], registered, task.Register) {
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
func checkTemplate(eng render.Engine, source string, format collection.ParamFormat, registered map[string]bool, own string) []string {
	tmpl, err := eng.Compile(source)
	if err != nil {
		return []string{err.Error()}
	}
	problems := engine.TemplateProblems(tmpl, format)
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
