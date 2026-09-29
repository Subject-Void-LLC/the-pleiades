// Rendered task parameters (Phase 117a): a task's params may read the run's
// variables and earlier tasks' registered results through the one template
// renderer (internal/render, PLAN.md Section 25), rendered when the task's
// node dispatches, never when the DAG compiles.
//
// Rendering at dispatch is Phase 87's own gate decision, which this file
// adopts: a result only exists once the task that registers it has run.
// Three roots are readable, and nothing else:
//
//   - vars: the run's variables (a launch's extra variables and survey
//     answers, `pleiades run --extra-vars`), without any secret a bound
//     credential injected (internal/adapters/native's injectedVariables);
//   - nodes: every registered result, register first and then device id,
//     the same snapshot a condition reads;
//   - result: each register that exactly one device (or one device-less
//     task) wrote, by name alone, so an author need not type a device id.
//
// A value with no "{{" is passed through untouched, so a runbook that
// renders nothing costs nothing and behaves as it always did.
package engine

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The roots a rendered parameter may read.
const (
	TemplateRootVars   = "vars"
	TemplateRootNodes  = "nodes"
	TemplateRootResult = "result"
)

// TemplateRoots are the roots a rendered parameter may read, for plan-time
// validation.
func TemplateRoots() []string {
	return []string{TemplateRootNodes, TemplateRootResult, TemplateRootVars}
}

// WithRenderer hands the Executor the template renderer its tasks' params
// render through. A composition root passes the one engine it holds; an
// Executor with none refuses a task whose params hold a template rather
// than handing the method the literal text.
func WithRenderer(eng render.Engine) ExecutorOption {
	return func(x *Executor) { x.renderer = eng }
}

// HasTemplate reports whether v, at any depth of a decoded YAML or JSON
// value, holds a string with a template action in it.
func HasTemplate(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, "{{")
	case map[string]any:
		for _, e := range t {
			if HasTemplate(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if HasTemplate(e) {
				return true
			}
		}
	}
	return false
}

// renderTask returns task with its params rendered, or task itself when no
// param holds a template. The copy shares everything but Params, so the
// DAG's own task is never changed and a later node reads the authored text.
func (r *run) renderTask(task *Task) (*Task, error) {
	if !HasTemplate(task.Params) {
		return task, nil
	}
	if r.x.renderer == nil {
		return nil, errors.New("this run has no template renderer, so a parameter holding {{ cannot be rendered")
	}
	// The rules validation holds a template to are held again here, so a
	// DAG that reached the executor without validation still cannot render
	// data onto a command line or into a request's host.
	if err := checkTaskTemplates(r.x.renderer, task); err != nil {
		return nil, err
	}
	ctx, err := r.templateContext()
	if err != nil {
		return nil, err
	}
	rendered, err := renderValue(r.x.renderer, task.Params, ctx)
	if err != nil {
		return nil, err
	}
	out := *task
	out.Params, _ = rendered.(map[string]any)
	return &out, nil
}

// templateContext is what a rendered parameter reads: vars, nodes and
// result, from the run's variables and its workflow context as they stand
// when the node dispatches.
func (r *run) templateContext() (map[string]any, error) {
	tree, err := r.x.workflow.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read the registered results: %w", err)
	}
	vars := r.x.extraVars
	if vars == nil {
		vars = map[string]any{}
	}
	return map[string]any{
		TemplateRootVars:   vars,
		TemplateRootNodes:  tree,
		TemplateRootResult: singleWriters(tree),
	}, nil
}

// singleWriters returns, from a workflow context snapshot, each register
// that exactly one device (or one device-less task) wrote, keyed by the
// register alone. A register several devices wrote is left out, so reading
// it through result is undefined and says so, rather than picking one.
func singleWriters(tree map[string]any) map[string]any {
	out := make(map[string]any, len(tree))
	for register, raw := range tree {
		byDevice, ok := raw.(map[string]any)
		if !ok || len(byDevice) != 1 {
			continue
		}
		for _, stats := range byDevice {
			out[register] = stats
		}
	}
	return out
}

// renderValue renders every string in v that holds a template, walking maps
// and lists and leaving every other value as it is. A string that is one
// expression and nothing else keeps its value's type (render.Template.Value);
// any other string renders to text.
func renderValue(eng render.Engine, v any, ctx map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		tmpl, err := eng.Compile(t)
		if err != nil {
			return nil, err
		}
		if value, single, err := tmpl.Value(ctx); single {
			return value, err
		}
		return tmpl.Render(ctx)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			rendered, err := renderValue(eng, e, ctx)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			rendered, err := renderValue(eng, e, ctx)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	}
	return v, nil
}

// commandSafeFilters are the filters an expression rendered into command
// text must end with: quote for a POSIX shell, cli_token for a network CLI.
var commandSafeFilters = []string{"quote", "cli_token"}

// TemplateProblems returns what is wrong with one compiled template for a
// parameter of the given format, or nothing: an expression reading a root
// other than vars, nodes or result; in command text, an expression that
// does not end with quote or cli_token; in a URL, text before the first
// expression that does not already fix the scheme and host. Validation and
// the executor share it, so the rule is held once at plan time and again
// when the task renders.
func TemplateProblems(tmpl render.Template, format collection.ParamFormat) []string {
	var problems []string
	for _, expr := range tmpl.Expressions() {
		root := expr.Path[0]
		if !slices.Contains(TemplateRoots(), root) {
			problems = append(problems, fmt.Sprintf("an expression reads %q, and a parameter may read only %s", root, strings.Join(TemplateRoots(), ", ")))
			continue
		}
		if format == collection.ParamFormatCommand && (len(expr.Filters) == 0 || !slices.Contains(commandSafeFilters, expr.Filters[len(expr.Filters)-1])) {
			problems = append(problems, "this parameter is command text, so each expression must end with | quote (a shell) or | cli_token (a network CLI)")
		}
	}
	if format == collection.ParamFormatURL && !URLPrefixFixed(tmpl.Source()) {
		problems = append(problems, "this parameter is a URL, so the text before its first {{ must name the scheme and host and end the host with /, ? or #, or begin with / for a path on the device's API")
	}
	return problems
}

// URLPrefixFixed reports whether the text of source before its first
// expression fixes where the request goes: it begins with "/" (a path on
// the target device's own API), or it holds a scheme, "://", a host, and
// one of "/", "?" or "#" ending the host, so an expression can neither
// choose the host nor extend it ("api.example.com{{ x }}" could become
// "api.example.com.attacker.net").
func URLPrefixFixed(source string) bool {
	prefix, _, _ := strings.Cut(source, "{{")
	if strings.HasPrefix(prefix, "/") {
		return true
	}
	_, rest, ok := strings.Cut(prefix, "://")
	if !ok {
		return false
	}
	return strings.IndexAny(rest, "/?#") > 0
}

// ParamFormats returns each declared parameter's Format for fqcn, or an
// empty map for a name that is not a registered method.
func ParamFormats(fqcn string) map[string]collection.ParamFormat {
	out := map[string]collection.ParamFormat{}
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		return out
	}
	for _, p := range desc.Manifest.Doc.Params {
		if p.Format != "" {
			out[p.Name] = p.Format
		}
	}
	return out
}

// TemplateStrings returns every string in v, at any depth, that holds a
// template action, in a stable order.
func TemplateStrings(v any) []string {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "{{") {
			return []string{t}
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, TemplateStrings(t[k])...)
		}
		return out
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, TemplateStrings(e)...)
		}
		return out
	}
	return nil
}

// checkTaskTemplates holds every templated parameter of task to
// TemplateProblems, returning the first problem as an error naming the
// parameter. A rendered target is held here too, and to within: besides
// (resolveBounded).
func checkTaskTemplates(eng render.Engine, task *Task) error {
	formats := ParamFormats(task.FQCN)
	keys := make([]string, 0, len(task.Params))
	for k := range task.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, source := range TemplateStrings(task.Params[key]) {
			tmpl, err := eng.Compile(source)
			if err != nil {
				return fmt.Errorf("parameter %s: %w", key, err)
			}
			if problems := TemplateProblems(tmpl, formats[key]); len(problems) > 0 {
				return fmt.Errorf("parameter %s: %s", key, problems[0])
			}
		}
	}
	return nil
}

// resolveBounded resolves a target rendered from data (task.Within set):
// the rendered params.target names one or more devices, by name, as text
// (one name, or several separated by commas) or as a list of names, and
// each must be one of the devices within: resolves to. A tag, a pattern or
// a name outside the bound fails the task naming both, and nothing falls
// back to hosts: (FAILURE_PATTERNS 11). The bound is the author's, written
// literally; the names are the data's, and data never gets to name a tag.
func (r *run) resolveBounded(task *Task) ([]inventory.InventoryItem, error) {
	names, err := renderedNames(task.Params[collection.TargetParam])
	if err != nil {
		return nil, err
	}
	bound := r.x.resolver.Resolve(task.Within)
	if len(bound) == 0 {
		return nil, fmt.Errorf("within: %q matches no inventory host or tag", task.Within)
	}
	byName := make(map[string]inventory.InventoryItem, len(bound))
	for _, d := range bound {
		byName[d.Name()] = d
	}
	var out []inventory.InventoryItem
	seen := map[string]bool{}
	for _, name := range names {
		d, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("the rendered target names %q, which is not a device within %q", name, task.Within)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, d)
		}
	}
	return out, nil
}

// renderedNames reads a rendered params.target as device names: text holds
// one name or several separated by commas, and a list holds one name per
// item. Anything else, or no name at all, is refused.
func renderedNames(raw any) ([]string, error) {
	var names []string
	switch t := raw.(type) {
	case string:
		for _, part := range strings.Split(t, ",") {
			if name := strings.TrimSpace(part); name != "" {
				names = append(names, name)
			}
		}
	case []any:
		for _, item := range t {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("the rendered target is a list holding %s, and each item must be a device name", valueKind(item))
			}
			names = append(names, strings.TrimSpace(s))
		}
	default:
		return nil, fmt.Errorf("the rendered target is %s, and it must be a device name, several separated by commas, or a list of names", valueKind(raw))
	}
	if len(names) == 0 {
		return nil, errors.New("the rendered target names no device")
	}
	return names, nil
}
