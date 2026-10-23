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
// condition may skip it, and a skipped task reads no parameter.
func VarsRule(world WorldView) []Finding {
	if !world.VariablesKnown {
		return nil
	}
	eng := render.New()
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var findings []Finding
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		if world.DAG.Conditional(id) || !engine.HasTemplate(task.Params) {
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
					Node:     id,
					Message: fmt.Sprintf("task %s, parameter %s: no value is supplied for vars.%s; give it with --extra-vars %s=<value>, or make it optional with | default(...)",
						taskName(id, task), key, name, name),
				})
			}
		}
	}
	return findings
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
