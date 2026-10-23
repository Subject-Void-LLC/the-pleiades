// Package validate: VarsRule, which finds a task parameter reading a run
// variable nobody supplied, before anything runs.
package validate

import (
	"fmt"
	"slices"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// defaultFilter is the render filter that makes a missing value optional
// out loud ({{ vars.region | default('us-east-1') }}).
const defaultFilter = "default"

// VarsRule refuses a task parameter whose template reads vars.<name> when
// the run supplies no value for name, the failure the renderer would
// otherwise report only when the task dispatches, after the tasks before it
// have already run. It reads only the names each template's expressions
// reference (render.Template.Expressions), never the text around them. An
// expression guarded by | default(...) is optional and is not refused.
//
// It applies only when the caller knows every variable the run will have
// (WorldView.VariablesKnown); otherwise it finds nothing. A task with a
// when: of its own is left to the engine, as TransportRule leaves it: its
// condition may skip it, and a skipped task reads no parameter. Each
// task's rollback: steps are read too (named <node>.rollback[<i>]), since
// they render with the same variables when rollback_on_failure undoes the
// task, and with none at all when `pleiades rollback` replays them; a step
// of a conditional task is left to the engine with its task.
//
// The advice names how to supply a value only where the caller takes one
// (WorldView.VariablesFlag); a command that takes none is told so.
func VarsRule(world WorldView) []Finding {
	if !world.VariablesKnown {
		return nil
	}
	eng := render.New()
	var findings []Finding
	for _, pt := range paramTasks(world.DAG) {
		task := pt.task
		if world.DAG.Conditional(pt.node) || !engine.HasTemplate(task.Params) {
			continue
		}
		keys := make([]string, 0, len(task.Params))
		for k := range task.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, name := range missingVars(eng, task.Params[key], world.Variables) {
				findings = append(findings, Finding{
					RuleName: "vars",
					Node:     pt.id,
					Message: fmt.Sprintf("task %s, parameter %s: no value is supplied for vars.%s; %s",
						taskName(pt.id, task), key, name, varsAdvice(world.VariablesFlag, name)),
				})
			}
		}
	}
	return findings
}

// varsAdvice is how to fix a missing vars.<name>: give it with flag, where
// the command takes one, or otherwise write the value or a default, since
// nothing this command does can supply it.
func varsAdvice(flag, name string) string {
	if flag == "" {
		return "this command takes no variables, so write the value itself, or make it optional with | default(...)"
	}
	return fmt.Sprintf("give it with %s %s=<value>, or make it optional with | default(...)", flag, name)
}

// missingVars returns, once each and in the order first read, the names of
// vars.<name> that the templates in value read without a default and that
// supplied lacks. A template that does not compile is TemplateRule's to
// report, so it is passed over here.
func missingVars(eng render.Engine, value any, supplied map[string]any) []string {
	var missing []string
	for _, source := range engine.TemplateStrings(value) {
		tmpl, err := eng.Compile(source)
		if err != nil {
			continue
		}
		for _, expr := range tmpl.Expressions() {
			if len(expr.Path) < 2 || expr.Path[0] != engine.TemplateRootVars || slices.Contains(expr.Filters, defaultFilter) {
				continue
			}
			name := expr.Path[1]
			if _, ok := supplied[name]; ok || slices.Contains(missing, name) {
				continue
			}
			missing = append(missing, name)
		}
	}
	return missing
}

// init registers VarsRule with the rules Validate runs.
func init() {
	Register(VarsRule)
}
